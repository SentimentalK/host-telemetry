package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/wal"
)

func TestServeShutdownPreservesWAL(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, serveOptions{
			Fixtures: "../../testdata",
			Platform: "windows",
			Listen:   "127.0.0.1:0",
			Endpoint: "http://127.0.0.1:1/opentelemetry/api/v1/push",
			WALDir:   dir,
		})
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err == nil && len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wal directory %s stayed empty", dir)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancel")
	}

	store, err := wal.Open(dir, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	batches, err := store.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) == 0 {
		t.Fatal("pending batches = 0, want at least 1")
	}
	samples := 0
	for _, batch := range batches {
		samples += batch.Samples
	}
	if samples == 0 {
		t.Fatal("pending samples = 0, want more")
	}
}

func TestInstallDryRunWindowsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ProgramData", dir)

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = old }()

	provision("install", []string{"--dry-run", "--platform", "windows", "--host-id", "11111111-1111-1111-1111-111111111111"})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()

	got := string(out)
	for _, want := range []string{"service host-telemetry", "start auto", "icacls", "config.yaml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, got)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry run wrote %d entries to %s", len(entries), dir)
	}
}
