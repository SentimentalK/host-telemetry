//go:build !windows

package hostinfo

import "errors"

func MachineGUID() (string, error) {
	return "", errors.New("hostinfo: machine guid is only available on windows")
}
