//go:build windows

package main

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"

	"github.com/SentimentalK/host-telemetry/deploy/windows"
)

func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

func runWindowsService(opts serveOptions) error {
	return svc.Run(winunit.Name, &windowsService{opts: opts})
}

type windowsService struct {
	opts serveOptions
}

func (s *windowsService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, s.opts)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case err := <-done:
			if err != nil {
				log.Printf("host-telemetry: %v", err)
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-done; err != nil {
					log.Printf("host-telemetry: %v", err)
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			case svc.Interrogate:
				changes <- request.CurrentStatus
			}
		}
	}
}
