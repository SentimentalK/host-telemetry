package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/config"
)

func testConfig() config.Config {
	return config.Config{
		Endpoint: "http://127.0.0.1:8428/opentelemetry/api/v1/push",
		Token:    "test",
		HostID:   "c32e69d5cb5c43ec914ddd62b271c773",
		HostName: "box",
	}
}

func TestPlanUser(t *testing.T) {
	home := t.TempDir()
	target := Target{User: true, Home: home, Executable: "/usr/local/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 4 {
		t.Fatalf("actions = %d, want 4", len(actions))
	}

	configAction, unitAction := actions[0], actions[1]
	wantConfig := filepath.Join(home, ".config", "host-telemetry", "config.yaml")
	if configAction.Kind != "write" || configAction.Path != wantConfig || configAction.Mode != "0600" {
		t.Fatalf("config action = %+v", configAction)
	}
	wantUnit := filepath.Join(home, ".config", "systemd", "user", "host-telemetry.service")
	if unitAction.Kind != "write" || unitAction.Path != wantUnit || unitAction.Mode != "0644" {
		t.Fatalf("unit action = %+v", unitAction)
	}
	for _, want := range []string{
		"Restart=always",
		"RestartSec=5",
		"MemoryMax=128M",
		"CPUQuota=2%",
		"ProtectSystem=strict",
		"NoNewPrivileges=yes",
	} {
		if !strings.Contains(unitAction.Content, want) {
			t.Fatalf("unit missing %q:\n%s", want, unitAction.Content)
		}
	}
	if !strings.Contains(unitAction.Content, "ExecStart=/usr/local/bin/host-telemetry run --config "+wantConfig) {
		t.Fatalf("unit ExecStart wrong:\n%s", unitAction.Content)
	}
	if !strings.Contains(unitAction.Content, "WantedBy=default.target") {
		t.Fatalf("unit WantedBy wrong:\n%s", unitAction.Content)
	}
	if !strings.Contains(unitAction.Content, "ReadWritePaths="+filepath.Join(home, ".local", "state", "host-telemetry")) {
		t.Fatalf("unit ReadWritePaths wrong:\n%s", unitAction.Content)
	}

	wantDaemon := []string{"systemctl", "--user", "daemon-reload"}
	if got := actions[2]; got.Kind != "exec" || strings.Join(got.Command, " ") != strings.Join(wantDaemon, " ") {
		t.Fatalf("daemon-reload action = %+v", got)
	}
	wantEnable := []string{"systemctl", "--user", "enable", "--now", "host-telemetry"}
	if got := actions[3]; got.Kind != "exec" || strings.Join(got.Command, " ") != strings.Join(wantEnable, " ") {
		t.Fatalf("enable action = %+v", got)
	}
}

func TestPlanSystem(t *testing.T) {
	etc := filepath.Join(t.TempDir(), "etc")
	target := Target{Etc: etc, Home: t.TempDir(), Executable: "/usr/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := actions[0].Path, filepath.Join(etc, "host-telemetry", "config.yaml"); got != want {
		t.Fatalf("config path = %q, want %q", got, want)
	}
	if got, want := actions[1].Path, filepath.Join(etc, "systemd", "system", "host-telemetry.service"); got != want {
		t.Fatalf("unit path = %q, want %q", got, want)
	}
	if !strings.Contains(actions[1].Content, "WantedBy=multi-user.target") {
		t.Fatalf("unit WantedBy wrong:\n%s", actions[1].Content)
	}
	if !strings.Contains(actions[1].Content, "ReadWritePaths=/var/lib/host-telemetry") {
		t.Fatalf("unit ReadWritePaths wrong:\n%s", actions[1].Content)
	}
	if got := actions[2].Command[0]; got != "systemctl" {
		t.Fatalf("daemon-reload argv = %v", actions[2].Command)
	}
	for _, arg := range append(actions[2].Command, actions[3].Command...) {
		if arg == "--user" {
			t.Fatalf("system argv contains --user: %v %v", actions[2].Command, actions[3].Command)
		}
	}
}

func TestApplyIdempotent(t *testing.T) {
	home := t.TempDir()
	target := Target{User: true, Home: home, XDGStateHome: filepath.Join(home, "state"), Executable: "/usr/local/bin/host-telemetry"}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	writes := actions[:2]
	if err := Apply(writes); err != nil {
		t.Fatal(err)
	}
	paths := []string{actions[0].Path, actions[1].Path}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(actions[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	if err := Apply(writes); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before[i], after) {
			t.Fatalf("%s changed on second apply", path)
		}
	}
}

func TestRender(t *testing.T) {
	actions, err := Plan(Target{User: true, Home: t.TempDir(), Executable: "/usr/local/bin/host-telemetry"}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	Render(&buf, actions)
	got := buf.String()
	if !strings.Contains(got, actions[1].Content) {
		t.Fatalf("Render missing unit content:\n%s", got)
	}
	if !strings.Contains(got, "write "+actions[0].Path+" (0600)") {
		t.Fatalf("Render missing config header:\n%s", got)
	}
	if !strings.Contains(got, "exec systemctl --user enable --now host-telemetry") {
		t.Fatalf("Render missing enable command:\n%s", got)
	}
}
