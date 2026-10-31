// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const desktopServiceName = "IRVIO"

type desktopService interface {
	Config() (mgr.Config, error)
	Query() (svc.Status, error)
	Start(...string) error
}

// DialDesktopTransport activates the installed service and returns its authenticated byte stream.
func DialDesktopTransport(ctx context.Context) (net.Conn, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, fmt.Errorf("open service manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString(desktopServiceName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.OpenService(manager, name, windows.SERVICE_START|windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return nil, fmt.Errorf("open installed IRVIO service: %w", err)
	}
	service := &mgr.Service{Name: desktopServiceName, Handle: handle}
	defer service.Close()
	if err := startDesktopService(ctx, service); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dialLocalService(ctx, DefaultSocketPath, desktopServiceName)
		if err == nil {
			owned := conn.(*pipeConnection)
			err = owned.withHandle(func(handle windows.Handle) error {
				var pid uint32
				if err := windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
					return err
				}
				return verifySystemServicePID(desktopServiceName, pid)
			})
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("authenticate installed IRVIO service: %w", err)
			}
			return conn, nil
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, err
		}
		status, statusErr := service.Query()
		if statusErr != nil {
			return nil, statusErr
		}
		if status.State != svc.Running && status.State != svc.StartPending {
			return nil, fmt.Errorf("IRVIO service stopped before Local API became ready")
		}
		if err := waitDesktopService(ctx); err != nil {
			return nil, err
		}
	}
}

func startDesktopService(ctx context.Context, service desktopService) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := service.Config()
	if err != nil {
		return err
	}
	if !strings.EqualFold(config.ServiceStartName, "LocalSystem") || config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
		return fmt.Errorf("IRVIO must be an isolated LocalSystem service: %w", ErrUnauthorized)
	}
	started := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := service.Query()
		if err != nil {
			return err
		}
		switch status.State {
		case svc.Running:
			return nil
		case svc.Stopped:
			if started {
				return fmt.Errorf("IRVIO service failed during startup (exit %d)", status.Win32ExitCode)
			}
			if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
				return fmt.Errorf("start IRVIO service: %w", err)
			}
			started = true
		case svc.StartPending:
			started = true
		case svc.StopPending:
		case svc.Paused, svc.PausePending, svc.ContinuePending:
			return fmt.Errorf("IRVIO service is paused or pausing")
		default:
			return fmt.Errorf("unexpected IRVIO service state %d", status.State)
		}
		if err := waitDesktopService(ctx); err != nil {
			return err
		}
	}
}

func waitDesktopService(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
