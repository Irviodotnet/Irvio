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
	"os"
	goruntime "runtime"
	"testing"
	"time"
	"unsafe"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func testLogonToken(t *testing.T, prefix string) windows.Token {
	t.Helper()
	user, password := os.Getenv(prefix+"_USER"), os.Getenv(prefix+"_PASSWORD")
	if user == "" || password == "" {
		t.Fatal("temporary account inputs missing")
	}
	name, err := windows.UTF16PtrFromString(user)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := windows.UTF16PtrFromString(password)
	if err != nil {
		t.Fatal("invalid test credential")
	}
	domain, _ := windows.UTF16PtrFromString(".")
	var token windows.Token
	ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW").Call(
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(secret)), 2, 0, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		t.Fatal("temporary user logon failed", err)
	}
	t.Cleanup(func() { token.Close() })
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			t.Fatal("test user is an administrator")
		}
	}
	return token
}

func withTestToken(token windows.Token, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		goruntime.LockOSThread()
		ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser").Call(uintptr(token))
		if ok == 0 {
			goruntime.UnlockOSThread()
			done <- err
			return
		}
		err = fn()
		revert := windows.RevertToSelf()
		if revert == nil {
			goruntime.UnlockOSThread()
		}
		done <- errors.Join(err, revert)
	}()
	return <-done
}

func TestWindowsNamedPipeNonAdminACL(t *testing.T) {
	if os.Getenv("IRVIO_PIPE_ALLOWED_USER") == "" {
		t.Skip("explicit two-account native ACL fixture required")
	}
	config := nativeTestConfig(t)
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	denied := testLogonToken(t, "IRVIO_PIPE_DENIED")
	user, err := allowed.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	config.Authorizer = CredentialPolicy{AllowedSID: user.User.Sid.String()}
	config.SocketPath = os.Getenv("IRVIO_PIPE_SERVICE_PATH")
	serviceName := os.Getenv("IRVIO_PIPE_SERVICE_NAME")
	if !validPipePath(config.SocketPath) || serviceName == "" {
		t.Fatal("real SCM fixture missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var conn net.Conn
	err = withTestToken(allowed, func() error {
		var err error
		conn, err = dialLocalService(ctx, config.SocketPath, serviceName)
		return err
	})
	if err != nil {
		t.Fatal("allowed non-admin cannot authenticate service", err)
	}
	defer conn.Close()
	request := &localv1.Envelope{ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: 1, Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: localv1.Method_METHOD_HELLO}}}
	if err := WriteFrame(conn, request); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := ReadFrame(conn)
	if err != nil || response.GetResponse().GetHello() == nil {
		t.Fatal("non-admin HELLO rejected", response, err)
	}
	err = withTestToken(denied, func() error {
		unexpected, err := winio.DialPipeAccessImpLevel(ctx, config.SocketPath, pipeClientAccess, winio.PipeImpLevelIdentification)
		if unexpected != nil {
			unexpected.Close()
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("other account was not denied by kernel DACL: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = withTestToken(allowed, func() error {
		path, _ := windows.UTF16PtrFromString(config.SocketPath)
		handle, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
			windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 255, 4096, 4096, 0, nil)
		if err == nil {
			windows.CloseHandle(handle)
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("client pipe-instance creation not denied: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("real non-admin: allowed HELLO, other user denied, client cannot create server instance")
}

func TestWindowsNamedPipeNonAdminRejectsOtherSystemProcess(t *testing.T) {
	if os.Getenv("IRVIO_PIPE_ALLOWED_USER") == "" {
		t.Skip("explicit account and SCM fixture required")
	}
	config := nativeTestConfig(t)
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	user, err := allowed.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	config.Authorizer = CredentialPolicy{AllowedSID: user.User.Sid.String()}
	_, _ = nativeTestServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = withTestToken(allowed, func() error {
		conn, err := dialLocalService(ctx, config.SocketPath, os.Getenv("IRVIO_PIPE_SERVICE_NAME"))
		if conn != nil {
			conn.Close()
		}
		if !errors.Is(err, ErrUnauthorized) {
			return fmt.Errorf("different SYSTEM process accepted as SCM service: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNamedPipeClientRejectsSquatter(t *testing.T) {
	if os.Getenv("IRVIO_PIPE_ALLOWED_USER") == "" {
		t.Skip("explicit account fixture required")
	}
	_ = nativeTestConfig(t)
	allowed := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	user, err := allowed.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	path := nativePipeName(t)
	var listener net.Listener
	err = withTestToken(allowed, func() error {
		var err error
		listener, err = winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "O:" + user.User.Sid.String() + "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")"})
		return err
	})
	if err != nil {
		t.Fatal("cannot create non-admin squatter fixture", err)
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
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var buf [1]byte
		n, _ := conn.Read(buf[:])
		if n != 0 {
			done <- errors.New("client disclosed API bytes before authenticating service")
			return
		}
		done <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialLocal(ctx, path)
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal("squatter not explicitly rejected", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
