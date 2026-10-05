// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/tunnelsignal"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type replayEntry struct {
	digest   [sha256.Size]byte
	response *irviov1.SessionControlResponse
}

type Server struct {
	listener  net.Listener
	noise     *noise.SessionTransport
	local     peer.ID
	authority Authority
	timeout   time.Duration
	maxReplay int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	closed   bool
	replays  map[string]replayEntry
	order    []string
	handlers sync.WaitGroup
}

func NewServer(listener net.Listener, privateKey crypto.PrivKey, authority Authority, timeout time.Duration) (*Server, error) {
	if listener == nil || authority == nil {
		return nil, ErrInvalid
	}
	transport, err := noiseTransport(privateKey)
	if err != nil {
		return nil, err
	}
	local, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{
		listener: listener, noise: transport, local: local, authority: authority,
		timeout: timeout, maxReplay: DefaultReplayEntries, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), replays: make(map[string]replayEntry),
	}
	go server.accept()
	return server, nil
}

func (s *Server) accept() {
	defer close(s.done)
	for {
		raw, err := s.listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			continue
		}
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			s.handle(raw)
		}()
	}
}

func (s *Server) handle(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(s.timeout))
	secure, err := s.noise.SecureInbound(s.ctx, raw, "")
	if err != nil {
		return
	}
	defer secure.Close()
	remote := secure.RemotePeer()
	if remote == "" || remote == s.local {
		return
	}
	var bound tunnellease.Lease
	for {
		_ = secure.SetDeadline(time.Now().Add(s.timeout))
		frame, payload, err := readFrame(secure)
		if err != nil {
			return
		}
		request := frame.GetRequest()
		if request == nil {
			return
		}
		if bound.ID.IsZero() {
			if request.Operation != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BIND {
				_ = writeFrame(secure, responseErrorFrame(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INVALID_REQUEST))
				return
			}
			lease, err := s.validateBound(remote, request)
			if err != nil {
				_ = writeFrame(secure, responseErrorFrame(request, leaseErrorCode(err)))
				return
			}
			bound = lease
			if err := writeFrame(secure, responseFrame(responseLease(
				request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_BOUND, lease, s.remaining(lease),
			))); err != nil {
				return
			}
			continue
		}
		var response *irviov1.SessionControlResponse
		if request.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE {
			response, err = s.rotationExchange(secure, remote, bound, request, payload)
			if err != nil {
				return
			}
		} else {
			response = s.process(remote, bound, request, payload)
		}
		if response.GetLease() != nil {
			if lease, err := tunnelsignal.LeaseFromProto(response.GetLease(), s.local, remote, time.Now().UTC()); err == nil {
				bound = lease
			}
		}
		releaseAccepted := request.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE &&
			response.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE_ACCEPTED
		writeErr := writeFrame(secure, &irviov1.SessionControlFrame{Message: &irviov1.SessionControlFrame_Response{Response: response}})
		if releaseAccepted {
			// RELEASE_ACCEPTED fences intent; revoke after client close or deadline so cleanup cannot swallow the response.
			if writeErr == nil {
				var terminal [1]byte
				_ = secure.SetReadDeadline(time.Now().Add(s.timeout))
				_, _ = secure.Read(terminal[:])
			}
			releaseContext, cancel := context.WithTimeout(s.ctx, s.timeout)
			_ = s.authority.Release(releaseContext, remote, bound.ID, bound.Generation, bound.Session)
			cancel()
			return
		}
		if writeErr != nil {
			return
		}
	}
}

// rotationExchange keeps old routes through READY/COMMIT, then switches keys and replies over fresh AWG.
func (s *Server) rotationExchange(secure net.Conn, remote peer.ID, bound tunnellease.Lease, request *irviov1.SessionControlRequest, payload []byte) (*irviov1.SessionControlResponse, error) {
	id, token, err := validateRequestOwnership(request)
	if err != nil || id != bound.ID || token != bound.Session {
		return responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_WRONG_PEER), nil
	}
	key := remote.String() + "/" + string(request.RequestId)
	wireDigest := requestDigest(payload)
	s.mu.Lock()
	if cached, exists := s.replays[key]; exists {
		s.mu.Unlock()
		if cached.digest != wireDigest {
			return responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE), nil
		}
		return cloneResponse(cached.response), nil
	}
	s.mu.Unlock()
	rotation, ok := s.authority.(RotationAuthority)
	if !ok {
		return s.rememberRotationError(key, wireDigest, request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INVALID_REQUEST), nil
	}
	var bindingDigest [sha256.Size]byte
	copy(bindingDigest[:], request.AwgBindingDigest)
	if err := rotation.ValidateAWGRotation(remote, id, request.ExpectedLeaseRevision, token, request.AwgKeyEpoch, bindingDigest); err != nil {
		return s.rememberRotationError(key, wireDigest, request, leaseErrorCode(err)), nil
	}
	ready := responseLease(request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_READY, bound, s.remaining(bound))
	if err := writeFrame(secure, responseFrame(ready)); err != nil {
		return nil, err
	}
	frame, _, err := readFrame(secure)
	if err != nil {
		return nil, err
	}
	commit := frame.GetRequest()
	if !matchingRotationCommit(request, commit) {
		return responseErrorResponse(commit, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, exists := s.replays[key]; exists {
		if cached.digest != wireDigest {
			return responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE), nil
		}
		return cloneResponse(cached.response), nil
	}
	rotateContext, cancel := context.WithTimeout(s.ctx, s.timeout)
	lease, rotateErr := rotation.CommitAWGRotation(rotateContext, remote, id, request.ExpectedLeaseRevision, token, request.AwgKeyEpoch, bindingDigest)
	cancel()
	var response *irviov1.SessionControlResponse
	if rotateErr != nil {
		response = responseErrorResponse(request, leaseErrorCode(rotateErr))
	} else {
		response = responseLease(request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED, lease, s.remaining(lease))
	}
	s.rememberLocked(key, wireDigest, response)
	return response, nil
}

func (s *Server) rememberRotationError(key string, digest [sha256.Size]byte, request *irviov1.SessionControlRequest, code irviov1.TunnelLeaseErrorCode) *irviov1.SessionControlResponse {
	response := responseErrorResponse(request, code)
	s.mu.Lock()
	s.rememberLocked(key, digest, response)
	s.mu.Unlock()
	return response
}

func matchingRotationCommit(initial, commit *irviov1.SessionControlRequest) bool {
	return initial != nil && commit != nil &&
		commit.ProtocolVersion == ProtocolVersion &&
		commit.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_COMMIT &&
		bytes.Equal(initial.RequestId, commit.RequestId) &&
		bytes.Equal(initial.LeaseId, commit.LeaseId) &&
		bytes.Equal(initial.SessionToken, commit.SessionToken) &&
		initial.ExpectedLeaseRevision == commit.ExpectedLeaseRevision &&
		initial.AwgKeyEpoch == commit.AwgKeyEpoch &&
		bytes.Equal(initial.AwgBindingDigest, commit.AwgBindingDigest)
}

func (s *Server) validateBound(remote peer.ID, request *irviov1.SessionControlRequest) (tunnellease.Lease, error) {
	id, token, err := validateRequestOwnership(request)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	return s.authority.Lookup(remote, id, request.ExpectedLeaseRevision, token)
}

func (s *Server) process(remote peer.ID, bound tunnellease.Lease, request *irviov1.SessionControlRequest, payload []byte) *irviov1.SessionControlResponse {
	id, token, err := validateRequestOwnership(request)
	if err != nil || id != bound.ID || token != bound.Session {
		return responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_WRONG_PEER)
	}
	key := remote.String() + "/" + string(request.RequestId)
	digest := requestDigest(payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, exists := s.replays[key]; exists {
		if cached.digest != digest {
			return responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE)
		}
		return cloneResponse(cached.response)
	}

	var response *irviov1.SessionControlResponse
	switch request.Operation {
	case irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEW:
		lease, renewErr := s.authority.Renew(remote, id, request.ExpectedLeaseRevision, token)
		if renewErr != nil {
			response = responseErrorResponse(request, leaseErrorCode(renewErr))
		} else {
			response = responseLease(request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEWED, lease, s.remaining(lease))
			if rotation, ok := s.authority.(RotationAuthority); ok {
				proposal, proposalErr := rotation.RotationProposal(remote, id, lease.Generation, token)
				if proposalErr == nil {
					if proposal.KeyEpoch == 0 || len(proposal.SignedBinding) == 0 || len(proposal.SignedBinding) > MaxSignedAWGBinding {
						response = responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INTERNAL)
					} else {
						response.SignedAwgBinding = append([]byte(nil), proposal.SignedBinding...)
						response.AwgKeyEpoch = proposal.KeyEpoch
					}
				}
			}
		}
	case irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE_QUERY:
		lease, lookupErr := s.authority.Lookup(remote, id, request.ExpectedLeaseRevision, token)
		if lookupErr != nil {
			response = responseErrorResponse(request, leaseErrorCode(lookupErr))
		} else {
			response = responseLease(request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE, lease, s.remaining(lease))
		}
	case irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE:
		lease, lookupErr := s.authority.Lookup(remote, id, request.ExpectedLeaseRevision, token)
		if lookupErr != nil {
			response = responseErrorResponse(request, leaseErrorCode(lookupErr))
		} else {
			response = responseLease(request, irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE_ACCEPTED, lease, 0)
		}
	default:
		response = responseErrorResponse(request, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_INVALID_REQUEST)
	}
	s.rememberLocked(key, digest, response)
	return response
}

func (s *Server) rememberLocked(key string, digest [sha256.Size]byte, response *irviov1.SessionControlResponse) {
	if _, exists := s.replays[key]; exists {
		return
	}
	if len(s.order) >= s.maxReplay {
		delete(s.replays, s.order[0])
		copy(s.order, s.order[1:])
		s.order = s.order[:len(s.order)-1]
	}
	s.order = append(s.order, key)
	s.replays[key] = replayEntry{digest: digest, response: cloneResponse(response)}
}

func (s *Server) remaining(lease tunnellease.Lease) int64 {
	value := time.Until(lease.ValidUntil)
	if value < 0 {
		return 0
	}
	return int64(value)
}

func validateRequestOwnership(request *irviov1.SessionControlRequest) (tunnellease.ID, tunnellease.SessionToken, error) {
	var id tunnellease.ID
	var token tunnellease.SessionToken
	if request == nil || request.ProtocolVersion != ProtocolVersion || len(request.RequestId) != RequestIDSize ||
		len(request.LeaseId) != len(id) || len(request.SessionToken) != len(token) ||
		request.ExpectedLeaseRevision == 0 {
		return id, token, ErrInvalid
	}
	rotation := request.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE ||
		request.Operation == irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_COMMIT
	if rotation != (len(request.AwgBindingDigest) != 0 || request.AwgKeyEpoch != 0) ||
		rotation && (len(request.AwgBindingDigest) != sha256.Size || request.AwgKeyEpoch == 0) {
		return id, token, ErrInvalid
	}
	copy(id[:], request.LeaseId)
	copy(token[:], request.SessionToken)
	if id.IsZero() || token.IsZero() {
		return id, token, ErrInvalid
	}
	return id, token, nil
}

func responseLease(request *irviov1.SessionControlRequest, operation irviov1.SessionControlOperation, lease tunnellease.Lease, remaining int64) *irviov1.SessionControlResponse {
	return &irviov1.SessionControlResponse{
		ProtocolVersion: ProtocolVersion, Operation: operation,
		RequestId: append([]byte(nil), request.RequestId...),
		Lease:     tunnelsignal.LeaseToProto(lease), AuthoritativeRemainingNanos: remaining,
	}
}

func responseErrorResponse(request *irviov1.SessionControlRequest, code irviov1.TunnelLeaseErrorCode) *irviov1.SessionControlResponse {
	response := &irviov1.SessionControlResponse{
		ProtocolVersion: ProtocolVersion,
		Operation:       irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_ERROR,
		Error:           &irviov1.TunnelLeaseError{Code: code},
	}
	if request != nil {
		response.RequestId = append([]byte(nil), request.RequestId...)
	}
	return response
}

func responseErrorFrame(request *irviov1.SessionControlRequest, code irviov1.TunnelLeaseErrorCode) *irviov1.SessionControlFrame {
	return responseFrame(responseErrorResponse(request, code))
}

func responseFrame(response *irviov1.SessionControlResponse) *irviov1.SessionControlFrame {
	return &irviov1.SessionControlFrame{Message: &irviov1.SessionControlFrame_Response{Response: response}}
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

func cloneResponse(value *irviov1.SessionControlResponse) *irviov1.SessionControlResponse {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*irviov1.SessionControlResponse)
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	err := s.listener.Close()
	<-s.done
	s.handlers.Wait()
	return err
}
