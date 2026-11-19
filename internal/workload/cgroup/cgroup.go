package cgroup

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type CPU struct {
	User   uint64
	System uint64
	Nice   uint64
	Usage  uint64
}

type Scope struct {
	ID            string
	MemoryCurrent uint64
	CPU           CPU
	Pressure      map[string]float64
	PIDs          []int
}

var pressureResources = []string{"cpu", "memory", "io"}

const (
	readWorkers = 16
	walkWorkers = 16
)

func Discover(root fs.FS) ([]Scope, error) {
	var errs error
	paths, err := walk(root)
	if err != nil {
		errs = errors.Join(errs, err)
	}
	var dirs []string
	for _, p := range paths {
		populated, err := isPopulated(root, p)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		if populated {
			dirs = append(dirs, p)
		}
	}

	scopes := make([]Scope, len(dirs))
	scopeErrs := make([]error, len(dirs))
	workers := readWorkers
	if len(dirs) < workers {
		workers = len(dirs)
	}
	var next atomic.Int64
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(dirs) {
					return
				}
				scopes[index], scopeErrs[index] = Read(root, dirs[index])
			}
		}()
	}
	wait.Wait()

	out := make([]Scope, 0, len(scopes))
	for i := range scopes {
		if scopeErrs[i] != nil {
			errs = errors.Join(errs, scopeErrs[i])
			continue
		}
		out = append(out, scopes[i])
	}
	return out, errs
}

func selected(p string) bool {
	trimmed := strings.TrimPrefix(p, "/")
	name := path.Base(trimmed)
	if strings.Contains("/"+trimmed, "/orca_ptys_") && strings.HasPrefix(name, "orca_pty_") {
		return true
	}
	if path.Base(path.Dir(trimmed)) != "app.slice" {
		return false
	}
	for _, prefix := range []string{"app-", "snap.", "orca-daemon-"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

func isPopulated(root fs.FS, dir string) (bool, error) {
	data, err := fs.ReadFile(root, path.Join(dir, "cgroup.events"))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			return fields[1] == "1", nil
		}
	}
	return false, nil
}

func Read(root fs.FS, dir string) (Scope, error) {
	scope := Scope{ID: strings.TrimSuffix(path.Base(dir), ".scope")}
	var errs error

	memory, err := readUint(root, dir, "memory.current")
	if err != nil {
		errs = errors.Join(errs, err)
	}
	scope.MemoryCurrent = memory

	cpu, err := readCPU(root, dir)
	if err != nil {
		errs = errors.Join(errs, err)
	}
	scope.CPU = cpu

	for _, resource := range pressureResources {
		value, ok, err := readPressure(root, dir, resource)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if scope.Pressure == nil {
			scope.Pressure = make(map[string]float64, len(pressureResources))
		}
		scope.Pressure[resource] = value
	}

	pids, err := readPIDs(root, dir)
	if err != nil {
		errs = errors.Join(errs, err)
	}
	scope.PIDs = pids

	return scope, errs
}

func readUint(root fs.FS, dir, name string) (uint64, error) {
	data, err := fs.ReadFile(root, path.Join(dir, name))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return value, nil
}

func readCPU(root fs.FS, dir string) (CPU, error) {
	data, err := fs.ReadFile(root, path.Join(dir, "cpu.stat"))
	if err != nil {
		return CPU{}, err
	}
	var cpu CPU
	var seen uint8
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return CPU{}, fmt.Errorf("cpu.stat %s: %w", fields[0], err)
		}
		switch fields[0] {
		case "user_usec":
			cpu.User = value
			seen |= 1
		case "system_usec":
			cpu.System = value
			seen |= 2
		case "nice_usec":
			cpu.Nice = value
			seen |= 4
		case "usage_usec":
			cpu.Usage = value
			seen |= 8
		}
	}
	if seen != 0xF {
		return CPU{}, fmt.Errorf("cpu.stat: missing required fields")
	}
	return cpu, nil
}

func readPressure(root fs.FS, dir, resource string) (float64, bool, error) {
	name := resource + ".pressure"
	data, err := fs.ReadFile(root, path.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, field := range fields[1:] {
			value, ok := strings.CutPrefix(field, "avg10=")
			if !ok {
				continue
			}
			fraction, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return 0, false, fmt.Errorf("%s: %w", name, err)
			}
			return fraction / 100, true, nil
		}
		return 0, false, fmt.Errorf("%s: missing some avg10", name)
	}
	return 0, false, fmt.Errorf("%s: missing some line", name)
}

func readPIDs(root fs.FS, dir string) ([]int, error) {
	data, err := fs.ReadFile(root, path.Join(dir, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			return nil, fmt.Errorf("cgroup.procs: %w", err)
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func walk(root fs.FS) ([]string, error) {
	queue := make(chan string, 256)
	queue <- "."
	var mu sync.Mutex
	var paths []string
	var errs error
	var active sync.WaitGroup
	active.Add(1)
	var workers sync.WaitGroup
	for range walkWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for dir := range queue {
				entries, err := fs.ReadDir(root, dir)
				if err != nil {
					mu.Lock()
					errs = errors.Join(errs, fmt.Errorf("%s: %w", dir, err))
					mu.Unlock()
					active.Done()
					continue
				}
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					child := path.Join(dir, entry.Name())
					if strings.HasSuffix(entry.Name(), ".scope") && selected(child) {
						mu.Lock()
						paths = append(paths, child)
						mu.Unlock()
					}
					active.Add(1)
					queue <- child
				}
				active.Done()
			}
		}()
	}
	active.Wait()
	close(queue)
	workers.Wait()
	sort.Strings(paths)
	return paths, errs
}
