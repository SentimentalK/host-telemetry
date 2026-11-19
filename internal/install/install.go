package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SentimentalK/host-telemetry/deploy/systemd"
	"github.com/SentimentalK/host-telemetry/internal/config"
)

type Target struct {
	User          bool
	Home          string
	Etc           string
	XDGConfigHome string
	XDGStateHome  string
	Executable    string
}

type Action struct {
	Kind    string
	Path    string
	Mode    string
	Content string
	Command []string
}

func (t Target) ConfigPath() string {
	if t.User {
		return filepath.Join(t.configHome(), "host-telemetry", "config.yaml")
	}
	return filepath.Join(t.etc(), "host-telemetry", "config.yaml")
}

func (t Target) UnitPath() string {
	if t.User {
		return filepath.Join(t.configHome(), "systemd", "user", "host-telemetry.service")
	}
	return filepath.Join(t.etc(), "systemd", "system", "host-telemetry.service")
}

func (t Target) StateDir() string {
	if t.User {
		return filepath.Join(t.stateHome(), "host-telemetry")
	}
	return "/var/lib/host-telemetry"
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

func Plan(t Target, cfg config.Config) ([]Action, error) {
	data, err := config.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return []Action{
		{Kind: "write", Path: t.ConfigPath(), Mode: "0600", Content: string(data)},
		{Kind: "write", Path: t.UnitPath(), Mode: "0644", Content: renderUnit(t)},
		{Kind: "exec", Command: t.SystemctlArgs("daemon-reload")},
		{Kind: "exec", Command: t.SystemctlArgs("enable", "--now", "host-telemetry")},
	}, nil
}

func renderUnit(t Target) string {
	return strings.NewReplacer(
		"{{ExecStart}}", t.Executable,
		"{{ConfigPath}}", t.ConfigPath(),
		"{{StateDir}}", t.StateDir(),
		"{{WantedBy}}", t.WantedBy(),
	).Replace(systemd.Template)
}

func Apply(actions []Action) error {
	for _, action := range actions {
		switch action.Kind {
		case "write":
			if err := write(action); err != nil {
				return err
			}
		case "exec":
			cmd := exec.Command(action.Command[0], action.Command[1:]...)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
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
		case "exec":
			fmt.Fprintf(w, "exec %s\n", strings.Join(action.Command, " "))
		}
	}
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
