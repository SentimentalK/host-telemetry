package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/deploy/launchd"
	"github.com/SentimentalK/host-telemetry/deploy/systemd"
	"github.com/SentimentalK/host-telemetry/deploy/windows"
	"github.com/SentimentalK/host-telemetry/internal/config"
)

const launchdLabel = "dev.host-telemetry"

func DefaultTarget() Target {
	return Target{Platform: runtime.GOOS, User: os.Geteuid() != 0}
}

type Target struct {
	Platform      string
	User          bool
	UID           int
	Home          string
	Etc           string
	XDGConfigHome string
	XDGStateHome  string
	Executable    string
	ProgramData   string
}

type Action struct {
	Kind        string
	Path        string
	Mode        string
	Content     string
	Command     []string
	IgnoreError bool
	Service     *winunit.Spec
}

func (t Target) ConfigPath() string {
	if t.Platform == "windows" {
		return t.windowsBase() + `\config.yaml`
	}
	if t.Platform == "darwin" {
		return filepath.Join(t.darwinBase(), "config.yaml")
	}
	if t.User {
		return filepath.Join(t.configHome(), "host-telemetry", "config.yaml")
	}
	return filepath.Join(t.etc(), "host-telemetry", "config.yaml")
}

func (t Target) UnitPath() string {
	if t.Platform == "darwin" {
		if t.User {
			return filepath.Join(t.home(), "Library", "LaunchAgents", launchdLabel+".plist")
		}
		return filepath.Join("/Library/LaunchDaemons", launchdLabel+".plist")
	}
	if t.User {
		return filepath.Join(t.configHome(), "systemd", "user", "host-telemetry.service")
	}
	return filepath.Join(t.etc(), "systemd", "system", "host-telemetry.service")
}

func (t Target) StateDir() string {
	if t.Platform == "windows" {
		return t.windowsBase()
	}
	if t.Platform == "darwin" {
		return t.darwinBase()
	}
	if t.User {
		return filepath.Join(t.stateHome(), "host-telemetry")
	}
	return "/var/lib/host-telemetry"
}

func (t Target) LogPath() string {
	if t.Platform == "windows" {
		return t.windowsBase() + `\host-telemetry.log`
	}
	return filepath.Join(t.darwinBase(), "host-telemetry.log")
}

func (t Target) SystemctlArgs(args ...string) []string {
	base := []string{"systemctl"}
	if t.User {
		base = append(base, "--user")
	}
	return append(base, args...)
}

func (t Target) WantedBy() string {
	if t.User {
		return "default.target"
	}
	return "multi-user.target"
}

func (t Target) home() string {
	if t.Home != "" {
		return t.Home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func (t Target) configHome() string {
	if t.XDGConfigHome != "" {
		return t.XDGConfigHome
	}
	return filepath.Join(t.home(), ".config")
}

func (t Target) stateHome() string {
	if t.XDGStateHome != "" {
		return t.XDGStateHome
	}
	return filepath.Join(t.home(), ".local", "state")
}

func (t Target) etc() string {
	if t.Etc != "" {
		return t.Etc
	}
	return "/etc"
}

func (t Target) darwinBase() string {
	if t.User {
		return filepath.Join(t.home(), "Library", "Application Support", "host-telemetry")
	}
	return "/Library/Application Support/host-telemetry"
}

func (t Target) programData() string {
	if t.ProgramData != "" {
		return t.ProgramData
	}
	if data := os.Getenv("ProgramData"); data != "" {
		return data
	}
	return `C:\ProgramData`
}

func (t Target) windowsBase() string {
	return t.programData() + `\host-telemetry`
}

func (t Target) launchdDomain() string {
	if !t.User {
		return "system"
	}
	uid := t.UID
	if uid == 0 {
		uid = os.Getuid()
	}
	return "gui/" + strconv.Itoa(uid)
}

func Plan(t Target, cfg config.Config) ([]Action, error) {
	data, err := config.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if t.Platform == "windows" {
		return planWindows(t, data), nil
	}
	if t.Platform == "darwin" {
		return planLaunchd(t, data), nil
	}
	return []Action{
		{Kind: "write", Path: t.ConfigPath(), Mode: "0600", Content: string(data)},
		{Kind: "write", Path: t.UnitPath(), Mode: "0644", Content: renderUnit(t)},
		{Kind: "exec", Command: t.SystemctlArgs("daemon-reload")},
		{Kind: "exec", Command: t.SystemctlArgs("enable", "--now", "host-telemetry")},
	}, nil
}

func planWindows(t Target, data []byte) []Action {
	spec := winunit.DefaultSpec(t.Executable, t.ConfigPath())
	configPath := t.ConfigPath()
	return []Action{
		{Kind: "write", Path: configPath, Mode: "0600", Content: string(data)},
		{Kind: "exec", Command: []string{"icacls", configPath, "/inheritance:r", "/grant:r", "*S-1-5-18:(F)", "*S-1-5-32-544:(F)"}},
		{Kind: "service", Path: configPath, Service: &spec},
	}
}

func planLaunchd(t Target, data []byte) []Action {
	domain := t.launchdDomain()
	return []Action{
		{Kind: "write", Path: t.ConfigPath(), Mode: "0600", Content: string(data)},
		{Kind: "write", Path: t.UnitPath(), Mode: "0644", Content: renderPlist(t)},
		{Kind: "exec", Command: []string{"launchctl", "bootout", domain + "/" + launchdLabel}, IgnoreError: true},
		{Kind: "exec", Command: []string{"launchctl", "bootstrap", domain, t.UnitPath()}},
		{Kind: "exec", Command: []string{"launchctl", "enable", domain + "/" + launchdLabel}},
		{Kind: "exec", Command: []string{"launchctl", "kickstart", "-k", domain + "/" + launchdLabel}},
	}
}

func renderUnit(t Target) string {
	return strings.NewReplacer(
		"{{ExecStart}}", t.Executable,
		"{{ConfigPath}}", t.ConfigPath(),
		"{{StateDir}}", t.StateDir(),
		"{{WantedBy}}", t.WantedBy(),
	).Replace(systemd.Template)
}

func renderPlist(t Target) string {
	return strings.NewReplacer(
		"{{Executable}}", t.Executable,
		"{{ConfigPath}}", t.ConfigPath(),
		"{{StateDir}}", t.StateDir(),
		"{{LogPath}}", t.LogPath(),
	).Replace(launchd.Template)
}

func Apply(actions []Action) error {
	for _, action := range actions {
		switch action.Kind {
		case "write":
			if err := write(action); err != nil {
				return err
			}
		case "service":
			if action.Service == nil {
				return fmt.Errorf("install: service action has no spec")
			}
			if err := applyService(*action.Service); err != nil {
				return err
			}
		case "exec":
			cmd := exec.Command(action.Command[0], action.Command[1:]...)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil && !action.IgnoreError {
				return err
			}
		default:
			return fmt.Errorf("install: unknown action kind %q", action.Kind)
		}
	}
	return nil
}

func Render(w io.Writer, actions []Action) {
	for _, action := range actions {
		switch action.Kind {
		case "write":
			fmt.Fprintf(w, "write %s (%s)\n%s", action.Path, action.Mode, action.Content)
			if !strings.HasSuffix(action.Content, "\n") {
				fmt.Fprintln(w)
			}
		case "service":
			renderService(w, action)
		case "exec":
			prefix := "exec"
			if action.IgnoreError {
				prefix = "exec (ignore-errors)"
			}
			fmt.Fprintf(w, "%s %s\n", prefix, strings.Join(action.Command, " "))
		}
	}
}

func renderService(w io.Writer, action Action) {
	spec := action.Service
	if spec == nil {
		return
	}
	fmt.Fprintf(w, "service %s\n", spec.Name)
	fmt.Fprintf(w, "  display-name %s\n", spec.DisplayName)
	fmt.Fprintf(w, "  description %s\n", spec.Description)
	fmt.Fprintf(w, "  binary %s\n", winunit.CommandLine(*spec))
	fmt.Fprintf(w, "  start %s\n", spec.StartType)
	fmt.Fprintf(w, "  dependencies %s\n", strings.Join(spec.Dependencies, ", "))
	fmt.Fprintf(w, "  failure %s, reset %s\n", failureText(spec.FailureActions), durationText(spec.FailureReset))
	fmt.Fprintf(w, "  config %s\n", action.Path)
}

func failureText(actions []winunit.FailureAction) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		verb := "none"
		if action.Restart {
			verb = "restart"
		}
		parts = append(parts, verb+" after "+durationText(action.Delay))
	}
	return strings.Join(parts, ", ")
}

func durationText(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return d.String()
}

func write(action Action) error {
	mode, err := strconv.ParseUint(action.Mode, 8, 32)
	if err != nil {
		return fmt.Errorf("install: %s: invalid mode %q: %w", action.Path, action.Mode, err)
	}
	old, err := os.ReadFile(action.Path)
	if err == nil && string(old) == action.Content {
		return nil
	}
	dir := filepath.Dir(action.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".host-telemetry-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(os.FileMode(mode)); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(action.Content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, action.Path)
}
