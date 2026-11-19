package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveModeAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	want := Config{
		Endpoint: "http://127.0.0.1:8428/opentelemetry/api/v1/push",
		Token:    "test",
		HostID:   "c32e69d5cb5c43ec914ddd62b271c773",
		HostName: "box",
		Listen:   "127.0.0.1:9101",
		WALDir:   "/tmp/wal",
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadNotExist(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	if got != (Config{}) {
		t.Fatalf("config = %+v, want zero", got)
	}
}

func TestWithEnvOverridesNonEmpty(t *testing.T) {
	base := Config{Endpoint: "http://config", Token: "config", Listen: "config", WALDir: "config"}
	env := map[string]string{
		"HT_ENDPOINT": "http://env",
		"HT_TOKEN":    "",
		"HT_LISTEN":   "0.0.0.0:1",
		"HT_WAL_DIR":  "",
	}
	got := base.WithEnv(func(key string) string { return env[key] })
	want := Config{Endpoint: "http://env", Token: "config", Listen: "0.0.0.0:1", WALDir: "config"}
	if got != want {
		t.Fatalf("WithEnv = %+v, want %+v", got, want)
	}
}
