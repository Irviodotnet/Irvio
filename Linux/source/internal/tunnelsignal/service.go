// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsignal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

// Service issues and releases leases only for the authenticated RemotePeer.
type Service struct {
	host      host.Host
	authority leaseAuthority
	timeout   time.Duration
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	handlers  sync.WaitGroup
}

type leaseAuthority interface {
	Allocate(peer.ID, tunnellease.SessionToken) (tunnellease.Lease, error)
	Renew(peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) (tunnellease.Lease, error)
	Release(context.Context, peer.ID, tunnellease.ID, uint64, tunnellease.SessionToken) error
}

type allocatorAuthority struct {
	allocator *tunnellease.Allocator
}

func (a allocatorAuthority) Allocate(client peer.ID, _ tunnellease.SessionToken) (tunnellease.Lease, error) {
	return a.allocator.Allocate(client)
}

func (a allocatorAuthority) Renew(client peer.ID, id tunnellease.ID, generation uint64, _ tunnellease.SessionToken) (tunnellease.Lease, error) {
	return a.allocator.Renew(client, id, generation)
}

func (a allocatorAuthority) Release(_ context.Context, client peer.ID, id tunnellease.ID, generation uint64, _ tunnellease.SessionToken) error {
	return a.allocator.ReleaseGeneration(client, id, generation)
}

type lifecycleAuthority struct{ lifecycle *tunnellease.Lifecycle }

func (a lifecycleAuthority) Allocate(client peer.ID, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	return a.lifecycle.AllocateForSession(client, token)
}
func (a lifecycleAuthority) Renew(client peer.ID, id tunnellease.ID, generation uint64, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	return a.lifecycle.RenewSession(client, id, generation, token)
}
func (a lifecycleAuthority) Release(ctx context.Context, client peer.ID, id tunnellease.ID, generation uint64, token tunnellease.SessionToken) error {
	return a.lifecycle.ReleaseSession(ctx, client, id, generation, token)
}

func NewService(host host.Host, allocator *tunnellease.Allocator, timeout time.Duration) (*Service, error) {
	if host == nil || allocator == nil {
		return nil, ErrInvalidMessage
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalidMessage
	}
	return newService(host, allocatorAuthority{allocator: allocator}, timeout)
}

// NewLifecycleService exposes atomic renewal and unified resource revocation.
func NewLifecycleService(host host.Host, lifecycle *tunnellease.Lifecycle, timeout time.Duration) (*Service, error) {
	if lifecycle == nil {
		return nil, ErrInvalidMessage
	}
	if err := lifecycle.RequireSessionTokens(); err != nil {
		return nil, err
	}
	return newService(host, lifecycleAuthority{lifecycle: lifecycle}, timeout)
}

func newService(host host.Host, authority leaseAuthority, timeout time.Duration) (*Service, error) {
	if host == nil || authority == nil {
		return nil, ErrInvalidMessage
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalidMessage
	}
	service := &Service{host: host, authority: authority, timeout: timeout}
	host.SetStreamHandler(ProtocolID, service.handle)
	return service, nil
}

func (s *Service) handle(stream network.Stream) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Reset()
		return
	}
	s.handlers.Add(1)
	s.mu.Unlock()
	defer s.handlers.Done()
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(s.timeout))
	message := new(irviov1.TunnelLeaseRequest)
	if err := readFrame(stream, message); err != nil {
		s.reject(stream, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INVALID_REQUEST)
		return
	}
	remote := stream.Conn().RemotePeer()
	id, generation, token, err := validateRequest(message, remote, s.host.ID())
	if err != nil {
		code := irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INVALID_REQUEST
		if errors.Is(err, ErrWrongPeer) {
			code = irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_WRONG_PEER
		}
		s.reject(stream, code)
		return
	}
	response := &irviov1.TunnelLeaseResponse{ProtocolVersion: ProtocolVersion}
	switch message.Operation {
	case irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_ACQUIRE:
		lease, allocateErr := s.authority.Allocate(remote, token)
		if allocateErr != nil {
			code := irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INTERNAL
			if errors.Is(allocateErr, tunnellease.ErrExhausted) {
				code = irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_EXHAUSTED
			}
			s.reject(stream, code)
			return
		}
		response.Result = &irviov1.TunnelLeaseResponse_Lease{Lease: leaseToProto(lease)}
	case irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RENEW:
		lease, renewErr := s.authority.Renew(remote, id, generation, token)
		if renewErr != nil {
			s.reject(stream, leaseErrorCode(renewErr))
			return
		}
		response.Result = &irviov1.TunnelLeaseResponse_Lease{Lease: leaseToProto(lease)}
	case irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RELEASE:
		releaseContext, releaseCancel := context.WithTimeout(context.Background(), s.timeout)
		releaseErr := s.authority.Release(releaseContext, remote, id, generation, token)
		releaseCancel()
		if releaseErr != nil {
			s.reject(stream, leaseErrorCode(releaseErr))
			return
		}
		response.Result = &irviov1.TunnelLeaseResponse_Lease{Lease: &irviov1.TunnelLease{
			LeaseId: append([]byte(nil), id[:]...), ExitPeerId: []byte(s.host.ID()), ClientPeerId: []byte(remote),
			Generation:   generation,
			SessionToken: append([]byte(nil), token[:]...),
		}}
	}
	_ = writeFrame(stream, response)
}

func leaseErrorCode(err error) irviov1.TunnelLeaseErrorCode {
	switch {
	case errors.Is(err, tunnellease.ErrWrongPeer):
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_WRONG_PEER
	case errors.Is(err, tunnellease.ErrStaleGeneration):
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE
	case errors.Is(err, tunnellease.ErrLeaseExpired):
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_EXPIRED
	case errors.Is(err, tunnellease.ErrRevocationPending):
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_REVOCATION_PENDING
	case errors.Is(err, tunnellease.ErrUnknownLease):
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_NOT_FOUND
	default:
		return irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INTERNAL
	}
}

func (s *Service) reject(stream network.Stream, code irviov1.TunnelLeaseErrorCode) {
	_ = writeFrame(stream, &irviov1.TunnelLeaseResponse{
		ProtocolVersion: ProtocolVersion,
		Result:          &irviov1.TunnelLeaseResponse_Error{Error: &irviov1.TunnelLeaseError{Code: code}},
	})
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.host.RemoveStreamHandler(ProtocolID)
		s.mu.Unlock()
		s.handlers.Wait()
	})
	return nil
}
