// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type fakeDesktopService struct {
	config                        mgr.Config
	states                        []svc.State
	starts                        int
	startErr, queryErr, configErr error
}

func (s *fakeDesktopService) Config() (mgr.Config, error) { return s.config, s.configErr }
func (s *fakeDesktopService) Start(...string) error       { s.starts++; return s.startErr }
func (s *fakeDesktopService) Query() (svc.Status, error) {
	state := s.states[0]
	if len(s.states) > 1 {
		s.states = s.states[1:]
	}
	return svc.Status{State: state}, s.queryErr
}

func TestDesktopServiceActivation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		states     []svc.State
		startErr   error
		wantStarts int
		wantErr    bool
	}{
		{"already running", []svc.State{svc.Running}, nil, 0, false},
		{"start", []svc.State{svc.Stopped, svc.StartPending, svc.Running}, nil, 1, false},
		{"join startup", []svc.State{svc.StartPending, svc.Running}, nil, 0, false},
		{"wait stop", []svc.State{svc.StopPending, svc.Stopped, svc.Running}, nil, 1, false},
		{"concurrent start", []svc.State{svc.Stopped, svc.Running}, windows.ERROR_SERVICE_ALREADY_RUNNING, 1, false},
		{"no restart loop", []svc.State{svc.Stopped, svc.StartPending, svc.Stopped}, nil, 1, true},
		{"failed other starter", []svc.State{svc.StartPending, svc.Stopped}, nil, 0, true},
		{"paused", []svc.State{svc.Paused}, nil, 0, true},
		{"denied", []svc.State{svc.Stopped}, windows.ERROR_ACCESS_DENIED, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeDesktopService{config: mgr.Config{ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS}, states: tc.states, startErr: tc.startErr}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := startDesktopService(ctx, s)
			if (err != nil) != tc.wantErr || s.starts != tc.wantStarts {
				t.Fatalf("err=%v starts=%d", err, s.starts)
			}
		})
	}
}

func TestDesktopServiceRejectsWrongIdentity(t *testing.T) {
	for _, config := range []mgr.Config{
		{ServiceStartName: "IrvioLab", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS},
		{ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_SHARE_PROCESS},
		{ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS | windows.SERVICE_INTERACTIVE_PROCESS},
	} {
		s := &fakeDesktopService{config: config}
		if err := startDesktopService(context.Background(), s); !errors.Is(err, ErrUnauthorized) || s.starts != 0 {
			t.Fatalf("err=%v starts=%d", err, s.starts)
		}
	}
}

func TestDesktopServiceBoundedWait(t *testing.T) {
	s := &fakeDesktopService{config: mgr.Config{ServiceStartName: "LocalSystem", ServiceType: windows.SERVICE_WIN32_OWN_PROCESS}, states: []svc.State{svc.StartPending}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := startDesktopService(ctx, s); !errors.Is(err, context.DeadlineExceeded) || s.starts != 0 {
		t.Fatalf("err=%v starts=%d", err, s.starts)
	}
	if _, err := DialDesktopTransport(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
}
