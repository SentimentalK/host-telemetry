package hostinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMachineID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "etc/machine-id", "  abc123\n")
	id, err := MachineID(os.DirFS(root))
	if err != nil || id != "abc123" {
		t.Fatalf("ID = %q, %v; want abc123", id, err)
	}

	fallback := t.TempDir()
	writeFile(t, fallback, "var/lib/dbus/machine-id", "dbus456")
	id, err = MachineID(os.DirFS(fallback))
	if err != nil || id != "dbus456" {
		t.Fatalf("ID fallback = %q, %v; want dbus456", id, err)
	}

	empty := t.TempDir()
	writeFile(t, empty, "etc/machine-id", "\n")
	writeFile(t, empty, "var/lib/dbus/machine-id", "dbus789")
	id, err = MachineID(os.DirFS(empty))
	if err != nil || id != "dbus789" {
		t.Fatalf("ID empty primary = %q, %v; want dbus789", id, err)
	}

	if _, err := MachineID(os.DirFS(t.TempDir())); err == nil {
		t.Fatal("ID with no files returned no error")
	}
}

func TestOS(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "proc/sys/kernel/osrelease", "6.9.1-test\n")
	writeFile(t, root, "etc/os-release", "NAME=Test\nPRETTY_NAME=\"Test Linux 1.0\"\n")

	typ, version, description := OS(os.DirFS(root))
	if typ != "linux" || version != "6.9.1-test" || description != "Test Linux 1.0" {
		t.Fatalf("OS = %q, %q, %q", typ, version, description)
	}

	typ, version, description = OS(os.DirFS(t.TempDir()))
	if typ != "linux" || version != "" || description != "" {
		t.Fatalf("OS missing files = %q, %q, %q", typ, version, description)
	}
}

func TestIOPlatformUUID(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "darwin", "ioreg-platform.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	uuid, err := IOPlatformUUID(func(name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return data, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "C02A4B5C-1234-5E6F-8A9B-C0D1E2F34567"; uuid != want {
		t.Fatalf("UUID = %q, want %q", uuid, want)
	}
	if want := "ioreg -rd1 -c IOPlatformExpertDevice"; strings.Join(gotArgs, " ") != want {
		t.Fatalf("argv = %q, want %q", gotArgs, want)
	}

	for name, run := range map[string]func(string, ...string) ([]byte, error){
		"empty output": func(string, ...string) ([]byte, error) { return nil, nil },
		"missing key":  func(string, ...string) ([]byte, error) { return []byte(`"model" = <"MacBookPro18,3">`), nil },
		"empty value":  func(string, ...string) ([]byte, error) { return []byte(`      "IOPlatformUUID" = ""`), nil },
		"command fails": func(string, ...string) ([]byte, error) {
			return nil, errors.New("ioreg: not found")
		},
	} {
		if _, err := IOPlatformUUID(run); err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}

func TestDarwinOS(t *testing.T) {
	responses := map[string]string{
		"-productVersion": "14.6.1",
		"-productName":    "macOS",
		"-buildVersion":   "23G93",
	}
	run := func(name string, args ...string) ([]byte, error) {
		if name != "sw_vers" || len(args) != 1 {
			t.Fatalf("unexpected command %q %q", name, args)
		}
		out, ok := responses[args[0]]
		if !ok {
			return nil, errors.New("unexpected flag")
		}
		return []byte(out + "\n"), nil
	}
	version, description, err := DarwinOS(run)
	if err != nil {
		t.Fatal(err)
	}
	if version != "14.6.1" || description != "macOS 14.6.1 (23G93)" {
		t.Fatalf("version = %q, description = %q", version, description)
	}

	noBuild := func(name string, args ...string) ([]byte, error) {
		if args[0] == "-buildVersion" {
			return nil, errors.New("sw_vers: failed")
		}
		return []byte(responses[args[0]] + "\n"), nil
	}
	version, description, err = DarwinOS(noBuild)
	if err != nil {
		t.Fatal(err)
	}
	if version != "14.6.1" || description != "macOS 14.6.1" {
		t.Fatalf("missing build: version = %q, description = %q", version, description)
	}

	failVersion := func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("sw_vers: failed")
	}
	if _, _, err := DarwinOS(failVersion); err == nil {
		t.Fatal("productVersion failure returned no error")
	}
}
