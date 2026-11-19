//go:build darwin

package darwin

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	hostCPULoadInfo = 3
	cpuStateMax     = 4
)

var (
	machOnce      sync.Once
	machHostSelf  uintptr
	machStats     uintptr
	machSymbolErr error
)

func loadMachSymbols() (uintptr, uintptr, error) {
	machOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		if err != nil {
			machSymbolErr = fmt.Errorf("dlopen libSystem: %w", err)
			return
		}
		hostSelf, err := purego.Dlsym(lib, "mach_host_self")
		if err != nil {
			machSymbolErr = fmt.Errorf("dlsym mach_host_self: %w", err)
			return
		}
		stats, err := purego.Dlsym(lib, "host_statistics")
		if err != nil {
			machSymbolErr = fmt.Errorf("dlsym host_statistics: %w", err)
			return
		}
		machHostSelf = hostSelf
		machStats = stats
	})
	return machHostSelf, machStats, machSymbolErr
}

func readCPUTicks() (CPUTicks, error) {
	hostSelf, stats, err := loadMachSymbols()
	if err != nil {
		return CPUTicks{}, err
	}
	host, _, _ := purego.SyscallN(hostSelf)
	if uint32(host) == 0 {
		return CPUTicks{}, fmt.Errorf("mach_host_self returned 0")
	}
	ticks := [cpuStateMax]uint32{}
	count := uint32(cpuStateMax)
	ret, _, _ := purego.SyscallN(
		stats,
		uintptr(uint32(host)),
		uintptr(hostCPULoadInfo),
		uintptr(unsafe.Pointer(&ticks[0])),
		uintptr(unsafe.Pointer(&count)),
	)
	if int32(ret) != 0 {
		return CPUTicks{}, fmt.Errorf("host_statistics: kern_return_t %d", int32(ret))
	}
	if count < cpuStateMax {
		return CPUTicks{}, fmt.Errorf("host_statistics: count %d, want at least %d", count, cpuStateMax)
	}
	return CPUTicks{User: float64(ticks[0]), System: float64(ticks[1]), Idle: float64(ticks[2])}, nil
}
