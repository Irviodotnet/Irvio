// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/networkstate"
)

const (
	resolvectlPath            = "/usr/bin/resolvectl"
	maxTunnelDNSServers       = 8
	tunnelDNSOperationTimeout = 3 * time.Second
	maxTunnelDNSStderr        = 4 * 1024
)

type tunnelDNSCommand func(context.Context, ...string) error
type tunnelDNSLinkIndex func(string) (int, error)

type tunnelDNSState struct {
	token     uint64
	linkIndex int
	servers   []netip.Addr
}

type boundedCommandOutput struct {
	data      []byte
	limit     int
	truncated bool
}

func (w *boundedCommandOutput) Write(value []byte) (int, error) {
	remaining := w.limit - len(w.data)
	if remaining > 0 {
		w.data = append(w.data, value[:min(remaining, len(value))]...)
	}
	if len(value) > remaining {
		w.truncated = true
	}
	return len(value), nil
}

func (w *boundedCommandOutput) String() string {
	value := strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' {
			return ' '
		}
		if character < 0x20 || character == 0x7f {
			return '?'
		}
		return character
	}, string(bytes.ToValidUTF8(w.data, []byte("?"))))
	value = strings.Join(strings.Fields(value), " ")
	if w.truncated {
		value += " [truncated]"
	}
	return value
}

type tunnelDNSCommandError struct {
	operation     string
	interfaceName string
	stderr        string
	err           error
}

func (e *tunnelDNSCommandError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("resolvectl operation=%s interface=%q: %v", e.operation, e.interfaceName, e.err)
	}
	return fmt.Sprintf("resolvectl operation=%s interface=%q: %v: stderr=%q", e.operation, e.interfaceName, e.err, e.stderr)
}

func (e *tunnelDNSCommandError) Unwrap() error { return e.err }

// tunnelDNSManager owns TUN per-link DNS; session tokens prevent stale reverts after ownership transfer.
type tunnelDNSManager struct {
	interfaceName string
	servers       []netip.Addr
	run           tunnelDNSCommand
	linkIndex     tunnelDNSLinkIndex
	timeout       time.Duration
	next          atomic.Uint64

	mu       sync.Mutex
	current  tunnelDNSState
	closed   bool
	closeErr error
}

type tunnelDNSInstaller struct {
	base    SessionInstaller
	network NetworkReconciler
	dns     *tunnelDNSManager
}

type stagedTunnelDNS struct {
	base  io.Closer
	dns   *tunnelDNSManager
	lease InstallRequest
	token uint64

	mu        sync.Mutex
	promoted  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func newTunnelDNSManager(interfaceName string, servers []netip.Addr, run tunnelDNSCommand, linkIndex tunnelDNSLinkIndex) (*tunnelDNSManager, error) {
	if !validTunnelDNSInterfaceName(interfaceName) || len(servers) == 0 || len(servers) > maxTunnelDNSServers {
		return nil, ErrInvalidConfig
	}
	validated := make([]netip.Addr, 0, len(servers))
	seen := make(map[netip.Addr]struct{}, len(servers))
	for _, server := range servers {
		if !server.IsValid() || server.IsUnspecified() || server.IsLoopback() || server.IsLinkLocalUnicast() ||
			server.IsMulticast() || server.Is4In6() || server.Zone() != "" {
			return nil, fmt.Errorf("invalid tunnel DNS server: %w", ErrInvalidConfig)
		}
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		validated = append(validated, server)
	}
	if len(validated) == 0 {
		return nil, ErrInvalidConfig
	}
	if run == nil {
		run = runTunnelDNSCommand
	}
	if linkIndex == nil {
		linkIndex = func(name string) (int, error) {
			link, err := net.InterfaceByName(name)
			if err != nil {
				return 0, err
			}
			return link.Index, nil
		}
	}
	return &tunnelDNSManager{
		interfaceName: interfaceName,
		servers:       validated,
		run:           run,
		linkIndex:     linkIndex,
		timeout:       tunnelDNSOperationTimeout,
	}, nil
}

func runTunnelDNSCommand(ctx context.Context, args ...string) error {
	return runTunnelDNSExecutable(ctx, resolvectlPath, args...)
}

func runTunnelDNSExecutable(ctx context.Context, executable string, args ...string) error {
	command := exec.CommandContext(ctx, executable, args...)
	stderr := &boundedCommandOutput{limit: maxTunnelDNSStderr}
	command.Stdout = io.Discard
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		operation, interfaceName := "unknown", "unknown"
		if len(args) > 0 {
			operation = args[0]
		}
		if len(args) > 1 {
			interfaceName = args[1]
		}
		return &tunnelDNSCommandError{operation: operation, interfaceName: interfaceName, stderr: stderr.String(), err: err}
	}
	return nil
}

func validTunnelDNSInterfaceName(name string) bool {
	if name == "" || len(name) > 15 || name[0] == '-' {
		return false
	}
	for _, value := range name {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' ||
			value == '_' || value == '-' || value == '.' {
			continue
		}
		return false
	}
	return true
}

func newTunnelDNSInstaller(base SessionInstaller, network NetworkReconciler, dns *tunnelDNSManager) (*tunnelDNSInstaller, error) {
	if base == nil || dns == nil {
		return nil, ErrInvalidConfig
	}
	return &tunnelDNSInstaller{base: base, network: network, dns: dns}, nil
}

func (i *tunnelDNSInstaller) Install(ctx context.Context, request InstallRequest) (io.Closer, error) {
	if i == nil || ctx == nil {
		return nil, ErrInvalidConfig
	}
	base, err := i.base.Install(ctx, request)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, ErrInvalidConfig
	}
	if _, ok := base.(SessionPromoter); !ok {
		_ = base.Close()
		return nil, fmt.Errorf("tunnel DNS requires a staged session: %w", ErrInvalidConfig)
	}
	return &stagedTunnelDNS{base: base, dns: i.dns, lease: request, token: i.dns.next.Add(1)}, nil
}

func (i *tunnelDNSInstaller) ReconcileNetwork(ctx context.Context, state networkstate.State) error {
	if i == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if i.network == nil {
		return nil
	}
	return i.network.ReconcileNetwork(ctx, state)
}

func (s *stagedTunnelDNS) Promote(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.promoted {
		return nil
	}
	promoter := s.base.(SessionPromoter)
	if err := promoter.Promote(ctx); err != nil {
		return err
	}
	if err := s.dns.activate(ctx, s.token, s.lease); err != nil {
		return localPromotionFailure("tunnel_dns_activate", errors.Join(err, s.base.Close()))
	}
	s.promoted = true
	return nil
}

func (s *stagedTunnelDNS) StageRemoteAWGKey(oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	rotator, ok := s.base.(SessionAWGKeyRotator)
	if !ok {
		return ErrInvalidConfig
	}
	return rotator.StageRemoteAWGKey(oldKey, newKey, remote)
}

func (s *stagedTunnelDNS) CommitRemoteAWGKey(ctx context.Context, oldKey, newKey awg.Key, remote peer.ID) error {
	if s == nil || ctx == nil || remote == "" {
		return ErrInvalidConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.promoted {
		return ErrGenerationStale
	}
	rotator, ok := s.base.(SessionAWGKeyRotator)
	if !ok {
		return ErrInvalidConfig
	}
	return rotator.CommitRemoteAWGKey(ctx, oldKey, newKey, remote)
}

func (s *stagedTunnelDNS) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		promoted := s.promoted
		s.mu.Unlock()
		if promoted {
			s.closeErr = s.dns.release(s.token)
		}
		s.closeErr = errors.Join(s.closeErr, s.base.Close())
	})
	return s.closeErr
}

func (m *tunnelDNSManager) activate(ctx context.Context, token uint64, request InstallRequest) error {
	if m == nil || ctx == nil || token == 0 {
		return ErrInvalidConfig
	}
	servers := m.serversForLease(request)
	if len(servers) == 0 {
		return fmt.Errorf("no tunnel DNS server matches the lease address families: %w", ErrInvalidConfig)
	}
	linkIndex, err := m.linkIndex(m.interfaceName)
	if err != nil || linkIndex <= 0 {
		return fmt.Errorf("resolve tunnel DNS link %q: %w", m.interfaceName, errors.Join(err, ErrInvalidConfig))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if m.current.linkIndex == linkIndex && slices.Equal(m.current.servers, servers) {
		m.current.token = token
		return nil
	}
	operationContext, operationCancel := context.WithTimeout(ctx, m.timeout)
	err = m.apply(operationContext, servers)
	operationCancel()
	if err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), m.timeout)
		cleanupErr := m.run(cleanupContext, "revert", m.interfaceName)
		cancel()
		if m.current.linkIndex == linkIndex {
			m.current = tunnelDNSState{}
		}
		return errors.Join(err, cleanupErr)
	}
	m.current = tunnelDNSState{token: token, linkIndex: linkIndex, servers: append([]netip.Addr(nil), servers...)}
	return nil
}

func (m *tunnelDNSManager) serversForLease(request InstallRequest) []netip.Addr {
	result := make([]netip.Addr, 0, len(m.servers))
	for _, server := range m.servers {
		if (server.Is4() && request.Lease.IPv4.IsValid()) || (server.Is6() && request.Lease.IPv6.IsValid()) {
			result = append(result, server)
		}
	}
	return result
}

func (m *tunnelDNSManager) apply(ctx context.Context, servers []netip.Addr) error {
	values := make([]string, 0, 2+len(servers))
	values = append(values, "dns", m.interfaceName)
	for _, server := range servers {
		values = append(values, server.String())
	}
	if err := m.run(ctx, values...); err != nil {
		return fmt.Errorf("configure tunnel DNS servers: %w", err)
	}
	if err := m.run(ctx, "domain", m.interfaceName, "~."); err != nil {
		return fmt.Errorf("configure tunnel DNS routing domain: %w", err)
	}
	if err := m.run(ctx, "default-route", m.interfaceName, "yes"); err != nil {
		return fmt.Errorf("configure tunnel DNS default route: %w", err)
	}
	return nil
}

func (m *tunnelDNSManager) release(token uint64) error {
	if m == nil || token == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current.token != token {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	err := m.run(ctx, "revert", m.interfaceName)
	cancel()
	if err == nil {
		m.current = tunnelDNSState{}
	}
	return err
}

func (m *tunnelDNSManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.current.token == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	m.closeErr = m.run(ctx, "revert", m.interfaceName)
	cancel()
	if m.closeErr == nil {
		m.current = tunnelDNSState{}
	}
	return m.closeErr
}

var _ SessionInstaller = (*tunnelDNSInstaller)(nil)
var _ NetworkReconciler = (*tunnelDNSInstaller)(nil)
var _ SessionPromoter = (*stagedTunnelDNS)(nil)
var _ SessionAWGKeyRotator = (*stagedTunnelDNS)(nil)
