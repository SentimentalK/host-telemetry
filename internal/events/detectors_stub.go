//go:build !linux

package events

func LinuxDetectors(_ DetectorOptions) []Detector {
	return nil
}
