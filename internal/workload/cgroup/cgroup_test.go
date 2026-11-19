package cgroup_test

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/workload/cgroup"
)

const fixtureRoot = "../../../testdata/linux/workload/cgroup"

func discover(t *testing.T) []cgroup.Scope {
	t.Helper()
	scopes, err := cgroup.Discover(os.DirFS(fixtureRoot))
	if err != nil {
		t.Fatal(err)
	}
	return scopes
}

func scopeByID(t *testing.T, scopes []cgroup.Scope, id string) cgroup.Scope {
	t.Helper()
	for _, scope := range scopes {
		if scope.ID == id {
			return scope
		}
	}
	t.Fatalf("scope %q not found in %v", id, scopes)
	return cgroup.Scope{}
}

func TestDiscoverSelectsOnlyAllowedScopes(t *testing.T) {
	scopes := discover(t)
	ids := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		ids[scope.ID] = true
	}
	if len(scopes) != 2 || !ids["orca_pty_d_1"] || !ids["test-agent-1"] {
		t.Fatalf("scopes = %v, want orca_pty_d_1 and test-agent-1", scopes)
	}
	for _, filtered := range []string{"app-chrome", "sleepy"} {
		if ids[filtered] {
			t.Errorf("scope %q must be filtered", filtered)
		}
	}
}

func TestReadValues(t *testing.T) {
	scopes := discover(t)
	orca := scopeByID(t, scopes, "orca_pty_d_1")
	if orca.MemoryCurrent != 104857600 {
		t.Errorf("orca memory = %d, want 104857600", orca.MemoryCurrent)
	}
	if orca.CPU != (cgroup.CPU{User: 1000000, System: 500000, Nice: 0, Usage: 1500000}) {
		t.Errorf("orca cpu = %+v", orca.CPU)
	}
	if len(orca.PIDs) != 1 || orca.PIDs[0] != 102 {
		t.Errorf("orca pids = %v, want [102]", orca.PIDs)
	}
	app := scopeByID(t, scopes, "test-agent-1")
	if app.MemoryCurrent != 52428800 {
		t.Errorf("app memory = %d, want 52428800", app.MemoryCurrent)
	}
	if app.CPU != (cgroup.CPU{User: 2000000, Usage: 2000000}) {
		t.Errorf("app cpu = %+v", app.CPU)
	}
	if len(app.PIDs) != 1 || app.PIDs[0] != 200 {
		t.Errorf("app pids = %v, want [200]", app.PIDs)
	}
}

func TestPressureConversion(t *testing.T) {
	scopes := discover(t)
	orca := scopeByID(t, scopes, "orca_pty_d_1")
	want := map[string]float64{"cpu": 0.01, "memory": 0.025, "io": 0}
	for resource, value := range want {
		got, ok := orca.Pressure[resource]
		if !ok {
			t.Fatalf("missing pressure %q", resource)
		}
		if got != value {
			t.Errorf("pressure %q = %v, want %v", resource, got, value)
		}
	}
	app := scopeByID(t, scopes, "test-agent-1")
	if len(app.Pressure) != 0 {
		t.Errorf("app pressure = %v, want none", app.Pressure)
	}
}

func TestDiscoverKeepsHealthyScopeOnError(t *testing.T) {
	root := fstest.MapFS{
		"app.slice/good.scope/cgroup.events":   {Data: []byte("populated 1\n")},
		"app.slice/good.scope/memory.current":  {Data: []byte("1024\n")},
		"app.slice/good.scope/cpu.stat":        {Data: []byte("user_usec 1\nsystem_usec 2\nnice_usec 3\nusage_usec 6\n")},
		"app.slice/good.scope/cgroup.procs":    {Data: []byte("7\n")},
		"app.slice/broken.scope/cgroup.events": {Data: []byte("populated 1\n")},
		"app.slice/broken.scope/cpu.stat":      {Data: []byte("user_usec 1\nsystem_usec 2\nnice_usec 3\nusage_usec 6\n")},
		"app.slice/broken.scope/cgroup.procs":  {Data: []byte("8\n")},
	}
	scopes, err := cgroup.Discover(root)
	if err == nil {
		t.Fatal("err = nil, want error for broken scope")
	}
	if len(scopes) != 1 || scopes[0].ID != "good" {
		t.Fatalf("scopes = %v, want only good", scopes)
	}
}
