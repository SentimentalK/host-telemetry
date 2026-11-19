package hostinfo

import (
	"os"
	"path/filepath"
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

func TestID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "etc/machine-id", "  abc123\n")
	id, err := ID(os.DirFS(root))
	if err != nil || id != "abc123" {
		t.Fatalf("ID = %q, %v; want abc123", id, err)
	}

	fallback := t.TempDir()
	writeFile(t, fallback, "var/lib/dbus/machine-id", "dbus456")
	id, err = ID(os.DirFS(fallback))
	if err != nil || id != "dbus456" {
		t.Fatalf("ID fallback = %q, %v; want dbus456", id, err)
	}

	empty := t.TempDir()
	writeFile(t, empty, "etc/machine-id", "\n")
	writeFile(t, empty, "var/lib/dbus/machine-id", "dbus789")
	id, err = ID(os.DirFS(empty))
	if err != nil || id != "dbus789" {
		t.Fatalf("ID empty primary = %q, %v; want dbus789", id, err)
	}

	if _, err := ID(os.DirFS(t.TempDir())); err == nil {
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
