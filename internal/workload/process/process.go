package process

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	clockTicks = 100
	workers    = 8
	rssWorkers = 16
)

var pagesize = uint64(os.Getpagesize())

type Tree struct {
	Exe           string
	RootPID       int
	PIDs          []int
	RSSBytes      uint64
	UserSeconds   float64
	SystemSeconds float64
}

type Tracker struct {
	ticks map[int]ticks
	roots map[int]ticks
}

type ticks struct {
	user   uint64
	system uint64
}

type procInfo struct {
	ppid   int
	user   uint64
	system uint64
	comm   string
}

type Snapshot struct {
	root     fs.FS
	procs    map[int]procInfo
	children map[int][]int
	pids     []int
}

func Scan(proc fs.FS) (*Snapshot, error) {
	procs, err := scan(proc)
	if err != nil {
		return nil, err
	}
	snapshot := &Snapshot{
		root:     proc,
		procs:    procs,
		children: make(map[int][]int, len(procs)),
		pids:     make([]int, 0, len(procs)),
	}
	for pid, info := range procs {
		snapshot.children[info.ppid] = append(snapshot.children[info.ppid], pid)
		snapshot.pids = append(snapshot.pids, pid)
	}
	sort.Ints(snapshot.pids)
	for pid := range snapshot.children {
		sort.Ints(snapshot.children[pid])
	}
	return snapshot, nil
}

func Match(proc fs.FS, executables []string, exclude map[int]bool, tracker *Tracker) ([]Tree, error) {
	if len(executables) == 0 {
		return nil, nil
	}
	snapshot, err := Scan(proc)
	if err != nil {
		return nil, err
	}
	return snapshot.Match(executables, exclude, tracker)
}

func (snapshot *Snapshot) Match(executables []string, exclude map[int]bool, tracker *Tracker) ([]Tree, error) {
	if len(executables) == 0 {
		return nil, nil
	}
	procs := snapshot.procs
	children := snapshot.children
	pids := snapshot.pids
	whitelist := make(map[string]bool, len(executables))
	for _, executable := range executables {
		whitelist[path.Base(executable)] = true
	}

	if tracker == nil {
		tracker = &Tracker{}
	}
	if tracker.ticks == nil {
		tracker.ticks = make(map[int]ticks)
	}
	if tracker.roots == nil {
		tracker.roots = make(map[int]ticks)
	}

	current := make(map[int]bool)
	for _, pid := range pids {
		if !whitelist[procs[pid].comm] || exclude[pid] {
			continue
		}
		if hasWhitelistedAncestor(pid, procs, whitelist) {
			continue
		}
		current[pid] = true
	}
	for pid := range tracker.roots {
		if !current[pid] {
			delete(tracker.roots, pid)
		}
	}

	trees := make([]Tree, 0, len(current))
	for _, pid := range pids {
		if !current[pid] {
			continue
		}
		treePids := descendants(pid, children, exclude)
		rss, readable := readRSS(snapshot.root, treePids)

		totals := tracker.roots[pid]
		for _, treePid := range treePids {
			info := procs[treePid]
			previous := tracker.ticks[treePid]
			delta := ticks{}
			if info.user >= previous.user {
				delta.user = info.user - previous.user
			} else {
				delta.user = info.user
			}
			if info.system >= previous.system {
				delta.system = info.system - previous.system
			} else {
				delta.system = info.system
			}
			tracker.ticks[treePid] = ticks{user: info.user, system: info.system}
			if !readable[treePid] {
				continue
			}
			totals.user += delta.user
			totals.system += delta.system
		}
		tracker.roots[pid] = totals

		kept := make([]int, 0, len(treePids))
		for _, treePid := range treePids {
			if readable[treePid] {
				kept = append(kept, treePid)
			}
		}
		trees = append(trees, Tree{
			Exe:           procs[pid].comm,
			RootPID:       pid,
			PIDs:          kept,
			RSSBytes:      rss,
			UserSeconds:   float64(totals.user) / clockTicks,
			SystemSeconds: float64(totals.system) / clockTicks,
		})
	}

	for pid := range tracker.ticks {
		if _, ok := procs[pid]; !ok {
			delete(tracker.ticks, pid)
		}
	}
	return trees, nil
}

func scan(proc fs.FS) (map[int]procInfo, error) {
	entries, err := fs.ReadDir(proc, "proc")
	if err != nil {
		return nil, fmt.Errorf("proc: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	infos := make([]procInfo, len(names))
	valid := make([]bool, len(names))
	workers := workers
	if len(names) < workers {
		workers = len(names)
	}
	var next atomic.Int64
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(names) {
					return
				}
				info, ok := readProc(proc, names[index])
				infos[index] = info
				valid[index] = ok
			}
		}()
	}
	wait.Wait()

	procs := make(map[int]procInfo, len(names))
	for i, name := range names {
		if !valid[i] {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		procs[pid] = infos[i]
	}
	return procs, nil
}

func readProc(proc fs.FS, name string) (procInfo, bool) {
	stat, err := fs.ReadFile(proc, "proc/"+name+"/stat")
	if err != nil {
		return procInfo{}, false
	}
	text := string(stat)
	start := strings.Index(text, "(")
	end := strings.LastIndex(text, ")")
	if start < 0 || end <= start {
		return procInfo{}, false
	}
	ppid, user, system, ok := parseStat(text[end+1:])
	if !ok {
		return procInfo{}, false
	}
	return procInfo{
		ppid:   ppid,
		user:   user,
		system: system,
		comm:   strings.Clone(text[start+1 : end]),
	}, true
}

func parseStat(fields string) (int, uint64, uint64, bool) {
	var ppid int
	var user, system uint64
	index := 0
	start := -1
	for i := 0; i <= len(fields); i++ {
		if i < len(fields) && fields[i] != ' ' && fields[i] != '\t' && fields[i] != '\n' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start < 0 {
			continue
		}
		field := fields[start:i]
		start = -1
		var err error
		switch index {
		case 1:
			ppid, err = strconv.Atoi(field)
		case 11:
			user, err = strconv.ParseUint(field, 10, 64)
		case 12:
			system, err = strconv.ParseUint(field, 10, 64)
		}
		if err != nil {
			return 0, 0, 0, false
		}
		index++
		if index > 12 {
			return ppid, user, system, true
		}
	}
	return 0, 0, 0, false
}

func descendants(root int, children map[int][]int, exclude map[int]bool) []int {
	var pids []int
	var walk func(pid int)
	walk = func(pid int) {
		if exclude[pid] {
			return
		}
		pids = append(pids, pid)
		for _, child := range children[pid] {
			walk(child)
		}
	}
	walk(root)
	return pids
}

func hasWhitelistedAncestor(pid int, procs map[int]procInfo, whitelist map[string]bool) bool {
	seen := map[int]bool{pid: true}
	for {
		info, ok := procs[pid]
		if !ok {
			return false
		}
		parent := info.ppid
		if parent <= 0 || seen[parent] {
			return false
		}
		seen[parent] = true
		ancestor, ok := procs[parent]
		if !ok {
			return false
		}
		if whitelist[ancestor.comm] {
			return true
		}
		pid = parent
	}
}

func readRSS(proc fs.FS, pids []int) (uint64, map[int]bool) {
	sizes := make([]uint64, len(pids))
	readable := make([]bool, len(pids))
	workers := rssWorkers
	if len(pids) < workers {
		workers = len(pids)
	}
	var next atomic.Int64
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(pids) {
					return
				}
				sizes[index], readable[index] = pidRSS(proc, pids[index])
			}
		}()
	}
	wait.Wait()

	out := make(map[int]bool, len(pids))
	var total uint64
	for index, pid := range pids {
		if !readable[index] {
			continue
		}
		out[pid] = true
		total += sizes[index]
	}
	return total, out
}

func pidRSS(proc fs.FS, pid int) (uint64, bool) {
	statm, err := fs.ReadFile(proc, fmt.Sprintf("proc/%d/statm", pid))
	if err == nil {
		fields := strings.Fields(string(statm))
		if len(fields) >= 2 {
			pages, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return pages * pagesize, true
			}
		}
	}
	status, err := fs.ReadFile(proc, fmt.Sprintf("proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	return vmRSS(string(status))
}

func vmRSS(status string) (uint64, bool) {
	for _, line := range strings.Split(status, "\n") {
		value, ok := strings.CutPrefix(line, "VmRSS:")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0, false
		}
		kB, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kB * 1024, true
	}
	return 0, false
}
