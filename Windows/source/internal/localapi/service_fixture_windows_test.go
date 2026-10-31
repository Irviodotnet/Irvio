// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/svc"
)

type pipeTestService struct{ path, sid string }

func (s pipeTestService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	config := DefaultServerConfig(s.path)
	config.Authorizer = CredentialPolicy{AllowedSID: s.sid}
	server, err := NewServer(config, &nativeAPIController{})
	if err != nil {
		return false, 1
	}
	if err := server.Start(context.Background()); err != nil {
		return false, 2
	}
	defer server.Close()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- current
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			status <- current
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			if err := server.Close(); err != nil {
				return false, 3
			}
			return false, 0
		}
	}
	return false, 4
}

func TestWindowsPipeServiceFixture(t *testing.T) {
	var args []string
	for i, value := range os.Args {
		if value == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		t.Skip("explicit disposable SCM fixture invocation required")
	}
	if len(args) != 3 || !strings.HasPrefix(args[0], "IRVIO-API-Fixture-") || !validPipePath(args[1]) || !validClientSID(args[2]) {
		t.Fatal("invalid SCM fixture arguments")
	}
	service, err := svc.IsWindowsService()
	if err != nil || !service {
		t.Fatal("SCM fixture must be started by Service Control Manager", err)
	}
	if err := svc.Run(args[0], pipeTestService{path: args[1], sid: args[2]}); err != nil {
		t.Fatal(err)
	}
}
