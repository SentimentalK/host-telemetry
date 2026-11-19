package process_test

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/workload/process"
)

const fixtureProc = "../../../testdata/linux/workload"

func TestMatchCollectsTree(t *testing.T) {
	trees, err := process.Match(os.DirFS(fixtureProc), []string{"agent"}, nil, &process.Tracker{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want one", trees)
	}
	tree := trees[0]
	if tree.Exe != "agent" || tree.RootPID != 100 {
		t.Errorf("tree = %+v, want agent#100", tree)
	}
	if len(tree.PIDs) != 2 || tree.PIDs[0] != 100 || tree.PIDs[1] != 101 {
		t.Errorf("pids = %v, want [100 101]", tree.PIDs)
	}
	if want := uint64((10240 + 4096) * 1024); tree.RSSBytes != want {
		t.Errorf("rss = %d, want %d", tree.RSSBytes, want)
	}
	if tree.UserSeconds != 0.7 || tree.SystemSeconds != 0.15 {
		t.Errorf("cpu = %v/%v, want 0.7/0.15", tree.UserSeconds, tree.SystemSeconds)
	}
}

func TestMatchExcludesRoot(t *testing.T) {
	trees, err := process.Match(os.DirFS(fixtureProc), []string{"agent"}, map[int]bool{100: true}, &process.Tracker{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 0 {
		t.Fatalf("trees = %v, want none", trees)
	}
}

func TestMatchExcludesSubtree(t *testing.T) {
	trees, err := process.Match(os.DirFS(fixtureProc), []string{"agent"}, map[int]bool{101: true}, &process.Tracker{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want one", trees)
	}
	tree := trees[0]
	if len(tree.PIDs) != 1 || tree.PIDs[0] != 100 {
		t.Errorf("pids = %v, want [100]", tree.PIDs)
	}
	if want := uint64(10240 * 1024); tree.RSSBytes != want {
		t.Errorf("rss = %d, want %d", tree.RSSBytes, want)
	}
	if tree.UserSeconds != 0.5 || tree.SystemSeconds != 0.1 {
		t.Errorf("cpu = %v/%v, want 0.5/0.1", tree.UserSeconds, tree.SystemSeconds)
	}
}

func TestMatchEmptyWhitelistDoesNotReadProc(t *testing.T) {
	trees, err := process.Match(fstest.MapFS{}, nil, nil, &process.Tracker{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(trees) != 0 {
		t.Fatalf("trees = %v, want none", trees)
	}
}

func TestTrackerDoesNotDoubleCount(t *testing.T) {
	tracker := &process.Tracker{}
	first, err := process.Match(os.DirFS(fixtureProc), []string{"agent"}, nil, tracker)
	if err != nil {
		t.Fatal(err)
	}
	second, err := process.Match(os.DirFS(fixtureProc), []string{"agent"}, nil, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].UserSeconds != 0.7 || second[0].UserSeconds != 0.7 {
		t.Errorf("user seconds = %v then %v, want 0.7 both", first[0].UserSeconds, second[0].UserSeconds)
	}
	if first[0].SystemSeconds != 0.15 || second[0].SystemSeconds != 0.15 {
		t.Errorf("system seconds = %v then %v, want 0.15 both", first[0].SystemSeconds, second[0].SystemSeconds)
	}
}

func TestTrackerKeepsChildContributionAfterExit(t *testing.T) {
	root := fstest.MapFS{
		"proc/100/stat":   {Data: []byte("100 (agent) S 1 100 100 0 -1 0 0 0 0 0 50 10 0 0 20 0 1 0 100 0 0\n")},
		"proc/100/comm":   {Data: []byte("agent\n")},
		"proc/100/status": {Data: []byte("Name:\tagent\nVmRSS:\t  1024 kB\n")},
		"proc/101/stat":   {Data: []byte("101 (child) S 100 100 100 0 -1 0 0 0 0 0 20 5 0 0 20 0 1 0 100 0 0\n")},
		"proc/101/comm":   {Data: []byte("child\n")},
		"proc/101/status": {Data: []byte("Name:\tchild\nVmRSS:\t  1024 kB\n")},
	}
	tracker := &process.Tracker{}
	if _, err := process.Match(root, []string{"agent"}, nil, tracker); err != nil {
		t.Fatal(err)
	}
	delete(root, "proc/101/stat")
	delete(root, "proc/101/comm")
	delete(root, "proc/101/status")
	trees, err := process.Match(root, []string{"agent"}, nil, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want one", trees)
	}
	if trees[0].UserSeconds != 0.7 || trees[0].SystemSeconds != 0.15 {
		t.Errorf("cpu = %v/%v, want 0.7/0.15 after child exit", trees[0].UserSeconds, trees[0].SystemSeconds)
	}
}

func TestNestedWhitelistSingleTree(t *testing.T) {
	trees, err := process.Match(os.DirFS(fixtureProc), []string{"agent", "child"}, nil, &process.Tracker{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want only agent#100", trees)
	}
	tree := trees[0]
	if tree.Exe != "agent" || tree.RootPID != 100 {
		t.Errorf("tree = %+v, want agent#100", tree)
	}
	if want := uint64((10240 + 4096) * 1024); tree.RSSBytes != want {
		t.Errorf("rss = %d, want %d counted once", tree.RSSBytes, want)
	}
	if tree.UserSeconds != 0.7 || tree.SystemSeconds != 0.15 {
		t.Errorf("cpu = %v/%v, want 0.7/0.15 covering parent and child", tree.UserSeconds, tree.SystemSeconds)
	}
}

func TestStatmRSS(t *testing.T) {
	root := fstest.MapFS{
		"proc/300/stat":   {Data: []byte("300 (agent) S 1 300 300 0 -1 0 0 0 0 0 10 2 0 0 20 0 1 0 100 0 0\n")},
		"proc/300/comm":   {Data: []byte("agent\n")},
		"proc/300/statm":  {Data: []byte("1000 7 3 0 0 0 0\n")},
		"proc/300/status": {Data: []byte("Name:\tagent\nVmRSS:\t  99999 kB\n")},
	}
	trees, err := process.Match(root, []string{"agent"}, nil, &process.Tracker{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want one", trees)
	}
	if want := uint64(7) * uint64(os.Getpagesize()); trees[0].RSSBytes != want {
		t.Errorf("rss = %d, want statm resident pages %d", trees[0].RSSBytes, want)
	}
}
