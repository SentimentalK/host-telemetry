package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lockName = "LOCK"

type dirLock struct {
	file *os.File
}

func acquireDirLock(dir string) (*dirLock, error) {
	path := filepath.Join(dir, lockName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("wal: open lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		holder, _ := readHolder(file)
		file.Close()
		if holder == "" {
			holder = "unknown holder"
		}
		return nil, fmt.Errorf("wal: directory %s is locked by %s: %w", dir, holder, err)
	}
	if err := writeHolder(file); err != nil {
		unlockFile(file)
		file.Close()
		return nil, err
	}
	return &dirLock{file: file}, nil
}

func (l *dirLock) release() {
	if err := l.file.Truncate(0); err != nil {
		_ = err
	}
	unlockFile(l.file)
	l.file.Close()
}

func readHolder(file *os.File) (string, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if n == 0 && err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func writeHolder(file *os.File) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "unknown"
	}
	holder := fmt.Sprintf("pid=%d exe=%s since=%s", os.Getpid(), exe, time.Now().Format(time.RFC3339))
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("wal: truncate lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("wal: seek lock: %w", err)
	}
	if _, err := file.WriteString(holder + "\n"); err != nil {
		return fmt.Errorf("wal: write lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("wal: sync lock: %w", err)
	}
	return nil
}
