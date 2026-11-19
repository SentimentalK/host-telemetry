package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/tsdb/wlog"
	"github.com/prometheus/prometheus/util/compression"
)

const (
	recordMagic        = "HTW1"
	recordHeaderSize   = 28
	defaultSegmentSize = 128 << 20
	cursorName         = "cursor"
)

type Batch struct {
	Seq     uint64
	At      time.Time
	Samples int
	Payload []byte
}

type Store struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
	wl       *wlog.WL
	cursor   uint64
	next     uint64
	dropped  uint64
}

func Open(dir string, maxBytes int64) (*Store, error) {
	return open(dir, maxBytes, defaultSegmentSize)
}

func open(dir string, maxBytes int64, segmentSize int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("wal: create dir: %w", err)
	}
	cursor, err := readCursor(dir)
	if err != nil {
		return nil, err
	}
	existing, err := segmentIndexes(dir)
	if err != nil {
		return nil, err
	}
	lastExisting := -1
	if len(existing) > 0 {
		lastExisting = existing[len(existing)-1]
	}

	writer, err := wlog.NewSize(nil, nil, dir, segmentSize, compression.None)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	store := &Store{dir: dir, maxBytes: maxBytes, wl: writer, cursor: cursor, next: cursor + 1}
	for _, index := range existing {
		err := scanSegment(dir, index, func(seq uint64, _ int64, _ int, _ []byte) error {
			if seq >= store.next {
				store.next = seq + 1
			}
			return nil
		})
		if err != nil {
			if index == lastExisting {
				break
			}
			writer.Close()
			return nil, fmt.Errorf("wal: scan segment %d: %w", index, err)
		}
	}
	return store, nil
}

func (s *Store) Append(at time.Time, samples int, payload []byte) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.next
	record := make([]byte, recordHeaderSize+len(payload))
	copy(record, recordMagic)
	binary.BigEndian.PutUint64(record[4:], seq)
	binary.BigEndian.PutUint64(record[12:], uint64(at.UnixNano()))
	binary.BigEndian.PutUint32(record[20:], uint32(samples))
	binary.BigEndian.PutUint32(record[24:], uint32(len(payload)))
	copy(record[recordHeaderSize:], payload)

	if err := s.wl.Log(record); err != nil {
		return 0, fmt.Errorf("wal: append: %w", err)
	}
	s.next++
	s.evict()
	return seq, nil
}

func (s *Store) Pending() []Batch {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexes, err := segmentIndexes(s.dir)
	if err != nil {
		return nil
	}
	var batches []Batch
	for _, index := range indexes {
		_ = scanSegment(s.dir, index, func(seq uint64, at int64, samples int, payload []byte) error {
			if seq <= s.cursor {
				return nil
			}
			batches = append(batches, Batch{
				Seq:     seq,
				At:      time.Unix(0, at),
				Samples: samples,
				Payload: append([]byte(nil), payload...),
			})
			return nil
		})
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].Seq < batches[j].Seq })
	return batches
}

func (s *Store) Ack(seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if seq <= s.cursor {
		return nil
	}
	s.cursor = seq
	return writeCursor(s.dir, seq)
}

func (s *Store) Discard(seq uint64, samples int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dropped += uint64(samples)
	if seq <= s.cursor {
		return nil
	}
	s.cursor = seq
	return writeCursor(s.dir, seq)
}

func (s *Store) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizeLocked()
}

func (s *Store) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

func (s *Store) PendingSamples() int {
	pending := s.Pending()
	total := 0
	for _, batch := range pending {
		total += batch.Samples
	}
	return total
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wl.Close()
}

func (s *Store) evict() {
	for {
		indexes, err := segmentIndexes(s.dir)
		if err != nil || len(indexes) <= 1 {
			return
		}
		if s.sizeLocked() <= s.maxBytes {
			return
		}
		oldest := indexes[0]
		samples := 0
		_ = scanSegment(s.dir, oldest, func(_ uint64, _ int64, count int, _ []byte) error {
			samples += count
			return nil
		})
		if err := s.wl.Truncate(oldest + 1); err != nil {
			return
		}
		s.dropped += uint64(samples)
	}
}

func (s *Store) sizeLocked() int64 {
	indexes, err := segmentIndexes(s.dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, index := range indexes {
		info, err := os.Stat(filepath.Join(s.dir, segmentName(index)))
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

func scanSegment(dir string, index int, fn func(seq uint64, at int64, samples int, payload []byte) error) error {
	file, err := os.Open(filepath.Join(dir, segmentName(index)))
	if err != nil {
		return err
	}
	defer file.Close()

	reader := wlog.NewReader(file)
	for reader.Next() {
		seq, at, samples, payload, err := parseRecord(reader.Record())
		if err != nil {
			return err
		}
		if err := fn(seq, at, samples, payload); err != nil {
			return err
		}
	}
	return reader.Err()
}

func parseRecord(record []byte) (uint64, int64, int, []byte, error) {
	if len(record) < recordHeaderSize {
		return 0, 0, 0, nil, fmt.Errorf("record too short: %d", len(record))
	}
	if string(record[:4]) != recordMagic {
		return 0, 0, 0, nil, fmt.Errorf("bad record magic %q", record[:4])
	}
	seq := binary.BigEndian.Uint64(record[4:])
	at := int64(binary.BigEndian.Uint64(record[12:]))
	samples := binary.BigEndian.Uint32(record[20:])
	length := binary.BigEndian.Uint32(record[24:])
	if int(length) != len(record)-recordHeaderSize {
		return 0, 0, 0, nil, fmt.Errorf("record %d: payload length %d, have %d", seq, length, len(record)-recordHeaderSize)
	}
	return seq, at, int(samples), record[recordHeaderSize:], nil
}

func segmentName(index int) string {
	return fmt.Sprintf("%08d", index)
}

func segmentIndexes(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var indexes []int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		index, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes, nil
}

func readCursor(dir string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(dir, cursorName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("wal: read cursor: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("wal: parse cursor: %w", err)
	}
	return cursor, nil
}

func writeCursor(dir string, seq uint64) error {
	temp, err := os.CreateTemp(dir, ".cursor-")
	if err != nil {
		return fmt.Errorf("wal: create cursor: %w", err)
	}
	name := temp.Name()
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("wal: chmod cursor: %w", err)
	}
	if _, err := temp.WriteString(strconv.FormatUint(seq, 10) + "\n"); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("wal: write cursor: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		os.Remove(name)
		return fmt.Errorf("wal: sync cursor: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("wal: close cursor: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, cursorName)); err != nil {
		os.Remove(name)
		return fmt.Errorf("wal: rename cursor: %w", err)
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("wal: open dir: %w", err)
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("wal: sync dir: %w", err)
	}
	return nil
}
