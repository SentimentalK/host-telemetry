package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLockMatchesVendoredModel(t *testing.T) {
	lock := readLock(t, "semconv.lock")

	dir := filepath.Join("model", "upstream")
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(lock) {
		t.Fatalf("%s has %d files, semconv.lock lists %d", dir, len(files), len(lock))
	}

	sort.Strings(files)
	for _, rel := range files {
		want, ok := lock[rel]
		if !ok {
			t.Fatalf("%s is not listed in semconv.lock", filepath.Join(dir, rel))
		}
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s: sha256 %s, semconv.lock expects %s", filepath.Join(dir, rel), got, want)
		}
	}
	for rel := range lock {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("semconv.lock lists %s: %v", rel, err)
		}
	}
}

func readLock(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	inFiles := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			inFiles = strings.HasPrefix(line, "files:")
			continue
		}
		if !inFiles {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		files[key] = strings.TrimSpace(value)
	}
	if len(files) == 0 {
		t.Fatalf("%s: no files listed", path)
	}
	return files
}
