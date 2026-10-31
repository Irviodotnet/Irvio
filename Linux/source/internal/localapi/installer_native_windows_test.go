// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// The fresh installer gate uses its real bridge and an explicit ordinary local account.
func TestWindowsInstalledDesktopActivation(t *testing.T) {
	if os.Getenv("IRVIO_INSTALLED_DESKTOP_TEST") != "1" {
		t.Skip("fresh disposable installer and non-admin account required")
	}
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	program, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(desktopServiceName)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	before, err := service.Query()
	if err != nil || before.State != svc.Stopped {
		t.Fatal("installed service must begin stopped", before, err)
	}
	if err := withTestToken(allowed, func() error {
		handle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
		if err != nil {
			return err
		}
		defer windows.CloseServiceHandle(handle)
		name, _ := windows.UTF16PtrFromString(desktopServiceName)
		for _, access := range []uint32{windows.SERVICE_STOP, windows.SERVICE_CHANGE_CONFIG, windows.WRITE_DAC} {
			h, err := windows.OpenService(handle, name, access)
			if err == nil {
				windows.CloseServiceHandle(h)
				return errors.New("ordinary user has excess service rights")
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return err
			}
		}
		path, _ := windows.UTF16PtrFromString(filepath.Join(program, "IRVIO", "daemon", "irviod.exe"))
		h, err := windows.CreateFile(path, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
			return errors.New("ordinary user can write the privileged daemon")
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(program, "IRVIO", "ui", "irvio-ui-bridge.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(allowed)}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
	call := func(id uint64, method localv1.Method) *localv1.Response {
		t.Helper()
		request := &localv1.Envelope{ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: id, Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: method}}}
		if err := WriteFrame(in, request); err != nil {
			t.Fatal(err)
		}
		response, err := ReadFrame(out)
		if err != nil || response.GetResponse().GetCode() != localv1.ResultCode_RESULT_CODE_OK {
			cancel()
			in.Close()
			_ = cmd.Wait()
			t.Fatal("installed bridge request failed", method, response, err, stderr.String())
		}
		return response.GetResponse()
	}
	if call(1, localv1.Method_METHOD_HELLO).GetHello() == nil {
		t.Fatal("missing HELLO")
	}
	status := call(2, localv1.Method_METHOD_GET_STATUS).GetStatus()
	if status == nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.ContributionEnabled || status.PrivateHostingWanted {
		t.Fatal("fresh install not idle", status)
	}
	bootstrap := call(3, localv1.Method_METHOD_GET_BOOTSTRAP_STATE).GetBootstrapState()
	if bootstrap == nil || bootstrap.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
		t.Fatal("fresh bootstrap not empty", bootstrap)
	}
	call(4, localv1.Method_METHOD_SHUTDOWN)
	in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal("bridge did not exit cleanly", err, stderr.String())
	}
	for {
		s, err := service.Query()
		if err != nil {
			t.Fatal(err)
		}
		if s.State == svc.Stopped {
			if s.Win32ExitCode != 0 || s.ServiceSpecificExitCode != 0 {
				t.Fatal("unclean shutdown", s)
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("service did not stop", ctx.Err())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Log("ordinary user: narrow SCM rights, protected daemon, activation, IDLE/EMPTY, authenticated API, shutdown PASS")
}
