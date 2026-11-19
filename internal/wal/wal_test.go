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

	store, err := open(dir, 1<<20, 32<<10)
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

	reopened, err := open(dir, 1<<20, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	pending := reopened.Pending()
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
	store, err := open(dir, maxBytes, 32<<10)
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
	store, err := open(dir, 1<<20, 32<<10)
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

	reopened, err := open(dir, 1<<20, 32<<10)
	if err != nil {
		t.Fatalf("reopen torn WAL: %v", err)
	}
	defer reopened.Close()

	pending := reopened.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d batches, want the intact prefix of 1", len(pending))
	}
	if !bytes.Equal(pending[0].Payload, []byte("first")) || pending[0].Samples != 2 {
		t.Fatalf("batch = %+v", pending[0])
	}
}
