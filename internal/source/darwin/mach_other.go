//go:build !darwin

package darwin

import "fmt"

func readCPUTicks() (CPUTicks, error) {
	return CPUTicks{}, fmt.Errorf("host_statistics is only available on darwin")
}
