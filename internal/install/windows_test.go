package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/deploy/windows"
)

func TestWindowsPlan(t *testing.T) {
	t.Setenv("ProgramData", "")
	executable := `C:\Program Files\host-telemetry\host-telemetry.exe`
	target := Target{Platform: "windows", Executable: executable}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(actions))
	}

	configPath := `C:\ProgramData\host-telemetry\config.yaml`
	writeAction := actions[0]
	if writeAction.Kind != "write" || writeAction.Path != configPath || writeAction.Mode != "0600" {
		t.Fatalf("config action = %+v", writeAction)
	}
	if target.ConfigPath() != configPath {
		t.Fatalf("ConfigPath = %q, want %q", target.ConfigPath(), configPath)
	}
	if target.StateDir() != `C:\ProgramData\host-telemetry` {
		t.Fatalf("StateDir = %q", target.StateDir())
	}
	if target.LogPath() != `C:\ProgramData\host-telemetry\host-telemetry.log` {
		t.Fatalf("LogPath = %q", target.LogPath())
	}

	acl := actions[1]
	wantACL := []string{"icacls", configPath, "/inheritance:r", "/grant:r", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)"}
	if acl.Kind != "exec" || len(acl.Command) != len(wantACL) {
		t.Fatalf("acl action = %+v", acl)
	}
	for i, want := range wantACL {
		if acl.Command[i] != want {
			t.Fatalf("acl command[%d] = %q, want %q", i, acl.Command[i], want)
		}
	}

	serviceAction := actions[2]
	if serviceAction.Kind != "service" || serviceAction.Service == nil {
		t.Fatalf("service action = %+v", serviceAction)
	}
	spec := *serviceAction.Service
	if spec.Name != "host-telemetry" || spec.StartType != "auto" {
		t.Fatalf("service spec = %+v", spec)
	}
	if len(spec.Dependencies) != 1 || spec.Dependencies[0] != "Tcpip" {
		t.Fatalf("dependencies = %v", spec.Dependencies)
	}
	if spec.FailureReset != 24*time.Hour {
		t.Fatalf("failure reset = %s, want 24h", spec.FailureReset)
	}
	wantDelays := []time.Duration{5 * time.Second, 15 * time.Second, 60 * time.Second}
	if len(spec.FailureActions) != len(wantDelays) {
		t.Fatalf("failure actions = %v", spec.FailureActions)
	}
	for i, want := range wantDelays {
		if !spec.FailureActions[i].Restart || spec.FailureActions[i].Delay != want {
			t.Fatalf("failure action %d = %+v, want restart after %s", i, spec.FailureActions[i], want)
		}
	}
	wantCommand := `"C:\Program Files\host-telemetry\host-telemetry.exe" run --config C:\ProgramData\host-telemetry\config.yaml`
	if got := winunit.CommandLine(spec); got != wantCommand {
		t.Fatalf("command line = %q, want %q", got, wantCommand)
	}
	if len(spec.Arguments) != 3 || spec.Arguments[0] != "run" || spec.Arguments[1] != "--config" || spec.Arguments[2] != configPath {
		t.Fatalf("arguments = %v", spec.Arguments)
	}
}

func TestWindowsQuote(t *testing.T) {
	cases := map[string]string{
		`C:\host-telemetry.exe`:       `C:\host-telemetry.exe`,
		`C:\Program Files\host.exe`:   `"C:\Program Files\host.exe"`,
		`a	b`:                         `"a	b"`,
		`a"b`:                         `"a\"b"`,
		`C:\path with space\`:         `"C:\path with space\\"`,
		`C:\path with space\"quoted"`: `"C:\path with space\\\"quoted\""`,
	}
	for input, want := range cases {
		if got := winunit.Quote(input); got != want {
			t.Fatalf("Quote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsDryRunGolden(t *testing.T) {
	t.Setenv("ProgramData", "")
	target := Target{Platform: "windows", Executable: `C:\Program Files\host-telemetry\host-telemetry.exe`}
	actions, err := Plan(target, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	Render(&buf, actions)

	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "windows", "install-dry-run.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("dry-run output differs from golden:\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

func TestWindowsDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ProgramData", dir)
	actions, err := Plan(Target{Platform: "windows", Executable: `C:\host-telemetry.exe`}, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	Render(&buf, actions)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry run wrote %d entries to %s", len(entries), dir)
	}
}
