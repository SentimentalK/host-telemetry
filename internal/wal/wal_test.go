package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendReplayAndAck(t *testing.T) {
	dir := t.TempDir()
	at1 := time.Unix(100, 1)
	at2 := time.Unix(200, 2)

	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	seq1, err := store.Append(at1, 2, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := store.Append(at2, 3, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if seq2 != seq1+1 {
		t.Fatalf("sequence %d then %d", seq1, seq2)
	}
	if err := store.Ack(seq1); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	pending, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d batches, want 1", len(pending))
	}
	batch := pending[0]
	if batch.Seq != seq2 || batch.Samples != 3 || !bytes.Equal(batch.Payload, []byte("second")) || !batch.At.Equal(at2) {
		t.Fatalf("batch = %+v", batch)
	}
	if reopened.PendingSamples() != 3 {
		t.Fatalf("pending samples = %d, want 3", reopened.PendingSamples())
	}
	seq3, err := reopened.Append(time.Unix(300, 0), 1, []byte("third"))
	if err != nil {
		t.Fatal(err)
	}
	if seq3 != seq2+1 {
		t.Fatalf("sequence after reopen = %d, want %d", seq3, seq2+1)
	}
}

func TestEviction(t *testing.T) {
	dir := t.TempDir()
	const maxBytes = 64 << 10
	store, err := open(dir, maxBytes, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	payload := bytes.Repeat([]byte("x"), 1000)
	const total = 100
	for i := range total {
		if _, err := store.Append(time.Unix(int64(i), 0), 1, payload); err != nil {
			t.Fatal(err)
		}
	}
	if store.Size() > maxBytes {
		t.Fatalf("size = %d, want <= %d", store.Size(), maxBytes)
	}
	if store.Dropped() == 0 {
		t.Fatal("no samples were dropped")
	}
	if got := store.PendingSamples() + int(store.Dropped()); got != total {
		t.Fatalf("pending+dropped = %d, want %d", got, total)
	}
}

func TestTornTail(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(time.Unix(10, 0), 2, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(time.Unix(20, 0), 3, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := segmentIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(dir, segmentName(segments[len(segments)-1]))
	torn := int64(recordHeaderSize + len("first") + 10)
	if err := os.Truncate(last, torn); err != nil {
		t.Fatal(err)
	}

	reopened, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatalf("reopen torn WAL: %v", err)
	}
	defer reopened.Close()

	pending, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d batches, want the intact prefix of 1", len(pending))
	}
	if !bytes.Equal(pending[0].Payload, []byte("first")) || pending[0].Samples != 2 {
		t.Fatalf("batch = %+v", pending[0])
	}
}

func TestReclaimAfterAck(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var last uint64
	for i := range 12 {
		seq, err := store.Append(time.Unix(int64(i), 0), 1, []byte("payload"))
		if err != nil {
			t.Fatal(err)
		}
		last = seq
	}
	if err := store.Ack(last); err != nil {
		t.Fatal(err)
	}
	if store.PendingSamples() != 0 {
		t.Fatalf("pending samples = %d, want 0", store.PendingSamples())
	}
	if store.Size() > 2*32<<10 {
		t.Fatalf("size = %d, want <= %d", store.Size(), 2*32<<10)
	}
	pending, err := store.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %d batches, want 0", len(pending))
	}

	seq, err := store.Append(time.Unix(100, 0), 1, []byte("after-ack"))
	if err != nil {
		t.Fatal(err)
	}
	batch, ok, err := store.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || batch.Seq != seq || !bytes.Equal(batch.Payload, []byte("after-ack")) {
		t.Fatalf("next = %+v, %v", batch, ok)
	}
}

func TestDroppedCountsOnlyUnacked(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 64<<10, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Append(time.Unix(1, 0), 1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	seqTwo, err := store.Append(time.Unix(2, 0), 2, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(time.Unix(3, 0), 7, []byte("three")); err != nil {
		t.Fatal(err)
	}
	if err := store.Ack(seqTwo); err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("x"), 1000)
	for i := range 200 {
		if _, err := store.Append(time.Unix(10+int64(i), 0), 0, payload); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.Dropped(); got != 7 {
		t.Fatalf("dropped = %d, want only the unacked 7", got)
	}
}

func TestPendingSamplesNoScan(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.Append(time.Unix(1, 0), 4, []byte("a")); err != nil {
		t.Fatal(err)
	}
	seqTwo, err := store.Append(time.Unix(2, 0), 5, []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	seqThree, err := store.Append(time.Unix(3, 0), 6, []byte("c"))
	if err != nil {
		t.Fatal(err)
	}
	if store.PendingSamples() != 15 {
		t.Fatalf("pending samples = %d, want 15", store.PendingSamples())
	}
	if err := store.Ack(seqTwo); err != nil {
		t.Fatal(err)
	}
	if store.PendingSamples() != 6 {
		t.Fatalf("pending samples after ack = %d, want 6", store.PendingSamples())
	}
	pending, err := store.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Seq != seqThree {
		t.Fatalf("pending = %+v, want only %d", pending, seqThree)
	}
}

func TestNextAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	payload := bytes.Repeat([]byte("z"), 1000)
	const total = 100
	want := make([]uint64, 0, total)
	for i := range total {
		seq, err := store.Append(time.Unix(int64(i), 0), 1, payload)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, seq)
	}
	segments, err := segmentIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) < 2 {
		t.Fatalf("segments = %d, want multiple", len(segments))
	}

	var got []uint64
	for {
		batch, ok, err := store.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, batch.Seq)
		if len(batch.Payload) != len(payload) {
			t.Fatalf("batch %d payload = %d bytes", batch.Seq, len(batch.Payload))
		}
		if err := store.Ack(batch.Seq); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("next returned %d batches, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batch %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRollWhenDrained(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 64<<10, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	payload := bytes.Repeat([]byte("p"), 100)
	var last uint64
	for i := range 5 {
		last, err = store.Append(time.Unix(int64(i), 0), 1, payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Ack(last); err != nil {
		t.Fatal(err)
	}
	if store.PendingSamples() != 0 {
		t.Fatalf("pending samples = %d, want 0", store.PendingSamples())
	}
	if store.Size() > 32<<10 {
		t.Fatalf("size after drain = %d, want <= %d", store.Size(), 32<<10)
	}

	seq, err := store.Append(time.Unix(9, 0), 1, payload)
	if err != nil {
		t.Fatal(err)
	}
	batch, ok, err := store.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || batch.Seq != seq {
		t.Fatalf("next = %+v, %v", batch, ok)
	}
	if err := store.Ack(seq); err != nil {
		t.Fatal(err)
	}
	if store.Size() > 32<<10 {
		t.Fatalf("size after second drain = %d, want <= %d", store.Size(), 32<<10)
	}
}

func TestPendingSinglePass(t *testing.T) {
	dir := t.TempDir()
	store, err := open(dir, 1<<20, 32<<10, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	payload := bytes.Repeat([]byte("q"), 20<<10)
	want := make([]uint64, 0, 3)
	for i := range 3 {
		seq, err := store.Append(time.Unix(int64(i), 0), i+1, payload)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, seq)
	}
	segments, err := segmentIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) < 2 {
		t.Fatalf("segments = %d, want multiple", len(segments))
	}

	pending, err := store.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != len(want) {
		t.Fatalf("pending = %d batches, want %d", len(pending), len(want))
	}
	for i, batch := range pending {
		if batch.Seq != want[i] {
			t.Fatalf("batch %d seq = %d, want %d", i, batch.Seq, want[i])
		}
		if batch.Samples != i+1 {
			t.Fatalf("batch %d samples = %d, want %d", i, batch.Samples, i+1)
		}
		if !bytes.Equal(batch.Payload, payload) {
			t.Fatalf("batch %d payload mismatch", i)
		}
	}
}
