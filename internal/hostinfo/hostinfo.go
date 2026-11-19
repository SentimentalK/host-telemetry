package hostinfo

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

func MachineID(root fs.FS) (string, error) {
	for _, path := range []string{"etc/machine-id", "var/lib/dbus/machine-id"} {
		data, err := fs.ReadFile(root, path)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}
	return "", errors.New("hostinfo: machine id not found")
}

func Name() (string, error) {
	return os.Hostname()
}

func Arch() string {
	return runtime.GOARCH
}

func OS(root fs.FS) (typ, version, description string) {
	typ = runtime.GOOS
	if typ != "linux" {
		return typ, "", ""
	}
	if data, err := fs.ReadFile(root, "proc/sys/kernel/osrelease"); err == nil {
		version = strings.TrimSpace(string(data))
	}
	if data, err := fs.ReadFile(root, "etc/os-release"); err == nil {
		description = prettyName(string(data))
	}
	return typ, version, description
}

func prettyName(data string) string {
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "PRETTY_NAME" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}
