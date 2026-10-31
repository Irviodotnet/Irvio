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
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestWindowsDesktopBridgeServiceFixture(t *testing.T) {
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		t.Skip("explicit disposable IRVIO SCM fixture required")
	}
	if len(args) != 2 || !validPipePath(args[0]) || !validClientSID(args[1]) {
		t.Fatal("invalid fixture arguments")
	}
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		t.Fatal("fixture requires SCM", err)
	}
	if err := svc.Run(desktopServiceName, pipeTestService{path: args[0], sid: args[1]}); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsDesktopBridgeNonAdmin(t *testing.T) {
	binary := os.Getenv("IRVIO_BRIDGE_TEST_BINARY")
	if binary == "" {
		t.Skip("explicit installed test bridge and disposable accounts required")
	}
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	denied := testLogonToken(t, "IRVIO_PIPE_DENIED")
	if err := withTestToken(allowed, func() error {
		manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
		if err != nil {
			return err
		}
		defer windows.CloseServiceHandle(manager)
		name, _ := windows.UTF16PtrFromString(desktopServiceName)
		for _, access := range []uint32{windows.SERVICE_STOP, windows.SERVICE_CHANGE_CONFIG, windows.WRITE_DAC} {
			handle, err := windows.OpenService(manager, name, access)
			if err == nil {
				windows.CloseServiceHandle(handle)
				return errors.New("ordinary user has excess service rights")
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return err
			}
		}
		return nil
	}); err != nil {
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
		t.Fatal("fixture must begin stopped", before, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 10; i++ {
		cmd := exec.CommandContext(ctx, binary)
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(allowed)}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() { input.Close(); cmd.Process.Kill(); cmd.Wait() }()
			call := func(id uint64, method localv1.Method) *localv1.Response {
				t.Helper()
				request := &localv1.Envelope{ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: id, Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: method, CountryCode: "NL"}}}
				if err := WriteFrame(input, request); err != nil {
					t.Fatal(err)
				}
				response, err := ReadFrame(output)
				if err != nil || response.GetResponse().GetCode() != localv1.ResultCode_RESULT_CODE_OK {
					t.Fatal("bridge call failed", response, err, stderr.String())
				}
				return response.GetResponse()
			}
			if call(1, localv1.Method_METHOD_HELLO).GetHello() == nil {
				t.Fatal("HELLO missing")
			}
			if i == 0 {
				call(2, localv1.Method_METHOD_CONNECT)
			}
			status := call(3, localv1.Method_METHOD_GET_STATUS).GetStatus()
			if status.GetState() != localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
				t.Fatal("GUI closure changed fixture intent", status)
			}
			if err := input.Close(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal("bridge EOF exit", err, stderr.String())
			}
		}()
		current, err := service.Query()
		if err != nil || current.State != svc.Running || (i > 0 && current.ProcessId != before.ProcessId) {
			t.Fatal("bridge closure restarted/stopped service", current, err)
		}
		before = current
	}
	cmd := exec.CommandContext(ctx, binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(denied)}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err == nil || ctx.Err() != nil || stdout.Len() != 0 {
		t.Fatal("other user did not fail closed", err, ctx.Err(), stdout.Len())
	}
	if smoke := os.Getenv("IRVIO_BRIDGE_DART_SMOKE"); smoke != "" {
		cmd := exec.CommandContext(ctx, smoke)
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(allowed)}
		out, err := cmd.CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("API=1.7 state=connected")) {
			t.Fatalf("real Dart/bridge/pipe negotiation: %s %v", out, err)
		}
		t.Log("compiled Dart shared client -> packaged helper -> authenticated pipe PASS")
	}
	t.Log("ordinary-user SCM start + HELLO + 10 GUI closures preserve fixture intent/PID; other user denied")
}

func TestWindowsDesktopBridgeRejectsForeignPipe(t *testing.T) {
	binary := os.Getenv("IRVIO_BRIDGE_TEST_BINARY")
	if binary == "" || os.Getenv("IRVIO_BRIDGE_FOREIGN_PIPE") != "1" {
		t.Skip("explicit foreign-pipe fixture required")
	}
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	user, err := allowed.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	for _, owner := range []string{"SY", sid} {
		t.Run(owner, func(t *testing.T) {
			var listener net.Listener
			create := func() error {
				var err error
				listener, err = winio.ListenPipe(DefaultSocketPath, &winio.PipeConfig{SecurityDescriptor: "O:" + owner + "D:P(A;;GA;;;SY)(A;;GA;;;" + sid + ")"})
				return err
			}
			var err error
			if owner == "SY" {
				err = create()
			} else {
				err = withTestToken(allowed, create)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				var b [1]byte
				n, err := conn.Read(b[:])
				if n != 0 || err != io.EOF {
					done <- errors.New("foreign pipe received data or did not close")
					return
				}
				done <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(allowed)}
			cmd.Stdin = bytes.NewReader([]byte{0, 0, 0, 1, 42})
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			if err := cmd.Run(); err == nil || ctx.Err() != nil || stdout.Len() != 0 {
				t.Fatal("foreign pipe not rejected", err, ctx.Err(), stdout.Len())
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("foreign connection did not terminate")
			}
		})
	}
}
