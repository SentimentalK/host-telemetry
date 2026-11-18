package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type lockFile struct {
	Version string            `yaml:"version"`
	Files   map[string]string `yaml:"files"`
}

func readLock(path string) (*lockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf lockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if lf.Version == "" {
		return nil, fmt.Errorf("%s: missing version", path)
	}
	if len(lf.Files) == 0 {
		return nil, fmt.Errorf("%s: missing files", path)
	}
	return &lf, nil
}

func verifyUpstream(dir string, lf *lockFile) ([]string, error) {
	onDisk := make(map[string]bool, len(lf.Files))
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
		rel = filepath.ToSlash(rel)
		onDisk[rel] = true
		want, ok := lf.Files[rel]
		if !ok {
			return fmt.Errorf("%s: not listed in %s", path, lockPath)
		}
		sum, err := fileDigest(path)
		if err != nil {
			return err
		}
		if sum != want {
			return fmt.Errorf("%s: sha256 %s, %s expects %s", path, sum, lockPath, want)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rels := make([]string, 0, len(lf.Files))
	for rel := range lf.Files {
		if !onDisk[rel] {
			return nil, fmt.Errorf("%s: missing file %s", dir, rel)
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	return rels, nil
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
