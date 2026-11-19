//go:build !windows

package install

import (
	"errors"

	"github.com/SentimentalK/host-telemetry/deploy/windows"
)

func applyService(spec winunit.Spec) error {
	return errors.New("install: service actions require windows")
}
