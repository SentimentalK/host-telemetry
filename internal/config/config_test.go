package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadNotExist(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	if !reflect.DeepEqual(got, Config{}) {
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WithEnv = %+v, want %+v", got, want)
	}
}

func TestWithEnvWorkload(t *testing.T) {
	base := Config{Workload: WorkloadConfig{Executables: []string{"from-config"}}}
	env := map[string]string{
		"HT_WORKLOAD_ENABLED":     "true",
		"HT_WORKLOAD_CGROUPS":     "false",
		"HT_WORKLOAD_EXECUTABLES": " agent , worker ,",
	}
	got := base.WithEnv(func(key string) string { return env[key] })
	if !got.Workload.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if got.Workload.Cgroups == nil || *got.Workload.Cgroups {
		t.Fatalf("Cgroups = %v, want false", got.Workload.Cgroups)
	}
	if want := []string{"agent", "worker"}; !reflect.DeepEqual(got.Workload.Executables, want) {
		t.Fatalf("Executables = %v, want %v", got.Workload.Executables, want)
	}
}

func TestWorkloadCgroupsDefaultsToNil(t *testing.T) {
	got := Config{}.WithEnv(func(string) string { return "" })
	if got.Workload.Cgroups != nil {
		t.Fatalf("Cgroups = %v, want nil", got.Workload.Cgroups)
	}
	if got.Workload.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
}
