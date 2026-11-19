package linux

import (
	"context"
	"io/fs"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type filesystemSource struct {
	root   fs.FS
	statfs func(mountpoint string) (Statfs, error)
}

func (s *filesystemSource) Name() string { return "linux-filesystem" }

func (s *filesystemSource) Interval() time.Duration { return interval }

func (s *filesystemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	data, err := fs.ReadFile(s.root, "proc/self/mountinfo")
	if err != nil {
		return nil, nil
	}
	var points []schema.Point
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || separator+2 >= len(fields) {
			continue
		}
		fstype, ok := filesystemType(fields[separator+1])
		if !ok {
			continue
		}
		mode := strings.Split(fields[5], ",")[0]
		if mode != "rw" && mode != "ro" {
			continue
		}
		mountpoint := fields[4]
		device := fields[separator+2]
		stats, err := s.statfs(mountpoint)
		if err != nil {
			continue
		}
		if stats.Bsize == 0 || stats.Blocks == 0 {
			continue
		}
		total := stats.Blocks * stats.Bsize
		freeBytes := stats.Bfree * stats.Bsize
		if freeBytes > total {
			continue
		}
		availBytes := stats.Bavail * stats.Bsize
		reserved := uint64(0)
		if freeBytes > availBytes {
			reserved = freeBytes - availBytes
		}
		base := []schema.Attr{
			{Key: schema.AttrSystemFilesystemType, Value: fstype},
			{Key: schema.AttrSystemFilesystemMode, Value: mode},
			{Key: schema.AttrSystemFilesystemMountpoint, Value: mountpoint},
		}
		if device != "" {
			base = append(base, schema.Attr{Key: schema.AttrSystemDeviceName, Value: device})
		}
		for _, state := range []struct {
			name  string
			value uint64
		}{
			{schema.SystemFilesystemStateUsed, total - freeBytes},
			{schema.SystemFilesystemStateFree, availBytes},
			{schema.SystemFilesystemStateReserved, reserved},
		} {
			attrs := append(append([]schema.Attr(nil), base...), schema.Attr{Key: schema.AttrSystemFilesystemState, Value: state.name})
			points = append(points,
				schema.NewPoint(schema.SystemFilesystemUsage, float64(state.value), attrs...),
				schema.NewPoint(schema.SystemFilesystemUtilization, float64(state.value)/float64(total), attrs...),
			)
		}
	}
	return points, nil
}

func filesystemType(fstype string) (string, bool) {
	switch fstype {
	case "ext4":
		return schema.SystemFilesystemTypeExt4, true
	case "vfat":
		return schema.SystemFilesystemTypeFat32, true
	case "exfat":
		return schema.SystemFilesystemTypeExfat, true
	case "ntfs", "ntfs3":
		return schema.SystemFilesystemTypeNtfs, true
	case "hfsplus":
		return schema.SystemFilesystemTypeHfsplus, true
	case "refs":
		return schema.SystemFilesystemTypeRefs, true
	default:
		return "", false
	}
}

var _ source.Source = (*filesystemSource)(nil)
