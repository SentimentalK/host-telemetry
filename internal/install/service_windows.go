//go:build windows

package install

import (
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/SentimentalK/host-telemetry/deploy/windows"
)

func applyService(spec winunit.Spec) error {
	mgr, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CREATE_SERVICE|windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(mgr)

	name, err := windows.UTF16PtrFromString(spec.Name)
	if err != nil {
		return err
	}
	display, err := windows.UTF16PtrFromString(spec.DisplayName)
	if err != nil {
		return err
	}
	commandLine, err := windows.UTF16PtrFromString(winunit.CommandLine(spec))
	if err != nil {
		return err
	}
	dependencies := serviceDependencies(spec.Dependencies)
	startType := serviceStartType(spec.StartType)

	service, err := windows.CreateService(mgr, name, display, windows.SERVICE_ALL_ACCESS, windows.SERVICE_WIN32_OWN_PROCESS, startType, windows.SERVICE_ERROR_NORMAL, commandLine, nil, nil, dependencies, nil, nil)
	if err == windows.ERROR_SERVICE_EXISTS {
		service, err = windows.OpenService(mgr, name, windows.SERVICE_ALL_ACCESS)
		if err != nil {
			return err
		}
		if err := windows.ChangeServiceConfig(service, windows.SERVICE_NO_CHANGE, startType, windows.SERVICE_NO_CHANGE, commandLine, nil, nil, dependencies, nil, nil, display); err != nil {
			windows.CloseServiceHandle(service)
			return err
		}
	} else if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(service)

	description, err := windows.UTF16PtrFromString(spec.Description)
	if err != nil {
		return err
	}
	descriptionInfo := windows.SERVICE_DESCRIPTION{Description: description}
	if err := windows.ChangeServiceConfig2(service, windows.SERVICE_CONFIG_DESCRIPTION, (*byte)(unsafe.Pointer(&descriptionInfo))); err != nil {
		return err
	}

	actions := make([]windows.SC_ACTION, 0, len(spec.FailureActions))
	for _, action := range spec.FailureActions {
		kind := uint32(windows.SC_ACTION_NONE)
		if action.Restart {
			kind = windows.SC_ACTION_RESTART
		}
		actions = append(actions, windows.SC_ACTION{Type: kind, Delay: uint32(action.Delay / time.Millisecond)})
	}
	if len(actions) > 0 {
		failure := windows.SERVICE_FAILURE_ACTIONS{
			ResetPeriod:  uint32(spec.FailureReset / time.Second),
			Actions:      &actions[0],
			ActionsCount: uint32(len(actions)),
		}
		if err := windows.ChangeServiceConfig2(service, windows.SERVICE_CONFIG_FAILURE_ACTIONS, (*byte)(unsafe.Pointer(&failure))); err != nil {
			return err
		}
	}

	flag := windows.SERVICE_FAILURE_ACTIONS_FLAG{FailureActionsOnNonCrashFailures: 1}
	if err := windows.ChangeServiceConfig2(service, windows.SERVICE_CONFIG_FAILURE_ACTIONS_FLAG, (*byte)(unsafe.Pointer(&flag))); err != nil {
		return err
	}

	if err := windows.StartService(service, 0, nil); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return err
	}
	return nil
}

func serviceDependencies(dependencies []string) *uint16 {
	if len(dependencies) == 0 {
		return nil
	}
	encoded := make([]uint16, 0, len(dependencies)*8)
	for _, dependency := range dependencies {
		encoded = append(encoded, utf16.Encode([]rune(dependency))...)
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	return &encoded[0]
}

func serviceStartType(startType string) uint32 {
	if strings.EqualFold(startType, "auto") {
		return windows.SERVICE_AUTO_START
	}
	return windows.SERVICE_DEMAND_START
}
