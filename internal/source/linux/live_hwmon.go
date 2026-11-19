package linux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const (
	hwmonWait = 4 * time.Millisecond

	hwKindName = iota
	hwKindTemperature
	hwKindFan
	hwKindPower
	hwKindFrequency
)

type hwmonTask struct {
	path   string
	dir    string
	sensor string
	number string
	kind   int
}

type hwmonSource struct {
	root fs.FS

	mu       sync.Mutex
	snapshot map[string]schema.Point
	err      error
	reading  bool
	done     chan struct{}
}

func (s *hwmonSource) Name() string { return "linux-hwmon" }

func (s *hwmonSource) Interval() time.Duration { return 30 * time.Second }

func (s *hwmonSource) Collect(ctx context.Context) ([]schema.Point, error) {
	s.mu.Lock()
	if !s.reading {
		s.reading = true
		done := make(chan struct{})
		s.done = done
		go s.refresh(done)
	}
	done := s.done
	s.mu.Unlock()

	timer := time.NewTimer(hwmonWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.err
	s.err = nil
	points := make([]schema.Point, 0, len(s.snapshot))
	for _, point := range s.snapshot {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return hwmonLess(points[i], points[j]) })
	return points, err
}

func (s *hwmonSource) refresh(done chan struct{}) {
	points, err := s.load()
	s.mu.Lock()
	if s.snapshot == nil || err == nil {
		s.snapshot = make(map[string]schema.Point, len(points))
	}
	for _, point := range points {
		s.snapshot[hwmonSeriesKey(point)] = point
	}
	if err != nil {
		s.err = err
	}
	s.reading = false
	s.mu.Unlock()
	close(done)
}

func hwmonSeriesKey(point schema.Point) string {
	var b strings.Builder
	b.WriteString(point.Name)
	for _, attr := range point.Attrs {
		b.WriteByte(0)
		b.WriteString(attr.Key)
		b.WriteByte('=')
		b.WriteString(attr.Value)
	}
	return b.String()
}

func hwmonLess(a, b schema.Point) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	for i := 0; i < len(a.Attrs) && i < len(b.Attrs); i++ {
		if a.Attrs[i].Key != b.Attrs[i].Key {
			return a.Attrs[i].Key < b.Attrs[i].Key
		}
		if a.Attrs[i].Value != b.Attrs[i].Value {
			return a.Attrs[i].Value < b.Attrs[i].Value
		}
	}
	return len(a.Attrs) < len(b.Attrs)
}

func (s *hwmonSource) load() ([]schema.Point, error) {
	tasks, err := s.tasks()
	if err != nil {
		return nil, err
	}
	raw, failures := s.readAll(tasks)
	names := make(map[string]string)
	var points []schema.Point
	var errs []error
	for i, task := range tasks {
		if failed := failures[i]; failed != nil {
			if task.kind == hwKindName || absentSensor(failed) {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", task.path, failed))
			continue
		}
		data := raw[i]
		if data == nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if task.kind == hwKindName {
			names[task.dir] = text
			continue
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", task.path, err))
			continue
		}
		attrs := []schema.Attr{}
		switch task.kind {
		case hwKindFrequency:
			attrs = append(attrs, schema.Attr{Key: schema.AttrCPULogicalNumber, Value: task.number})
		default:
			attrs = append(attrs, schema.Attr{Key: schema.AttrHWID, Value: task.dir + "/" + task.sensor})
			if name := names[task.dir]; name != "" {
				attrs = append(attrs, schema.Attr{Key: schema.AttrHWName, Value: name})
			}
		}
		switch task.kind {
		case hwKindTemperature:
			points = append(points, schema.NewPoint(schema.HWTemperature, float64(value)/1000, attrs...))
		case hwKindFan:
			points = append(points, schema.NewPoint(schema.HWFanSpeed, float64(value), attrs...))
		case hwKindPower:
			points = append(points, schema.NewPoint(schema.HWPower, float64(value)/1e6, attrs...))
		case hwKindFrequency:
			points = append(points, schema.NewPoint(schema.SystemCPUFrequency, float64(value)*1000, attrs...))
		}
	}
	return points, errors.Join(errs...)
}

func absentSensor(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENODEV)
}

func (s *hwmonSource) tasks() ([]hwmonTask, error) {
	var tasks []hwmonTask
	entries, err := fs.ReadDir(s.root, "sys/class/hwmon")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("sys/class/hwmon: %w", err)
	}
	if err == nil {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "hwmon") {
				continue
			}
			dir := "sys/class/hwmon/" + entry.Name()
			tasks = append(tasks, hwmonTask{path: dir + "/name", dir: entry.Name(), kind: hwKindName})
			files, err := fs.ReadDir(s.root, dir)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			for _, file := range files {
				sensor := strings.TrimSuffix(file.Name(), "_input")
				if sensor == file.Name() {
					continue
				}
				kind := -1
				switch {
				case strings.HasPrefix(sensor, "temp"):
					kind = hwKindTemperature
				case strings.HasPrefix(sensor, "fan"):
					kind = hwKindFan
				case strings.HasPrefix(sensor, "power"):
					kind = hwKindPower
				}
				if kind < 0 {
					continue
				}
				tasks = append(tasks, hwmonTask{path: dir + "/" + file.Name(), dir: entry.Name(), sensor: sensor, kind: kind})
			}
		}
	}
	cpus, err := fs.ReadDir(s.root, "sys/devices/system/cpu")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("sys/devices/system/cpu: %w", err)
	}
	if err == nil {
		for _, entry := range cpus {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "cpu") {
				continue
			}
			number := strings.TrimPrefix(entry.Name(), "cpu")
			if number == "" || strings.Trim(number, "0123456789") != "" {
				continue
			}
			tasks = append(tasks, hwmonTask{
				path:   "sys/devices/system/cpu/" + entry.Name() + "/cpufreq/scaling_cur_freq",
				number: number,
				kind:   hwKindFrequency,
			})
		}
	}
	return tasks, nil
}

func (s *hwmonSource) readAll(tasks []hwmonTask) ([][]byte, []error) {
	raw := make([][]byte, len(tasks))
	failures := make([]error, len(tasks))
	workers := processWorkers
	if len(tasks) < workers {
		workers = len(tasks)
	}
	var next atomic.Int64
	var readers sync.WaitGroup
	for range workers {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(tasks) {
					return
				}
				data, err := fs.ReadFile(s.root, tasks[index].path)
				if err != nil {
					failures[index] = err
					continue
				}
				raw[index] = data
			}
		}()
	}
	readers.Wait()
	return raw, failures
}

var _ source.Source = (*hwmonSource)(nil)
