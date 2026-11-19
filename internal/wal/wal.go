package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"log"
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
	recordMagic          = "HTW1"
	recordHeaderSize     = 28
	defaultSegmentSize   = 4 << 20
	defaultRollThreshold = 64 << 10
	cursorName           = "cursor"
)

var (
	errFound         = errors.New("wal: record found")
	errRecordMissing = errors.New("wal: record missing")
	errGap           = errors.New("segments are not sequential")
)

type Batch struct {
	Seq     uint64
	At      time.Time
	Samples int
	Payload []byte
}

type record struct {
	seq     uint64
	samples int
	at      int64
	seg     int
}

type Store struct {
	mu             sync.Mutex
	dir            string
	maxBytes       int64
	rollThreshold  int64
	lock           *dirLock
	seg            int
	healed         map[int]bool
	wl             *wlog.WL
	cursor         uint64
	next           uint64
	dropped        uint64
	pendingSamples int
	records        []record
}

func Open(dir string, maxBytes int64) (*Store, error) {
	return open(dir, maxBytes, defaultSegmentSize, defaultRollThreshold)
}

func open(dir string, maxBytes int64, segmentSize int, rollThreshold int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("wal: create dir: %w", err)
	}
	lock, err := acquireDirLock(dir)
	if err != nil {
		return nil, err
	}
	cursor, err := readCursor(dir)
	if err != nil {
		lock.release()
		return nil, err
	}
	existing, err := segmentIndexes(dir)
	if err != nil {
		lock.release()
		return nil, err
	}
	if err := checkSequential(existing); err != nil {
		lock.release()
		return nil, fmt.Errorf("wal: %s: %w", dir, err)
	}
	writeSegment := 0
	if len(existing) > 0 {
		writeSegment = existing[len(existing)-1] + 1
	}

	writer, err := wlog.NewSize(nil, nil, dir, segmentSize, compression.None)
	if err != nil {
		lock.release()
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	store := &Store{dir: dir, maxBytes: maxBytes, rollThreshold: rollThreshold, wl: writer, lock: lock, seg: writeSegment, healed: map[int]bool{}, cursor: cursor, next: cursor + 1}
	for _, index := range existing {
		err := scanSegment(dir, index, func(seq uint64, at int64, samples int, _ []byte) error {
			if seq >= store.next {
				store.next = seq + 1
			}
			if seq > store.cursor {
				store.records = append(store.records, record{seq: seq, samples: samples, at: at, seg: index})
				store.pendingSamples += samples
			}
			return nil
		})
		if err != nil {
			if index == existing[len(existing)-1] {
				break
			}
			writer.Close()
			lock.release()
			return nil, fmt.Errorf("wal: scan segment %d: %w", index, err)
		}
	}
	return store, nil
}

func (s *Store) Append(at time.Time, samples int, payload []byte) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.next
	encoded := make([]byte, recordHeaderSize+len(payload))
	copy(encoded, recordMagic)
	binary.BigEndian.PutUint64(encoded[4:], seq)
	binary.BigEndian.PutUint64(encoded[12:], uint64(at.UnixNano()))
	binary.BigEndian.PutUint32(encoded[20:], uint32(samples))
	binary.BigEndian.PutUint32(encoded[24:], uint32(len(payload)))
	copy(encoded[recordHeaderSize:], payload)

	if err := s.wl.Log(encoded); err != nil {
		return 0, fmt.Errorf("wal: append: %w", err)
	}
	seg, err := s.currentSegmentLocked()
	if err != nil {
		return 0, fmt.Errorf("wal: locate segment: %w", err)
	}
	s.seg = seg
	s.next++
	s.records = append(s.records, record{seq: seq, samples: samples, at: at.UnixNano(), seg: seg})
	s.pendingSamples += samples
	s.evict()
	return seq, nil
}

func (s *Store) Next() (Batch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextLocked(s.cursor)
}

func (s *Store) Pending() ([]Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	indexes, err := segmentIndexes(s.dir)
	if err != nil {
		return nil, err
	}
	var batches []Batch
	for _, index := range indexes {
		err := scanSegment(s.dir, index, func(seq uint64, at int64, samples int, payload []byte) error {
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
		if err != nil {
			log.Printf("wal: scan segment %d: %v", index, err)
		}
	}
	return batches, nil
}

func (s *Store) nextLocked(after uint64) (Batch, bool, error) {
	for {
		i := sort.Search(len(s.records), func(i int) bool { return s.records[i].seq > after })
		if i == len(s.records) {
			return Batch{}, false, nil
		}
		rec := s.records[i]
		batch, err := s.readBatchLocked(rec)
		if err == nil {
			return batch, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errRecordMissing) {
			return Batch{}, false, err
		}
		s.healLocked(rec, err)
		after = s.cursor
	}
}

func (s *Store) healLocked(rec record, cause error) {
	highest := s.cursor
	dropped := 0
	kept := s.records[:0]
	for _, candidate := range s.records {
		if candidate.seg != rec.seg {
			kept = append(kept, candidate)
			continue
		}
		if candidate.seq > highest {
			highest = candidate.seq
		}
		dropped += candidate.samples
		s.pendingSamples -= candidate.samples
	}
	s.records = kept
	if !s.healed[rec.seg] {
		s.healed[rec.seg] = true
		log.Printf("wal: segment %d is unreadable (%v); skipped %d samples and advanced cursor to %d", rec.seg, cause, dropped, highest)
	}
	if highest > s.cursor {
		s.cursor = highest
		if err := writeCursor(s.dir, highest); err != nil {
			log.Printf("wal: persist cursor after skipping segment %d: %v", rec.seg, err)
		}
	}
	s.dropped += uint64(dropped)
}

func (s *Store) readBatchLocked(rec record) (Batch, error) {
	var payload []byte
	found := false
	err := scanSegment(s.dir, rec.seg, func(seq uint64, _ int64, _ int, data []byte) error {
		if seq != rec.seq {
			return nil
		}
		payload = append([]byte(nil), data...)
		found = true
		return errFound
	})
	if err != nil && !errors.Is(err, errFound) {
		return Batch{}, err
	}
	if !found {
		return Batch{}, fmt.Errorf("%w: %d in segment %d", errRecordMissing, rec.seq, rec.seg)
	}
	return Batch{Seq: rec.seq, At: time.Unix(0, rec.at), Samples: rec.samples, Payload: payload}, nil
}

func (s *Store) Ack(seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if seq <= s.cursor {
		return nil
	}
	if err := writeCursor(s.dir, seq); err != nil {
		return err
	}
	s.advanceCursorLocked(seq)
	s.reclaim()
	return nil
}

func (s *Store) Discard(seq uint64, samples int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if seq <= s.cursor {
		return nil
	}
	if err := writeCursor(s.dir, seq); err != nil {
		return err
	}
	s.dropped += uint64(samples)
	s.advanceCursorLocked(seq)
	s.reclaim()
	return nil
}

func (s *Store) advanceCursorLocked(seq uint64) {
	s.cursor = seq
	i := sort.Search(len(s.records), func(i int) bool { return s.records[i].seq > seq })
	for _, rec := range s.records[:i] {
		s.pendingSamples -= rec.samples
	}
	n := copy(s.records, s.records[i:])
	s.records = s.records[:n]
}

func (s *Store) reclaim() {
	if len(s.records) == 0 && s.sizeLocked() > s.rollThreshold {
		next, err := s.wl.NextSegment()
		if err != nil {
			return
		}
		s.seg = next
		if err := s.wl.Truncate(next); err != nil {
			return
		}
	}
	limit := s.seg
	for i, rec := range s.records {
		if i == 0 || rec.seg < limit {
			limit = rec.seg
		}
	}
	indexes, err := segmentIndexes(s.dir)
	if err != nil {
		return
	}
	for _, index := range indexes {
		if index >= limit {
			return
		}
		if err := s.wl.Truncate(index + 1); err != nil {
			return
		}
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingSamples
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.wl.Close()
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
	return err
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
		dropped := 0
		kept := s.records[:0]
		for _, rec := range s.records {
			if rec.seg == oldest {
				dropped += rec.samples
				s.pendingSamples -= rec.samples
				continue
			}
			kept = append(kept, rec)
		}
		s.records = kept
		if err := s.wl.Truncate(oldest + 1); err != nil {
			return
		}
		s.dropped += uint64(dropped)
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

func checkSequential(indexes []int) error {
	for i := 0; i < len(indexes)-1; i++ {
		if indexes[i]+1 != indexes[i+1] {
			return fmt.Errorf("%w: segment %d is followed by %d", errGap, indexes[i], indexes[i+1])
		}
	}
	return nil
}

func (s *Store) currentSegmentLocked() (int, error) {
	indexes, err := segmentIndexes(s.dir)
	if err != nil {
		return 0, err
	}
	if len(indexes) == 0 {
		return 0, errors.New("no segments")
	}
	return indexes[len(indexes)-1], nil
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
