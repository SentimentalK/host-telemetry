//go:build windows

package windows

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	pdhMoreData        = 0x800007D2
	driveFixed         = 3
	fileReadOnlyVolume = 0x00080000
	fileSystemNameSize = 261
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	pdh      = windows.NewLazySystemDLL("pdh.dll")

	getSystemTimes         = kernel32.NewProc("GetSystemTimes")
	globalMemoryStatusEx   = kernel32.NewProc("GlobalMemoryStatusEx")
	getTickCount64         = kernel32.NewProc("GetTickCount64")
	getLogicalDriveStrings = kernel32.NewProc("GetLogicalDriveStringsW")
	getDriveType           = kernel32.NewProc("GetDriveTypeW")
	getVolumeInformation   = kernel32.NewProc("GetVolumeInformationW")
	getDiskFreeSpaceEx     = kernel32.NewProc("GetDiskFreeSpaceExW")

	pdhOpenQueryW          = pdh.NewProc("PdhOpenQueryW")
	pdhExpandWildCardPathW = pdh.NewProc("PdhExpandWildCardPathW")
	pdhAddEnglishCounterW  = pdh.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData    = pdh.NewProc("PdhCollectQueryData")
	pdhGetRawCounterValue  = pdh.NewProc("PdhGetRawCounterValue")
	pdhCloseQuery          = pdh.NewProc("PdhCloseQuery")
)

func filetimeTicks(value windows.Filetime) float64 {
	return float64(uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime))
}

func systemTimes() (cpuTimes, error) {
	var idle, kernel, user windows.Filetime
	if result, _, _ := getSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	); result == 0 {
		return cpuTimes{}, fmt.Errorf("windows: GetSystemTimes failed")
	}
	return cpuTimes{idle: filetimeTicks(idle), kernel: filetimeTicks(kernel), user: filetimeTicks(user)}, nil
}

type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func memoryStatus() (memoryCounters, error) {
	status := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if result, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); result == 0 {
		return memoryCounters{}, fmt.Errorf("windows: GlobalMemoryStatusEx failed")
	}
	return memoryCounters{
		totalPhys:     float64(status.totalPhys),
		availPhys:     float64(status.availPhys),
		totalPagefile: float64(status.totalPageFile),
		availPagefile: float64(status.availPageFile),
	}, nil
}

func tickCountMilliseconds() (float64, error) {
	milliseconds, _, _ := getTickCount64.Call()
	return float64(milliseconds), nil
}

type pdhRawCounter struct {
	status      uint32
	timestamp   windows.Filetime
	firstValue  int64
	secondValue int64
	multiCount  uint32
}

type pdhHandle struct {
	counter     uintptr
	path        string
	counterType uint32
}

type nativeQuery struct {
	query   uintptr
	handles []pdhHandle
}

func (q *nativeQuery) close() {
	if q.query != 0 {
		pdhCloseQuery.Call(q.query)
		q.query = 0
	}
}

func (q *nativeQuery) collect() ([]pdhRaw, error) {
	if status, _, _ := pdhCollectQueryData.Call(q.query); status != 0 {
		return nil, fmt.Errorf("windows: PdhCollectQueryData: %#x", status)
	}
	raws := make([]pdhRaw, 0, len(q.handles))
	for _, handle := range q.handles {
		var value pdhRawCounter
		if status, _, _ := pdhGetRawCounterValue.Call(handle.counter, 0, uintptr(unsafe.Pointer(&value))); status != 0 {
			return nil, fmt.Errorf("windows: PdhGetRawCounterValue %s: %#x", handle.path, status)
		}
		if value.status != 0 && value.status != 1 {
			return nil, fmt.Errorf("windows: %s: counter status %d", handle.path, value.status)
		}
		raws = append(raws, pdhRaw{path: handle.path, counterType: handle.counterType, first: value.firstValue})
	}
	return raws, nil
}

func pdhOpen(paths []string) (pdhQuery, error) {
	var query uintptr
	if status, _, _ := pdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&query))); status != 0 {
		return nil, fmt.Errorf("windows: PdhOpenQueryW: %#x", status)
	}
	opened := &nativeQuery{query: query}
	for _, template := range paths {
		class, _, name, err := counterPathParts(template)
		if err != nil {
			opened.close()
			return nil, err
		}
		spec, ok := counterSpecs[`\`+class+`\`+name]
		if !ok {
			opened.close()
			return nil, fmt.Errorf("windows: unknown counter %s", template)
		}
		expanded, err := expandWildcardPath(template)
		if err != nil {
			opened.close()
			return nil, err
		}
		for _, path := range expanded {
			counterPath, err := windows.UTF16PtrFromString(path)
			if err != nil {
				opened.close()
				return nil, err
			}
			var counter uintptr
			if status, _, _ := pdhAddEnglishCounterW.Call(query, uintptr(unsafe.Pointer(counterPath)), 0, uintptr(unsafe.Pointer(&counter))); status != 0 {
				opened.close()
				return nil, fmt.Errorf("windows: PdhAddEnglishCounterW %s: %#x", path, status)
			}
			opened.handles = append(opened.handles, pdhHandle{counter: counter, path: path, counterType: spec.counterType})
		}
	}
	return opened, nil
}

func expandWildcardPath(template string) ([]string, error) {
	wildcard, err := windows.UTF16PtrFromString(template)
	if err != nil {
		return nil, err
	}
	var size uint32
	status, _, _ := pdhExpandWildCardPathW.Call(0, uintptr(unsafe.Pointer(wildcard)), 0, uintptr(unsafe.Pointer(&size)))
	if status == 0 {
		return nil, nil
	}
	if uint32(status) != pdhMoreData {
		return nil, fmt.Errorf("windows: PdhExpandWildCardPathW %s: %#x", template, status)
	}
	if size == 0 {
		return nil, nil
	}
	buffer := make([]uint16, size)
	status, _, _ = pdhExpandWildCardPathW.Call(0, uintptr(unsafe.Pointer(wildcard)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return nil, fmt.Errorf("windows: PdhExpandWildCardPathW %s: %#x", template, status)
	}
	var paths []string
	for offset := 0; offset < len(buffer) && buffer[offset] != 0; {
		end := offset
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		paths = append(paths, windows.UTF16ToString(buffer[offset:end]))
		offset = end + 1
	}
	return paths, nil
}

func listVolumes() ([]volumeInfo, error) {
	length, _, _ := getLogicalDriveStrings.Call(0, 0)
	if length == 0 {
		return nil, fmt.Errorf("windows: GetLogicalDriveStringsW failed")
	}
	buffer := make([]uint16, length)
	if result, _, _ := getLogicalDriveStrings.Call(uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0]))); result == 0 {
		return nil, fmt.Errorf("windows: GetLogicalDriveStringsW failed")
	}
	var volumes []volumeInfo
	for offset := 0; offset < len(buffer) && buffer[offset] != 0; {
		end := offset
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		root := windows.UTF16ToString(buffer[offset:end])
		offset = end + 1
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return nil, err
		}
		if driveType, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(rootPtr))); uint32(driveType) != driveFixed {
			continue
		}
		volume, err := volumeInfoFor(root, rootPtr)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

func volumeInfoFor(root string, rootPtr *uint16) (volumeInfo, error) {
	fileSystem := make([]uint16, fileSystemNameSize)
	var serial, maximumComponent, flags uint32
	if result, _, _ := getVolumeInformation.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		0,
		0,
		uintptr(unsafe.Pointer(&serial)),
		uintptr(unsafe.Pointer(&maximumComponent)),
		uintptr(unsafe.Pointer(&flags)),
		uintptr(unsafe.Pointer(&fileSystem[0])),
		uintptr(len(fileSystem)),
	); result == 0 {
		return volumeInfo{}, fmt.Errorf("windows: GetVolumeInformationW %s failed", root)
	}
	var freeToCaller, total, free uint64
	if result, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(rootPtr)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	); result == 0 {
		return volumeInfo{}, fmt.Errorf("windows: GetDiskFreeSpaceExW %s failed", root)
	}
	return volumeInfo{
		drive:    strings.TrimSuffix(root, `\`),
		fsType:   windows.UTF16ToString(fileSystem),
		readonly: flags&fileReadOnlyVolume != 0,
		total:    float64(total),
		free:     float64(free),
	}, nil
}

const cimScript = `Get-CimInstance Win32_Processor | Select-Object Node,NumberOfCores,NumberOfLogicalProcessors | ConvertTo-Csv -NoTypeInformation
"---"
Get-CimInstance Win32_OperatingSystem | Select-Object Node,TotalVisibleMemorySize,MaxNumberOfProcesses,NumberOfProcesses | ConvertTo-Csv -NoTypeInformation`

func cimInventory(ctx context.Context) (cimInfo, error) {
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", cimScript).Output()
	if err != nil {
		return cimInfo{}, fmt.Errorf("windows: powershell inventory: %w", err)
	}
	processor, operatingSystem, err := splitCIMSections(output)
	if err != nil {
		return cimInfo{}, err
	}
	cores, logical, err := parseProcessorCSV(processor, "powershell Win32_Processor")
	if err != nil {
		return cimInfo{}, err
	}
	memory, processes, err := parseOSCSV(operatingSystem, "powershell Win32_OperatingSystem")
	if err != nil {
		return cimInfo{}, err
	}
	return cimInfo{cores: cores, logical: logical, totalVisibleMemory: memory, maxProcesses: processes}, nil
}

func splitCIMSections(data []byte) ([]byte, []byte, error) {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			return []byte(strings.Join(lines[:i], "\n")), []byte(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return nil, nil, fmt.Errorf("windows: inventory output has no --- separator")
}
