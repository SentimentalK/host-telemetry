package hostinfo

import (
	"errors"
	"strings"
)

func normalizeMachineGUID(value string) (string, error) {
	guid := strings.TrimSpace(value)
	if guid == "" {
		return "", errors.New("hostinfo: machine guid is empty")
	}
	return guid, nil
}
