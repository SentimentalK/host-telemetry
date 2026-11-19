//go:build linux

package events

import (
	"testing"
	"testing/fstest"
	"time"
)

func attrsOf(ev Event) map[string]string {
	attrs := make(map[string]string, len(ev.Attrs))
	for _, attr := range ev.Attrs {
		attrs[attr.Key] = attr.Value
	}
	return attrs
}

func TestOOMDetectorIgnoresNVRMOutOfMemory(t *testing.T) {
	journal := []byte(
		"Nov 19 10:00:00 host kernel: NVRM: Xid (PCI:0000:01:00): 13, pid 900, Graphics Exception: Out of memory [NV_ERR_NO_MEMORY]\n" +
			"Nov 19 10:00:01 host kernel: Out of memory: Killed process 1234 (agent) total-vm:9000kB, anon-rss:2048kB, file-rss:0kB\n",
	)
	var since time.Time
	fsys := fstest.MapFS{
		"proc/1234/cgroup": &fstest.MapFile{Data: []byte("0::/user.slice/user-1000.slice/session-2.scope\n")},
	}
	now := time.Date(2026, 11, 19, 10, 1, 0, 0, time.UTC)
	detector := LinuxDetectors(DetectorOptions{
		FS: fsys,
		Journal: func(at time.Time) ([]byte, error) {
			since = at
			return journal, nil
		},
	})[0]

	got := detector.Poll(now)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1: %+v", len(got), got)
	}
	event := got[0]
	if event.Name != OOMKill {
		t.Errorf("name = %q, want %q", event.Name, OOMKill)
	}
	if event.Subject != "1234" {
		t.Errorf("subject = %q, want 1234", event.Subject)
	}
	attrs := attrsOf(event)
	if attrs["pid"] != "1234" {
		t.Errorf("pid = %q, want 1234", attrs["pid"])
	}
	if attrs["rss"] != "2097152" {
		t.Errorf("rss = %q, want 2097152", attrs["rss"])
	}
	if attrs["workload.id"] != "session-2" {
		t.Errorf("workload.id = %q, want session-2", attrs["workload.id"])
	}
	if want := now.Add(-5 * time.Minute); !since.Equal(want) {
		t.Errorf("journal since = %s, want %s", since, want)
	}

	later := now.Add(time.Minute)
	detector.Poll(later)
	if !since.Equal(now) {
		t.Errorf("second poll since = %s, want %s", since, now)
	}
}

func TestOOMDetectorMissingWorkload(t *testing.T) {
	journal := []byte("Out of memory: Killed process 77 (java) anon-rss:1024kB\n")
	detector := LinuxDetectors(DetectorOptions{
		FS:      fstest.MapFS{},
		Journal: func(time.Time) ([]byte, error) { return journal, nil },
	})[0]

	got := detector.Poll(time.Now())
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if value, ok := attrsOf(got[0])["workload.id"]; !ok || value != "" {
		t.Fatalf("workload.id = %q (present %v), want empty", value, ok)
	}
}

func TestDiskDetectorThreshold(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/mounts": &fstest.MapFile{Data: []byte(
			"ext4 / ext4 rw,relatime 0 0\n" +
				"proc /proc proc rw,nosuid 0 0\n",
		)},
	}
	now := time.Date(2026, 11, 19, 10, 1, 0, 0, time.UTC)
	bfree := uint64(5)
	var paths []string
	opts := DetectorOptions{
		FS: fsys,
		Statfs: func(path string) (Statfs, error) {
			paths = append(paths, path)
			return Statfs{Blocks: 100, Bfree: bfree}, nil
		},
	}
	detector := LinuxDetectors(opts)[1]

	got := detector.Poll(now)
	if len(got) != 1 {
		t.Fatalf("events at 0.95 = %d, want 1", len(got))
	}
	if attrs := attrsOf(got[0]); attrs["mountpoint"] != "/" || attrs["used_ratio"] != "0.950000" {
		t.Fatalf("attributes = %v, want mountpoint=/ used_ratio=0.950000", attrs)
	}
	if got[0].Subject != "/" {
		t.Errorf("subject = %q, want /", got[0].Subject)
	}
	if len(paths) != 1 || paths[0] != "/" {
		t.Errorf("statfs paths = %v, want [/]", paths)
	}

	bfree = 6
	if got := detector.Poll(now); len(got) != 0 {
		t.Fatalf("events at 0.94 = %d, want 0", len(got))
	}
}

func TestDiskDetectorMountFilters(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 1, 0, 0, time.UTC)
	cases := []struct {
		name      string
		mounts    string
		bfree     uint64
		want      int
		wantRatio string
	}{
		{"read-only", "ext4 /mnt ext4 ro,nosuid,nodev 0 0\n", 1, 0, ""},
		{"read-write below", "ext4 /data ext4 rw,relatime 0 0\n", 6, 0, ""},
		{"read-write full", "ext4 /data ext4 rw,relatime 0 0\n", 4, 1, "0.960000"},
		{"image filesystem", "ext4 /img squashfs rw 0 0\n", 1, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DetectorOptions{
				FS: fstest.MapFS{"proc/mounts": &fstest.MapFile{Data: []byte(tc.mounts)}},
				Statfs: func(string) (Statfs, error) {
					return Statfs{Blocks: 100, Bfree: tc.bfree}, nil
				},
			}
			got := LinuxDetectors(opts)[1].Poll(now)
			if len(got) != tc.want {
				t.Fatalf("events = %d, want %d", len(got), tc.want)
			}
			if tc.wantRatio != "" {
				if attrs := attrsOf(got[0]); attrs["used_ratio"] != tc.wantRatio {
					t.Fatalf("used_ratio = %q, want %q", attrs["used_ratio"], tc.wantRatio)
				}
			}
		})
	}
}

func thermalFS(temp, cur string) fstest.MapFS {
	return fstest.MapFS{
		"sys/class/thermal/thermal_zone0/type":                 &fstest.MapFile{Data: []byte("x86_pkg_temp\n")},
		"sys/class/thermal/thermal_zone0/temp":                 &fstest.MapFile{Data: []byte(temp + "\n")},
		"sys/class/thermal/thermal_zone0/trip_point_0_temp":    &fstest.MapFile{Data: []byte("90000\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq": &fstest.MapFile{Data: []byte("3000000\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq": &fstest.MapFile{Data: []byte(cur + "\n")},
	}
}

func TestThermalDetectorConditions(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 1, 0, 0, time.UTC)

	triggered := LinuxDetectors(DetectorOptions{FS: thermalFS("95000", "1200000")})[2]
	got := triggered.Poll(now)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	attrs := attrsOf(got[0])
	if attrs["cpu"] != "x86_pkg_temp" || attrs["from_hz"] != "3000000000" || attrs["to_hz"] != "1200000000" {
		t.Fatalf("attributes = %v", attrs)
	}
	if got[0].Subject != "x86_pkg_temp" {
		t.Errorf("subject = %q, want x86_pkg_temp", got[0].Subject)
	}

	belowTrip := LinuxDetectors(DetectorOptions{FS: thermalFS("85000", "1200000")})[2]
	if got := belowTrip.Poll(now); len(got) != 0 {
		t.Fatalf("events below trip = %d, want 0", len(got))
	}

	atMax := LinuxDetectors(DetectorOptions{FS: thermalFS("95000", "3000000")})[2]
	if got := atMax.Poll(now); len(got) != 0 {
		t.Fatalf("events at max frequency = %d, want 0", len(got))
	}

	missing := LinuxDetectors(DetectorOptions{FS: fstest.MapFS{}})[2]
	if got := missing.Poll(now); len(got) != 0 {
		t.Fatalf("events without thermal zone = %d, want 0", len(got))
	}
}

func TestThermalDetectorSkipsNegativeTrip(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 1, 0, 0, time.UTC)
	fsys := fstest.MapFS{
		"sys/class/thermal/thermal_zone0/type":                 &fstest.MapFile{Data: []byte("iwlwifi_1\n")},
		"sys/class/thermal/thermal_zone0/temp":                 &fstest.MapFile{Data: []byte("61000\n")},
		"sys/class/thermal/thermal_zone0/trip_point_0_temp":    &fstest.MapFile{Data: []byte("-274000\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq": &fstest.MapFile{Data: []byte("5000000\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq": &fstest.MapFile{Data: []byte("3400000\n")},
	}
	detector := LinuxDetectors(DetectorOptions{FS: fsys})[2]

	if got := detector.Poll(now); len(got) != 0 {
		t.Fatalf("events with negative trip = %d, want 0", len(got))
	}

	fsys["sys/class/thermal/thermal_zone0/trip_point_0_temp"] = &fstest.MapFile{Data: []byte("50000\n")}
	got := detector.Poll(now)
	if len(got) != 1 {
		t.Fatalf("events with valid trip = %d, want 1", len(got))
	}
	attrs := attrsOf(got[0])
	if attrs["from_hz"] != "5000000000" || attrs["to_hz"] != "3400000000" {
		t.Fatalf("attributes = %v, want from_hz=5000000000 to_hz=3400000000", attrs)
	}
}
