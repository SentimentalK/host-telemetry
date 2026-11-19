package events

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const ClockSkew = "host.clock.skew"

type Event struct {
	Time   time.Time
	Name   string
	JumpMS int64
}

type Writer struct {
	Path string
	mu   sync.Mutex
}

type record struct {
	Time   string `json:"time"`
	Name   string `json:"event.name"`
	JumpMS int64  `json:"jump_ms"`
}

func (w *Writer) Append(ev Event) error {
	line, err := json.Marshal(record{Time: ev.Time.Format(time.RFC3339Nano), Name: ev.Name, JumpMS: ev.JumpMS})
	if err != nil {
		return err
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	file, err := os.OpenFile(w.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return err
	}
	return file.Sync()
}
