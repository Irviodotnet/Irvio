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
	"path/filepath"
	"syscall"
	"time"
)

const DefaultSocketPath = "/run/irvio/control.sock"

func defaultLocalPlatform(config *ServerConfig) {
	uid, gid := os.Geteuid(), os.Getegid()
	config.OwnerUID, config.OwnerGID = uid, gid
	config.Authorizer = CredentialPolicy{
		AllowedUIDs: map[uint32]struct{}{uint32(uid): {}},
		AllowedGIDs: map[uint32]struct{}{uint32(gid): {}},
	}
}

func validateLocalPlatform(config ServerConfig) error {
	if !filepath.IsAbs(config.SocketPath) {
		return ErrInvalidConfig
	}
	return nil
}

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func (server *Server) listen() (net.Listener, os.FileInfo, error) {
	if listener, err := inheritedSystemdListener(server.config.SocketPath); listener != nil || err != nil {
		return listener, nil, err
	}
	parent := filepath.Dir(server.config.SocketPath)
	parentInfo, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(parent, server.config.DirectoryMode.Perm()); err != nil {
			return nil, nil, fmt.Errorf("create local API directory: %w", err)
		}
		if err := os.Chown(parent, server.config.OwnerUID, server.config.OwnerGID); err != nil {
			return nil, nil, fmt.Errorf("own local API directory: %w", err)
		}
		parentInfo, err = os.Lstat(parent)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect local API directory: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("local API parent is not a real directory")
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok || int(parentStat.Uid) != server.config.OwnerUID || int(parentStat.Gid) != server.config.OwnerGID ||
		parentInfo.Mode().Perm()&0o007 != 0 {
		if !ok {
			return nil, nil, errors.New("local API parent ownership is unavailable")
		}
		return nil, nil, fmt.Errorf("local API parent has unsafe ownership or permissions: uid=%d gid=%d mode=%04o", parentStat.Uid, parentStat.Gid, parentInfo.Mode().Perm())
	}
	if err := server.removeStaleSocket(); err != nil {
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.config.SocketPath, Net: "unix"})
	if err != nil {
		return nil, nil, fmt.Errorf("listen on local API socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	fail := func(operation error) (*net.UnixListener, os.FileInfo, error) {
		_ = listener.Close()
		_ = os.Remove(server.config.SocketPath)
		return nil, nil, operation
	}
	if err := os.Chmod(server.config.SocketPath, server.config.SocketMode.Perm()); err != nil {
		return fail(fmt.Errorf("set local API socket mode: %w", err))
	}
	if err := os.Chown(server.config.SocketPath, server.config.OwnerUID, server.config.OwnerGID); err != nil {
		return fail(fmt.Errorf("own local API socket: %w", err))
	}
	info, err := os.Lstat(server.config.SocketPath)
	if err != nil {
		return fail(fmt.Errorf("inspect local API socket: %w", err))
	}
	return listener, info, nil
}

func inheritedSystemdListener(path string) (*net.UnixListener, error) {
	fds, pid := os.Getenv("LISTEN_FDS"), os.Getenv("LISTEN_PID")
	if fds == "" && pid == "" {
		return nil, nil
	}
	if fds != "1" || pid != fmt.Sprint(os.Getpid()) {
		return nil, errors.New("invalid systemd Local API socket activation environment")
	}
	file := os.NewFile(3, "systemd-hydra-control")
	if file == nil {
		return nil, errors.New("systemd Local API descriptor is missing")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("adopt systemd Local API socket: %w", err)
	}
	unix, ok := listener.(*net.UnixListener)
	address, addressOK := listener.Addr().(*net.UnixAddr)
	if !ok || !addressOK || address.Name != path {
		_ = listener.Close()
		return nil, errors.New("systemd Local API descriptor has unexpected type or path")
	}
	unix.SetUnlinkOnClose(false)
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	return unix, nil
}

func (server *Server) removeStaleSocket() error {
	info, err := os.Lstat(server.config.SocketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing local API path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to replace non-socket local API path")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != server.config.OwnerUID || int(stat.Gid) != server.config.OwnerGID {
		return errors.New("refusing to replace foreign local API socket")
	}
	connection, dialErr := net.DialTimeout("unix", server.config.SocketPath, 100*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return ErrSocketInUse
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("cannot prove local API socket is stale: %w", dialErr)
	}
	if err := os.Remove(server.config.SocketPath); err != nil {
		return fmt.Errorf("remove stale local API socket: %w", err)
	}
	return nil
}

func (server *Server) removeOwnedSocket() {
	server.mu.Lock()
	expected := server.socketInfo
	path := server.config.SocketPath
	server.mu.Unlock()
	if expected == nil {
		return
	}
	current, err := os.Lstat(path)
	if err == nil && current.Mode()&os.ModeSocket != 0 && os.SameFile(expected, current) {
		_ = os.Remove(path)
	}
}
