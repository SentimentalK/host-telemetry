//go:build !windows

package windows

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("windows: live collection is unsupported on this platform")

func systemTimes() (cpuTimes, error) { return cpuTimes{}, errUnsupported }

func memoryStatus() (memoryCounters, error) { return memoryCounters{}, errUnsupported }

func tickCountMilliseconds() (float64, error) { return 0, errUnsupported }

func pdhOpen(paths []string) (pdhQuery, error) { return nil, errUnsupported }

func listVolumes() ([]volumeInfo, error) { return nil, errUnsupported }

func cimInventory(ctx context.Context) (cimInfo, error) { return cimInfo{}, errUnsupported }
