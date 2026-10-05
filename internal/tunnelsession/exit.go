// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/tunnel"
	"hydra.local/hydra/internal/tunnellease"
)

// ExitConfig creates one shared downstream TUN and policy owner.
type ExitConfig struct {
	LocalPeer        peer.ID
	NewTUN           TUNFactory
	Allocator        *tunnellease.Allocator
	EnableForwarding func(context.Context) (io.Closer, error)
	InstallFirewall  func(context.Context, tunnel.DeviceOwner) (io.Closer, error)
}

// ExitService owns shared TUN, firewall, forwarding, and allocator resources, never individual peers.
type ExitService struct {
	config     ExitConfig
	tun        tunnel.DeviceOwner
	forwarding io.Closer
	firewall   io.Closer
	mu         sync.Mutex
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

func NewExit(ctx context.Context, config ExitConfig) (_ *ExitService, resultErr error) {
	if ctx == nil || config.LocalPeer == "" || config.NewTUN == nil || config.Allocator == nil || config.EnableForwarding == nil || config.InstallFirewall == nil {
		return nil, ErrInvalidConfig
	}
	owner, err := config.NewTUN(ctx)
	if err != nil {
		return nil, fmt.Errorf("create Exit TUN: %w", err)
	}
	service := &ExitService{config: config, tun: owner}
	if owner == nil || owner.Name() == "" || owner.IfIndex() <= 0 {
		if owner != nil {
			_ = owner.Close()
		}
		return nil, ErrInvalidConfig
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, service.closeResources())
		}
	}()
	service.forwarding, err = config.EnableForwarding(ctx)
	if err != nil {
		return nil, fmt.Errorf("enable Exit forwarding: %w", err)
	}
	service.firewall, err = config.InstallFirewall(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("install Exit firewall: %w", err)
	}
	return service, nil
}

func (s *ExitService) TUN() tunnel.DeviceOwner {
	if s == nil {
		return nil
	}
	return s.tun
}
func (s *ExitService) Allocate(client peer.ID) (tunnellease.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return tunnellease.Lease{}, ErrClosed
	}
	return s.config.Allocator.Allocate(client)
}
func (s *ExitService) Release(client peer.ID, id tunnellease.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.config.Allocator.Release(client, id)
}
func (s *ExitService) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.closeErr = s.closeResources() })
	return s.closeErr
}
func (s *ExitService) closeResources() error {
	var tunErr, allocatorErr error
	if s.tun != nil {
		tunErr = s.tun.Close()
	}
	if s.config.Allocator != nil {
		allocatorErr = s.config.Allocator.Close()
	}
	return errors.Join(closeResource(s.firewall), closeResource(s.forwarding), tunErr, allocatorErr)
}
