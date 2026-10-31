// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"errors"
	"net"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

var ErrUnauthorized = errors.New("local API peer is not authorized")

type PeerCredentials struct {
	PID uint32
	SID string
}

type Authorizer interface {
	Authorize(net.Conn) (PeerCredentials, error)
}

type CredentialPolicy struct{ AllowedSID string }

func NewWindowsCredentialPolicy(sid string) (CredentialPolicy, error) {
	if !validClientSID(sid) {
		return CredentialPolicy{}, ErrUnauthorized
	}
	return CredentialPolicy{AllowedSID: sid}, nil
}

func validClientSID(value string) bool {
	sid, err := windows.StringToSid(value)
	return err == nil && sid.String() == value && strings.HasPrefix(value, "S-1-5-21-") && strings.Count(value, "-") == 7
}

func (policy CredentialPolicy) permits(sid string) bool {
	return validClientSID(policy.AllowedSID) && (sid == policy.AllowedSID || sid == "S-1-5-18")
}

func processUserSID(pid uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func (policy CredentialPolicy) Authorize(value net.Conn) (PeerCredentials, error) {
	conn, ok := value.(*pipeConnection)
	if !ok || conn == nil {
		return PeerCredentials{}, ErrUnauthorized
	}
	var peer PeerCredentials
	err := conn.withHandle(func(handle windows.Handle) error {
		if err := windows.GetNamedPipeClientProcessId(handle, &peer.PID); err != nil {
			return err
		}
		var err error
		peer.SID, err = processUserSID(peer.PID)
		if err != nil || !policy.permits(peer.SID) {
			return errors.Join(ErrUnauthorized, err)
		}
		return nil
	})
	return peer, err
}

func (policy CredentialPolicy) AuthorizeFrame(value net.Conn) error {
	conn, ok := value.(*pipeConnection)
	if !ok || conn == nil {
		return ErrUnauthorized
	}
	return conn.withHandle(func(handle windows.Handle) error {
		sid, err := pipeMessageSID(handle)
		if err != nil || !policy.permits(sid) {
			return errors.Join(ErrUnauthorized, err)
		}
		return nil
	})
}

// A failed RevertToSelf retires this locked thread rather than returning it to Go's pool.
func pipeMessageSID(handle windows.Handle) (string, error) {
	type result struct {
		sid string
		err error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		ok, _, err := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient").Call(uintptr(handle))
		if ok == 0 {
			runtime.UnlockOSThread()
			done <- result{err: err}
			return
		}
		var token windows.Token
		value := result{}
		if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
			value.err = err
		} else {
			user, err := token.GetTokenUser()
			if err == nil {
				value.sid = user.User.Sid.String()
			}
			value.err = errors.Join(err, token.Close())
		}
		if err := windows.RevertToSelf(); err != nil {
			value.err = errors.Join(value.err, err)
		} else {
			runtime.UnlockOSThread()
		}
		done <- value
	}()
	value := <-done
	return value.sid, value.err
}
