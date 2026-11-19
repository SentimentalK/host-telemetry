//go:build !windows

package main

import "errors"

func isWindowsService() bool {
	return false
}

func runWindowsService(opts serveOptions) error {
	return errors.New("windows service is unsupported on this platform")
}
