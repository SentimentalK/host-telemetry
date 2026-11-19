//go:build windows

package hostinfo

import "golang.org/x/sys/windows/registry"

func MachineGUID() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	return normalizeMachineGUID(value)
}
