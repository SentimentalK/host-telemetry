package darwin

import "testing"

func TestParseFilesystemsPOSIXMountpointWithSpace(t *testing.T) {
	mountData := []byte("/dev/disk9 on /Volumes/My Disk (hfs, local, journaled)\n")
	dfData := []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk9 100 40 50 44% /Volumes/My Disk\n")

	stats, err := parseFilesystemsFrom(mountData, dfData, "mount", "df -kP")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("filesystems = %d, want 1", len(stats))
	}
	if stats[0].mountpoint != "/Volumes/My Disk" {
		t.Errorf("mountpoint = %q, want %q", stats[0].mountpoint, "/Volumes/My Disk")
	}
}
