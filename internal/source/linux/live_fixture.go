package linux

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

func FixtureStatfs(root fs.FS, name string) (func(string) (Statfs, error), error) {
	data, err := fs.ReadFile(root, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	table := make(map[string]Statfs)
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("%s: line %d: want 5 tab-separated fields, got %d", name, i+1, len(fields))
		}
		values := make([]uint64, 4)
		for j := range values {
			value, err := strconv.ParseUint(fields[j+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: %q: %w", name, i+1, fields[j+1], err)
			}
			values[j] = value
		}
		table[fields[0]] = Statfs{Blocks: values[0], Bfree: values[1], Bavail: values[2], Bsize: values[3]}
	}
	return func(mountpoint string) (Statfs, error) {
		stats, ok := table[mountpoint]
		if !ok {
			return Statfs{}, fmt.Errorf("%s: unknown mountpoint %q", name, mountpoint)
		}
		return stats, nil
	}, nil
}
