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
	"strings"
	"sync"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const DefaultSocketPath = `\\.\pipe\IRVIO.Control`
const pipeClientAccess = uint32(0x0012019b)

func defaultLocalPlatform(config *ServerConfig) {
	config.OwnerUID, config.OwnerGID = -1, -1
	config.Authorizer = CredentialPolicy{}
}

func validPipePath(path string) bool {
	const prefix = `\\.\pipe\IRVIO.`
	if !strings.HasPrefix(path, prefix) || len(path) <= len(prefix) || len(path) > 200 {
		return false
	}
	for _, ch := range path[len(prefix):] {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '.') {
			return false
		}
	}
	return true
}

func validateLocalPlatform(config ServerConfig) error {
	policy, ok := config.Authorizer.(CredentialPolicy)
	if !validPipePath(config.SocketPath) || !ok || !validClientSID(policy.AllowedSID) || config.OwnerUID != -1 || config.OwnerGID != -1 {
		return fmt.Errorf("Local API needs a local IRVIO pipe and an explicit client account SID: %w", ErrInvalidConfig)
	}
	return nil
}

func (server *Server) listen() (net.Listener, os.FileInfo, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return nil, nil, fmt.Errorf("Local API service must run as SYSTEM: %w", errors.Join(ErrUnauthorized, err))
	}
	policy := server.config.Authorizer.(CredentialPolicy)
	sddl := "O:SYG:SYD:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x0012019b;;;" + policy.AllowedSID + ")"
	listener, err := winio.ListenPipe(server.config.SocketPath, &winio.PipeConfig{
		SecurityDescriptor: sddl, InputBufferSize: MaxFrameSize, OutputBufferSize: MaxFrameSize,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create exclusive Local API pipe: %w", err)
	}
	return &pipeListener{Listener: listener}, nil, nil
}

// Named pipes are kernel objects; closing all owned handles removes the name.
func (server *Server) removeOwnedSocket() {}

type pipeListener struct{ net.Listener }

func (listener *pipeListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return ownPipeConnection(conn)
}

type pipeConnection struct {
	net.Conn
	mu     sync.Mutex
	handle windows.Handle
	closed bool
}

func ownPipeConnection(conn net.Conn) (*pipeConnection, error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		_ = conn.Close()
		return nil, ErrUnauthorized
	}
	return &pipeConnection{Conn: conn, handle: windows.Handle(file.Fd())}, nil
}

func (conn *pipeConnection) withHandle(fn func(windows.Handle) error) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return net.ErrClosed
	}
	return fn(conn.handle)
}

func (conn *pipeConnection) Close() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return nil
	}
	conn.closed = true
	return conn.Conn.Close()
}

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	return dialLocalService(ctx, path, "IRVIO")
}

func dialLocalService(ctx context.Context, path, serviceName string) (net.Conn, error) {
	if !validPipePath(path) {
		return nil, ErrInvalidConfig
	}
	value, err := winio.DialPipeAccessImpLevel(ctx, path, pipeClientAccess, winio.PipeImpLevelIdentification)
	if err != nil {
		return nil, err
	}
	conn, err := ownPipeConnection(value)
	if err != nil {
		return nil, err
	}
	err = conn.withHandle(func(handle windows.Handle) error {
		var pid uint32
		if err := windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
			return err
		}
		sid, err := processUserSID(pid)
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			if err := verifySystemServicePID(serviceName, pid); err != nil {
				return errors.Join(ErrUnauthorized, err)
			}
			sid, err = "S-1-5-18", nil
		}
		if err != nil || sid != "S-1-5-18" {
			return errors.Join(ErrUnauthorized, err)
		}
		security, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		owner, _, err := security.Owner()
		if err != nil || owner == nil || !owner.IsWellKnown(windows.WinLocalSystemSid) {
			return errors.Join(ErrUnauthorized, err)
		}
		return nil
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("Local API pipe is not owned by the SYSTEM service: %w", err)
	}
	return conn, nil
}

func verifySystemServicePID(name string, pid uint32) error {
	if name == "" || pid == 0 {
		return ErrUnauthorized
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(manager)
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	handle, err := windows.OpenService(manager, name16, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	service := &mgr.Service{Name: name, Handle: handle}
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		return err
	}
	if !strings.EqualFold(config.ServiceStartName, "LocalSystem") || config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
		return ErrUnauthorized
	}
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.ProcessId != pid || status.State != svc.Running {
		return ErrUnauthorized
	}
	return nil
}
