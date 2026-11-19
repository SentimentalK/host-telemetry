package darwin_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
)

type fakeRunner struct {
	dir   string
	files map[string]string
	extra map[string][]byte
	calls []string
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, key)
	if data, ok := f.extra[key]; ok {
		return data, nil
	}
	file, ok := f.files[key]
	if !ok {
		return nil, fmt.Errorf("unexpected command %q", key)
	}
	return os.ReadFile(filepath.Join(f.dir, file))
}

func (f *fakeRunner) hasCall(key string) bool {
	for _, call := range f.calls {
		if call == key {
			return true
		}
	}
	return false
}

func fixtureDir() string {
	return filepath.Join(fixtureRoot, "real-synthetic-01")
}

func pointNamed(points []schema.Point, name string) (schema.Point, bool) {
	for _, point := range points {
		if point.Name == name {
			return point, true
		}
	}
	return schema.Point{}, false
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLiveUptime(t *testing.T) {
	raw, err := strconv.ParseInt(strings.TrimSpace(string(readFixture(t, "boottime.txt"))), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	boot := make([]byte, 16)
	binary.LittleEndian.PutUint64(boot, uint64(raw))
	runner := &fakeRunner{dir: fixtureDir(), extra: map[string][]byte{"sysctl -b kern.boottime": boot}}
	now := func() time.Time { return time.Unix(1763412355, 0) }
	sources := darwin.NewLive(darwin.Options{Run: runner.run, Now: now})

	points, err := collect(t, sources, "darwin-uptime")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Name != schema.SystemUptime || points[0].Value != 12355 {
		t.Fatalf("points = %v, want %s = 12355", points, schema.SystemUptime)
	}
}

func TestLiveDisk(t *testing.T) {
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"ioreg -c IOBlockStorageDriver -r -l -w 0": "ioreg.txt",
	}}
	sources := darwin.NewLive(darwin.Options{Run: runner.run})

	points, err := collect(t, sources, "darwin-disk")
	if err != nil {
		t.Fatal(err)
	}
	point, ok := pointWithAttr(points, schema.SystemDiskIO, schema.AttrSystemDeviceName, "disk0")
	if !ok {
		t.Fatalf("%s for disk0 missing", schema.SystemDiskIO)
	}
	if point.Value != 368261685248 {
		t.Errorf("%s = %v, want 368261685248", schema.SystemDiskIO, point.Value)
	}
}

func TestLiveNetwork(t *testing.T) {
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"netstat -ibdnW": "netstat.txt",
	}}
	sources := darwin.NewLive(darwin.Options{Run: runner.run})

	points, err := collect(t, sources, "darwin-network")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.hasCall("netstat -ibdnW") {
		t.Errorf("runner calls = %v, want netstat -ibdnW", runner.calls)
	}
	if got := countPoints(points, schema.SystemNetworkIO); got == 0 {
		t.Errorf("%s points = 0", schema.SystemNetworkIO)
	}
}

func TestLiveFilesystem(t *testing.T) {
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"mount":  "mount.txt",
		"df -kP": "df.txt",
	}}
	sources := darwin.NewLive(darwin.Options{Run: runner.run})

	points, err := collect(t, sources, "darwin-filesystem")
	if err != nil {
		t.Fatal(err)
	}
	point, ok := pointWithAttr(points, schema.SystemFilesystemUsage, schema.AttrSystemFilesystemMountpoint, "/")
	if !ok {
		t.Fatalf("%s for / missing", schema.SystemFilesystemUsage)
	}
	if point.Value != 13221340*1024 {
		t.Errorf("%s = %v, want %v", schema.SystemFilesystemUsage, point.Value, 13221340*1024)
	}
}

func TestLiveProcess(t *testing.T) {
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"ps -axo stat=": "ps.txt",
		"sysctl -a":     "sysctl.txt",
	}}
	sources := darwin.NewLive(darwin.Options{Run: runner.run})

	points, err := collect(t, sources, "darwin-process")
	if err != nil {
		t.Fatal(err)
	}
	limit, ok := pointNamed(points, schema.SystemProcessLimit)
	if !ok {
		t.Fatalf("%s missing", schema.SystemProcessLimit)
	}
	if limit.Value != 4176 {
		t.Errorf("%s = %v, want 4176", schema.SystemProcessLimit, limit.Value)
	}
	if got := countPoints(points, schema.SystemProcessCount); got == 0 {
		t.Errorf("%s points = 0", schema.SystemProcessCount)
	}
}

func TestLiveProcessWithoutMaxproc(t *testing.T) {
	sysctl := readFixture(t, "sysctl.txt")
	filtered := []byte(strings.Replace(string(sysctl), "kern.maxproc: 4176\n", "", 1))
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"ps -axo stat=": "ps.txt",
	}}
	runner.extra = map[string][]byte{"sysctl -a": filtered}
	sources := darwin.NewLive(darwin.Options{Run: runner.run})

	points, err := collect(t, sources, "darwin-process")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pointNamed(points, schema.SystemProcessLimit); ok {
		t.Errorf("%s emitted without kern.maxproc", schema.SystemProcessLimit)
	}
	if got := countPoints(points, schema.SystemProcessCount); got == 0 {
		t.Errorf("%s points = 0", schema.SystemProcessCount)
	}
}

func TestLiveSensors(t *testing.T) {
	runner := &fakeRunner{dir: fixtureDir(), files: map[string]string{
		"macmon pipe -s 1 -i 1000": "macmon.json",
	}}
	sources := darwin.NewLive(darwin.Options{Run: runner.run, Sensors: true})

	points, err := collect(t, sources, "darwin-sensors")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pointWithAttr(points, schema.HWTemperature, schema.AttrHWID, "cpu"); !ok {
		t.Errorf("%s for cpu missing", schema.HWTemperature)
	}
}

func TestLiveSensorsMissingIsSilent(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("%s: not found", name)
	}
	sources := darwin.NewLive(darwin.Options{Run: run, Sensors: true})

	points, err := collect(t, sources, "darwin-sensors")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("points = %d, want 0", len(points))
	}
}

func TestLiveSensorsMalformed(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{"temp":`), nil
	}
	sources := darwin.NewLive(darwin.Options{Run: run, Sensors: true})

	if _, err := collect(t, sources, "darwin-sensors"); err == nil {
		t.Fatal("darwin-sensors accepted malformed JSON")
	}
}
