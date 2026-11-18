package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	upstreamDir = "internal/schema/model/upstream"
	lockPath    = "internal/schema/semconv.lock"
	projectPath = "internal/schema/model/project.yaml"
	outputPath  = "internal/schema/schema_gen.go"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "schema/gen:", err)
		os.Exit(1)
	}
}

func run() error {
	lock, err := readLock(lockPath)
	if err != nil {
		return err
	}
	files, err := verifyUpstream(upstreamDir, lock)
	if err != nil {
		return err
	}

	docs := make([]*document, 0, len(files)+1)
	for _, rel := range files {
		doc, err := readDocument(filepath.Join(upstreamDir, rel))
		if err != nil {
			return err
		}
		docs = append(docs, doc)
	}
	project, err := readDocument(projectPath)
	if err != nil {
		return err
	}
	docs = append(docs, project)

	model, err := build(docs)
	if err != nil {
		return err
	}
	src, err := emit(model, lock.Version)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, src, 0o644)
}
