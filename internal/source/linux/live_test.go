package linux_test

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

const realDump = "../../../testdata/linux/real-00"

func copyDump(t *testing.T) string {
	t.Helper()
	src := os.DirFS(realDump)
	dst := t.TempDir()
	err := fs.WalkDir(src, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, path)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func collectSource(t *testing.T, dir, name string) ([]schema.Point, error) {
	t.Helper()
	statfs, err := linux.FixtureStatfs(os.DirFS(dir), "statfs.tsv")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range linux.NewLive(os.DirFS(dir), linux.Options{Statfs: statfs}) {
		if src.Name() == name {
			return src.Collect(context.Background())
		}
	}
	t.Fatalf("no source named %q", name)
	return nil, nil
}

func TestLivePartialFailure(t *testing.T) {
	t.Run("missing pressure", func(t *testing.T) {
		dir := copyDump(t)
		if err := os.RemoveAll(filepath.Join(dir, "proc/pressure")); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-system")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			if strings.HasPrefix(point.Name, "system.pressure.") {
				t.Errorf("emitted %s without proc/pressure", point.Name)
			}
		}
	})

	t.Run("truncated proc/stat", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/stat")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		if err := os.WriteFile(path, []byte(lines[0]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := collectSource(t, dir, "linux-system"); err == nil {
			t.Fatal("truncated proc/stat did not return an error")
		}
	})

	t.Run("unlimited process limit", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/self/limits")
		if err := os.WriteFile(path, []byte("Max processes unlimited unlimited processes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-process")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			if point.Name == schema.SystemProcessLimit {
				t.Errorf("emitted %s for an unlimited limit", schema.SystemProcessLimit)
			}
		}
	})

	t.Run("missing mountinfo", func(t *testing.T) {
		dir := copyDump(t)
		if err := os.Remove(filepath.Join(dir, "proc/self/mountinfo")); err != nil {
			t.Fatal(err)
		}
		if _, err := collectSource(t, dir, "linux-filesystem"); err == nil {
			t.Fatal("missing proc/self/mountinfo did not return an error")
		}
	})

	t.Run("unknown filesystem type", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/self/mountinfo")
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("999 44 0:99 / /unknown rw - tmpfs tmpfs rw\n"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-filesystem")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		if len(points) == 0 {
			t.Fatal("no filesystem points emitted")
		}
		for _, point := range points {
			for _, attr := range point.Attrs {
				if attr.Key == schema.AttrSystemFilesystemMountpoint && attr.Value == "/unknown" {
					t.Errorf("emitted %s for an unsupported filesystem type", point.Name)
				}
			}
		}
	})

	t.Run("empty hwmon", func(t *testing.T) {
		dir := copyDump(t)
		if err := os.RemoveAll(filepath.Join(dir, "sys/class/hwmon")); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-hwmon")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			switch point.Name {
			case schema.HWTemperature, schema.HWFanSpeed, schema.HWPower:
				t.Errorf("emitted %s without hwmon sensors", point.Name)
			}
		}
	})
}

type testFS struct {
	fs.FS
	delay  time.Duration
	bad    string
	badErr error
}

func (t testFS) ReadFile(name string) ([]byte, error) {
	time.Sleep(t.delay)
	if name == t.bad {
		if t.badErr != nil {
			return nil, t.badErr
		}
		return nil, fmt.Errorf("injected read failure")
	}
	return fs.ReadFile(t.FS, name)
}

func hwmonFixture() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/hwmon/hwmon0/name":        {Data: []byte("fake\n")},
		"sys/class/hwmon/hwmon0/temp1_input": {Data: []byte("42000\n")},
	}
}

func findSource(t *testing.T, sources []source.Source, name string) source.Source {
	t.Helper()
	for _, src := range sources {
		if src.Name() == name {
			return src
		}
	}
	t.Fatalf("no source named %q", name)
	return nil
}

func metricValue(points []schema.Point, name string) (float64, bool) {
	for _, point := range points {
		if point.Name == name {
			return point.Value, true
		}
	}
	return 0, false
}

func TestHwmonBoundedWait(t *testing.T) {
	src := findSource(t, linux.NewLive(testFS{FS: hwmonFixture(), delay: 50 * time.Millisecond}, linux.Options{}), "linux-hwmon")

	start := time.Now()
	points, err := src.Collect(context.Background())
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("first collect blocked for %s", elapsed)
	}
	if err != nil {
		t.Fatalf("first collect error: %v", err)
	}
	if _, ok := metricValue(points, schema.HWTemperature); ok {
		t.Fatal("first collect returned a snapshot before the background read finished")
	}

	time.Sleep(60 * time.Millisecond)

	start = time.Now()
	points, err = src.Collect(context.Background())
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Fatalf("second collect blocked for %s", elapsed)
	}
	if err != nil {
		t.Fatalf("second collect error: %v", err)
	}
	value, ok := metricValue(points, schema.HWTemperature)
	if !ok {
		t.Fatal("second collect did not return the background snapshot")
	}
	if value != 42 {
		t.Fatalf("%s = %v, want 42", schema.HWTemperature, value)
	}
}

func TestHwmonBackgroundError(t *testing.T) {
	src := findSource(t, linux.NewLive(testFS{
		FS:    hwmonFixture(),
		delay: 20 * time.Millisecond,
		bad:   "sys/class/hwmon/hwmon0/temp1_input",
	}, linux.Options{}), "linux-hwmon")

	if _, err := src.Collect(context.Background()); err != nil {
		t.Fatalf("first collect error: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	points, err := src.Collect(context.Background())
	if err == nil {
		t.Fatal("second collect did not return the background read error")
	}
	if _, ok := metricValue(points, schema.HWTemperature); ok {
		t.Fatal("failed read replaced the previous snapshot")
	}
}

func hwmonSensorValue(points []schema.Point, name, id string) (float64, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		for _, attr := range point.Attrs {
			if attr.Key == schema.AttrHWID && attr.Value == id {
				return point.Value, true
			}
		}
	}
	return 0, false
}

func TestHwmonSkipsAbsentSensor(t *testing.T) {
	fixture := fstest.MapFS{
		"sys/class/hwmon/hwmon0/name":        {Data: []byte("fake\n")},
		"sys/class/hwmon/hwmon0/temp1_input": {Data: []byte("41000\n")},
		"sys/class/hwmon/hwmon0/temp2_input": {Data: []byte("43000\n")},
	}
	src := findSource(t, linux.NewLive(testFS{
		FS:     fixture,
		bad:    "sys/class/hwmon/hwmon0/temp1_input",
		badErr: fs.ErrNotExist,
	}, linux.Options{}), "linux-hwmon")

	if _, err := src.Collect(context.Background()); err != nil {
		t.Fatalf("first collect error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	points, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("absent sensor reported an error: %v", err)
	}
	if value, ok := hwmonSensorValue(points, schema.HWTemperature, "hwmon0/temp2"); !ok || value != 43 {
		t.Fatalf("temp2 = %v (present %v), want 43", value, ok)
	}
	if _, ok := hwmonSensorValue(points, schema.HWTemperature, "hwmon0/temp1"); ok {
		t.Fatal("absent sensor was emitted")
	}
}

func TestHwmonBadValueIsError(t *testing.T) {
	fixture := fstest.MapFS{
		"sys/class/hwmon/hwmon0/name":        {Data: []byte("fake\n")},
		"sys/class/hwmon/hwmon0/temp1_input": {Data: []byte("broken\n")},
	}
	src := findSource(t, linux.NewLive(testFS{FS: fixture}, linux.Options{}), "linux-hwmon")

	_, _ = src.Collect(context.Background())
	time.Sleep(20 * time.Millisecond)
	if _, err := src.Collect(context.Background()); err == nil {
		t.Fatal("unparseable sensor value did not report an error")
	}
}

func attrValue(points []schema.Point, name, key, value string) (float64, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		for _, attr := range point.Attrs {
			if attr.Key == key && attr.Value == value {
				return point.Value, true
			}
		}
	}
	return 0, false
}

func TestInventoryPhysicalCount(t *testing.T) {
	cpuinfo := ""
	for processor := 0; processor < 8; processor++ {
		id := processor / 4
		cpuinfo += "processor\t: " + strconv.Itoa(processor) + "\n"
		cpuinfo += "physical id\t: " + strconv.Itoa(id) + "\n"
		cpuinfo += "cpu cores\t: 4\n\n"
	}
	stat := "cpu 1 1 1 1 1 1 1 1 0 0\n"
	for cpu := 0; cpu < 8; cpu++ {
		stat += "cpu" + strconv.Itoa(cpu) + " 1 1 1 1 1 1 1 1 0 0\n"
	}
	root := fstest.MapFS{
		"proc/stat":    {Data: []byte(stat)},
		"proc/meminfo": {Data: []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")},
		"proc/cpuinfo": {Data: []byte(cpuinfo)},
	}
	src := findSource(t, linux.NewLive(root, linux.Options{}), "linux-inventory")
	points, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}
	value, ok := metricValue(points, schema.SystemCPUPhysicalCount)
	if !ok {
		t.Fatalf("missing %s", schema.SystemCPUPhysicalCount)
	}
	if value != 8 {
		t.Fatalf("%s = %v, want 8", schema.SystemCPUPhysicalCount, value)
	}
}

func TestSystemUtilizationFormulas(t *testing.T) {
	root := fstest.MapFS{
		"proc/stat":            {Data: []byte("cpu 100 0 0 1000 0 0 0 0 0 0\ncpu0 50 0 0 500 0 0 0 0 0 0\ncpu1 50 0 0 500 0 0 0 0 0 0\nprocesses 10\n")},
		"proc/meminfo":         {Data: []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")},
		"proc/pressure/memory": {Data: []byte("some avg10=25.00 avg60=0.00 avg300=0.00 total=5000000\n")},
	}
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	src := findSource(t, linux.NewLive(root, linux.Options{Now: func() time.Time { return at }}), "linux-system")
	if _, err := src.Collect(context.Background()); err != nil {
		t.Fatalf("first collect error: %v", err)
	}
	root["proc/stat"] = &fstest.MapFile{Data: []byte("cpu 200 0 0 1000 0 0 0 0 0 0\ncpu0 100 0 0 500 0 0 0 0 0 0\ncpu1 100 0 0 500 0 0 0 0 0 0\nprocesses 10\n")}
	at = at.Add(10 * time.Second)
	points, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("second collect error: %v", err)
	}
	value, ok := attrValue(points, schema.SystemCPUUtilization, schema.AttrCPUMode, schema.CPUModeUser)
	if !ok {
		t.Fatalf("missing %s{%s=user}", schema.SystemCPUUtilization, schema.AttrCPUMode)
	}
	if math.Abs(value-0.05) > 1e-9 {
		t.Errorf("%s{%s=user} = %v, want 0.05", schema.SystemCPUUtilization, schema.AttrCPUMode, value)
	}
	value, ok = attrValue(points, schema.SystemPressureUtilization, schema.AttrSystemPressureType, schema.SystemPressureTypeSome)
	if !ok || math.Abs(value-0.25) > 1e-9 {
		t.Errorf("%s{%s=some} = %v, want 0.25", schema.SystemPressureUtilization, schema.AttrSystemPressureType, value)
	}
	value, ok = attrValue(points, schema.SystemPressureStallTime, schema.AttrSystemPressureType, schema.SystemPressureTypeSome)
	if !ok || math.Abs(value-5) > 1e-9 {
		t.Errorf("%s{%s=some} = %v, want 5", schema.SystemPressureStallTime, schema.AttrSystemPressureType, value)
	}
}
