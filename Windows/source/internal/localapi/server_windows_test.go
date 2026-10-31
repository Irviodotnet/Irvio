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
	"io"
	"log/slog"
	"net"
	"os"
	goruntime "runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/wintest"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type nativeAPIController struct {
	mu                    sync.Mutex
	state                 runtime.Status
	connects, disconnects int
}

func (c *nativeAPIController) Status() runtime.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
func (c *nativeAPIController) Connect(_ context.Context, country exitselector.CountryIntent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects++
	c.state = runtime.Status{State: runtime.StateConnected, Country: country}
	return nil
}
func (c *nativeAPIController) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects++
	c.state = runtime.Status{State: runtime.StateIdle}
	return nil
}

func nativePipeName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`\\.\pipe\IRVIO.Test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

func nativeTestConfig(t *testing.T) ServerConfig {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		t.Skip("real SYSTEM service token required")
	}
	config := DefaultServerConfig(nativePipeName(t))
	config.Authorizer = CredentialPolicy{AllowedSID: "S-1-5-21-1-2-3-1001"}
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	config.IdleTimeout = time.Second
	config.FrameTimeout = 100 * time.Millisecond
	config.WriteTimeout = 100 * time.Millisecond
	return config
}

func nativeTestServer(t *testing.T, config ServerConfig) (*Server, *nativeAPIController) {
	t.Helper()
	controller := &nativeAPIController{state: runtime.Status{State: runtime.StateIdle}}
	server, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	return server, controller
}

func testServerConfig(t *testing.T) ServerConfig {
	return nativeTestConfig(t)
}

func startTestServer(t *testing.T, controller *fakeController, configure func(*ServerConfig)) (*Server, string) {
	t.Helper()
	config := testServerConfig(t)
	config.PollInterval = time.Millisecond
	config.RequestTimeout = 100 * time.Millisecond
	if configure != nil {
		configure(&config)
	}
	server, err := NewServer(config, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close server: %v", err)
		}
		if err := server.Close(); err != nil {
			t.Errorf("second close: %v", err)
		}
	})
	return server, config.SocketPath
}

func TestWindowsNamedPipeLifecycleResources(t *testing.T) {
	config := nativeTestConfig(t)
	previousProcs := goruntime.GOMAXPROCS(1)
	defer goruntime.GOMAXPROCS(previousProcs)
	handles := func() uint32 {
		var count uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(
			uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			t.Fatal(err)
		}
		return count
	}
	cycle := func() {
		t.Helper()
		server, err := NewServer(config, &nativeAPIController{})
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		client, err := Dial(ctx, config.SocketPath)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.GetStatus(ctx); err != nil {
			t.Fatal(err)
		}
		if err := client.Connect(ctx, "NL"); err != nil {
			t.Fatal(err)
		}
		if err := client.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for range 20 {
		cycle()
	}
	goruntime.GC()
	before, beforeGo := handles(), goruntime.NumGoroutine()
	beforeTypes := wintest.HandleTypes()
	for i := 0; i < 100; i++ {
		cycle()
	}
	deadline := time.Now().Add(2 * time.Second)
	for goruntime.NumGoroutine() > beforeGo+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	goruntime.GC()
	after, afterGo := handles(), goruntime.NumGoroutine()
	afterTypes := wintest.HandleTypes()
	t.Logf("100 API-only lifecycle cycles: handles %d -> %d; goroutines %d -> %d", before, after, beforeGo, afterGo)
	t.Logf("handle types before=%v after=%v", beforeTypes, afterTypes)
	threads := max(0, afterTypes["Thread"]-beforeTypes["Thread"])
	if err := wintest.ResourceGrowth(beforeTypes, afterTypes); err != nil {
		t.Fatal(err)
	}
	perThread := map[string]int{"Thread": 1, "Event": 2, "IRTimer": 2, "WaitCompletionPacket": 1}
	for kind, count := range afterTypes {
		if count > beforeTypes[kind]+perThread[kind]*threads {
			t.Fatalf("unexplained %s handle growth", kind)
		}
	}
	if after > before+uint32(6*threads) || afterGo > beforeGo+2 {
		t.Fatal("Local API resource growth")
	}
}

func TestWindowsPipeConfigRejectsUnsafeIdentityAndPath(t *testing.T) {
	config := DefaultServerConfig(DefaultSocketPath)
	controller := &nativeAPIController{}
	if _, err := NewServer(config, controller); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("missing client SID accepted", err)
	}
	for _, sid := range []string{"S-1-1-0", "S-1-5-18", "S-1-5-32-545", "bad;(A;;GA;;;WD)", ""} {
		config.Authorizer = CredentialPolicy{AllowedSID: sid}
		if _, err := NewServer(config, controller); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("unsafe client SID accepted", sid, err)
		}
	}
	config.Authorizer = CredentialPolicy{AllowedSID: "S-1-5-21-1-2-3-1001"}
	for _, path := range []string{`\\remote\pipe\IRVIO.Control`, `\\.\pipe\Other`, `\\.\pipe\IRVIO.`, `\\.\pipe\IRVIO.foo\bar`, `C:\control.sock`} {
		config.SocketPath = path
		if _, err := NewServer(config, controller); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("unsafe pipe path accepted", path, err)
		}
	}
}

func TestWindowsNamedPipeCommandsAndGUIReconnect(t *testing.T) {
	config := nativeTestConfig(t)
	server, controller := nativeTestServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := Dial(ctx, config.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hello().NegotiatedApiMinor != APIMinor {
		t.Fatal("API negotiation changed")
	}
	if _, err := first.GetStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.Connect(ctx, "NL"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if controller.Status().State != runtime.StateConnected {
		t.Fatal("GUI close disconnected runtime")
	}
	second, err := Dial(ctx, config.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.GetStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if controller.Status().State != runtime.StateIdle {
		t.Fatal("DISCONNECT not dispatched")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if server.SessionCount() != 0 {
		t.Fatal("pipe session survives close")
	}
	if _, err := Dial(ctx, config.SocketPath); err == nil {
		t.Fatal("closed named pipe accepted a client")
	}
}

func TestWindowsNamedPipeExclusiveListener(t *testing.T) {
	config := nativeTestConfig(t)
	first, _ := nativeTestServer(t, config)
	second, err := NewServer(config, &nativeAPIController{})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err == nil {
		second.Close()
		t.Fatal("second pipe server accepted")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNamedPipePartialFrameAndCanceledDial(t *testing.T) {
	config := nativeTestConfig(t)
	server, _ := nativeTestServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialLocal(ctx, config.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := conn.Read(one[:]); err == nil {
		t.Fatal("incomplete frame accepted")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("frame deadline did not disconnect client", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := dialLocal(canceled, config.SocketPath); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled dial ignored", err)
	}
}

func TestWindowsNamedPipeRejectsAnonymousMessageToken(t *testing.T) {
	config := nativeTestConfig(t)
	_, controller := nativeTestServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, config.SocketPath, pipeClientAccess)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := &localv1.Envelope{ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: 1, Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: localv1.Method_METHOD_HELLO}}}
	if err := WriteFrame(conn, request); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	reply, err := ReadFrame(conn)
	if err != nil || reply.GetResponse().GetCode() != localv1.ResultCode_RESULT_CODE_UNAUTHORIZED {
		t.Fatal("anonymous token was not explicitly rejected", reply, err)
	}
	if controller.Status().State != runtime.StateIdle {
		t.Fatal("unauthorized request reached controller")
	}
}
