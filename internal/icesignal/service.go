// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

// Service serves authenticated, bounded ICE offers on a libp2p host.
type Service struct {
	host       host.Host
	local      peer.ID
	factory    SessionFactory
	authorizer Authorizer
	config     Config
	logger     *slog.Logger
	replay     *replayRegistry

	context       context.Context
	cancel        context.CancelFunc
	closed        atomic.Bool
	closeOnce     sync.Once
	handlers      sync.WaitGroup
	semaphore     chan struct{}
	accepted      chan AcceptedSession
	acceptedSlots chan struct{}
	mu            sync.Mutex
	streams       map[network.Stream]struct{}
	perPeer       map[peer.ID]int
	stats         counters
}

// NewService installs the authenticated /hydra/ice/1.0.0 handler.
func NewService(h host.Host, local peer.ID, factory SessionFactory, authorizer Authorizer, config Config) (*Service, error) {
	if h == nil || factory == nil || authorizer == nil {
		return nil, fmt.Errorf("create ICE signaling service: nil dependency")
	}
	if local == "" || h.ID() != local {
		return nil, fmt.Errorf("create ICE signaling service: local PeerID mismatch")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create ICE signaling service: %w", err)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		host: h, local: local, factory: factory, authorizer: authorizer,
		config: config, logger: logger, replay: newReplayRegistry(config.MaxRecentSessions, config.ReplayTTL),
		context: ctx, cancel: cancel, semaphore: make(chan struct{}, config.MaxConcurrentOffers),
		accepted: make(chan AcceptedSession, config.AcceptedQueue), acceptedSlots: make(chan struct{}, config.AcceptedQueue),
		streams: make(map[network.Stream]struct{}), perPeer: make(map[peer.ID]int),
	}
	h.SetStreamHandler(ProtocolID, service.handle)
	return service, nil
}

func (s *Service) handle(stream network.Stream) {
	remote := stream.Conn().RemotePeer()
	if !s.trackStream(stream) {
		_ = stream.Reset()
		return
	}
	defer func() {
		s.stats.activeHandlers.Add(^uint64(0))
		s.untrackStream(stream)
		s.handlers.Done()
	}()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		s.stats.busy.Add(1)
		s.writeRejection(stream, ErrorBusy, "signaling capacity is busy")
		return
	}
	s.stats.offersStarted.Add(1)
	deadline := time.Now().Add(s.config.StreamTimeout)
	if err := stream.SetDeadline(deadline); err != nil {
		_ = stream.Reset()
		return
	}
	ctx, cancel := context.WithDeadline(s.context, deadline)
	defer cancel()

	wireOffer := new(irviov1.IceOffer)
	if err := readFrame(stream, wireOffer); err != nil {
		s.stats.invalidMessages.Add(1)
		s.writeRejection(stream, ErrorInvalidRequest, "invalid request")
		return
	}
	if err := validateOfferWireVersion(wireOffer); err != nil {
		s.stats.invalidMessages.Add(1)
		s.writeRejection(stream, ErrorUnsupportedVersion, "unsupported protocol version")
		return
	}
	offer, err := offerFromProto(wireOffer, s.config.Validation)
	if err != nil {
		s.stats.invalidMessages.Add(1)
		s.writeRejection(stream, ErrorInvalidRequest, "invalid request")
		return
	}
	if err := validateOffer(offer, remote, s.local, time.Now(), s.config); err != nil {
		s.stats.invalidMessages.Add(1)
		code := ErrorInvalidRequest
		if errors.Is(err, ErrWrongPeer) {
			code = ErrorWrongPeer
		}
		s.writeRejection(stream, code, "offer validation failed")
		return
	}
	if err := s.authorizer.AuthorizeICE(ctx, remote, offer.Purpose); err != nil {
		s.stats.unauthorized.Add(1)
		s.writeRejection(stream, ErrorUnauthorized, "request is not authorized")
		return
	}
	if err := s.replay.claim(remote, offer.SessionID, time.Now()); err != nil {
		if errors.Is(err, ErrBusy) {
			s.stats.busy.Add(1)
			s.writeRejection(stream, ErrorBusy, "replay capacity is busy")
			return
		}
		s.stats.replays.Add(1)
		s.writeRejection(stream, ErrorSessionConflict, "session was already seen")
		return
	}
	if !s.reserveAccepted(remote) {
		s.stats.busy.Add(1)
		s.writeRejection(stream, ErrorBusy, "session capacity is busy")
		return
	}
	keepReservation := false
	defer func() {
		if !keepReservation {
			s.releaseAccepted(remote)
		}
	}()
	prepared, err := s.factory.Prepare(ctx, remote, offer.Description.Clone())
	if err != nil {
		s.writeRejection(stream, ErrorInternal, "session preparation failed")
		return
	}
	if prepared == nil {
		s.writeRejection(stream, ErrorInternal, "session preparation failed")
		return
	}
	owned := true
	defer func() {
		if owned {
			_ = prepared.Close()
		}
	}()
	localDescription, err := icesession.ValidateDescription(prepared.LocalDescription(), s.config.Validation)
	if err != nil {
		s.writeRejection(stream, ErrorInternal, "session preparation failed")
		return
	}
	now := time.Now().Truncate(time.Second)
	answer := Answer{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: s.local,
		Purpose: offer.Purpose, Role: icesession.RoleControlled, Description: localDescription,
		CreatedAt: now, ExpiresAt: now.Add(s.config.DescriptionTTL),
	}
	if err := prepared.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		LocalPeer: s.local, RemotePeer: remote, Purpose: offer.Purpose,
		LocalRole: icesession.RoleControlled, RemoteRole: offer.Role,
	}); err != nil {
		s.writeRejection(stream, ErrorInternal, "session preparation failed")
		return
	}
	response := &irviov1.IceSignalResponse{Result: &irviov1.IceSignalResponse_Answer{Answer: answerToProto(answer)}}
	if err := writeFrame(stream, response); err != nil {
		_ = stream.Reset()
		return
	}
	s.stats.answersSent.Add(1)
	accepted := AcceptedSession{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		RemotePeer: remote, Local: prepared, RemoteDescription: offer.Description.Clone(),
		Purpose: offer.Purpose, LocalRole: icesession.RoleControlled, RemoteRole: offer.Role,
	}
	select {
	case s.accepted <- accepted:
		keepReservation = true
		owned = false
		s.stats.acceptedQueued.Add(1)
		s.stats.offersSucceeded.Add(1)
		s.logger.Info("ICE_SIGNAL_ACCEPTED", "session_id", fmt.Sprintf("%x", offer.SessionID), "peer_id", remote, "candidate_count", len(offer.Description.Candidates))
	case <-s.context.Done():
		_ = stream.Reset()
		return
	}
	_ = stream.Close()
}

// NextAccepted transfers ownership of one responder PreparedSession.
func (s *Service) NextAccepted(ctx context.Context) (AcceptedSession, error) {
	if ctx == nil {
		return AcceptedSession{}, fmt.Errorf("receive accepted ICE session: nil context")
	}
	select {
	case accepted := <-s.accepted:
		s.releaseAccepted(accepted.RemotePeer)
		s.stats.acceptedHandedOff.Add(1)
		return accepted, nil
	case <-ctx.Done():
		return AcceptedSession{}, ctx.Err()
	case <-s.context.Done():
		return AcceptedSession{}, ErrClosed
	}
}

// Stats returns a race-safe service snapshot.
func (s *Service) Stats() Stats {
	return Stats{
		OffersStarted: s.stats.offersStarted.Load(), OffersSucceeded: s.stats.offersSucceeded.Load(),
		OffersRejected: s.stats.offersRejected.Load(), AnswersSent: s.stats.answersSent.Load(),
		Unauthorized: s.stats.unauthorized.Load(), Busy: s.stats.busy.Load(), Replays: s.stats.replays.Load(),
		InvalidMessages: s.stats.invalidMessages.Load(), AcceptedQueued: s.stats.acceptedQueued.Load(),
		AcceptedHandedOff: s.stats.acceptedHandedOff.Load(), ActiveHandlers: s.stats.activeHandlers.Load(),
		RecentSessions: uint64(s.replay.size()),
	}
}

// Close stops the handler and closes only responder sessions not handed off.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.host.RemoveStreamHandler(ProtocolID)
		s.cancel()
		s.mu.Lock()
		streams := make([]network.Stream, 0, len(s.streams))
		for stream := range s.streams {
			streams = append(streams, stream)
		}
		s.mu.Unlock()
		for _, stream := range streams {
			_ = stream.Reset()
		}
		s.handlers.Wait()
		for {
			select {
			case accepted := <-s.accepted:
				s.releaseAccepted(accepted.RemotePeer)
				_ = accepted.Local.Close()
			default:
				return
			}
		}
	})
	return nil
}

func (s *Service) trackStream(stream network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return false
	}
	s.streams[stream] = struct{}{}
	s.handlers.Add(1)
	s.stats.activeHandlers.Add(1)
	return true
}

func (s *Service) untrackStream(stream network.Stream) {
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
}

func (s *Service) reserveAccepted(remote peer.ID) bool {
	select {
	case s.acceptedSlots <- struct{}{}:
	default:
		return false
	}
	s.mu.Lock()
	if s.perPeer[remote] >= s.config.MaxSessionsPerPeer {
		s.mu.Unlock()
		<-s.acceptedSlots
		return false
	}
	s.perPeer[remote]++
	s.mu.Unlock()
	return true
}

func (s *Service) releaseAccepted(remote peer.ID) {
	s.mu.Lock()
	if s.perPeer[remote] > 1 {
		s.perPeer[remote]--
	} else {
		delete(s.perPeer, remote)
	}
	s.mu.Unlock()
	select {
	case <-s.acceptedSlots:
	default:
	}
}

func (s *Service) writeRejection(stream network.Stream, code ErrorCode, message string) {
	s.stats.offersRejected.Add(1)
	_ = stream.SetDeadline(time.Now().Add(s.config.StreamTimeout))
	if len(message) > maxErrorMessageBytes {
		message = message[:maxErrorMessageBytes]
	}
	response := &irviov1.IceSignalResponse{Result: &irviov1.IceSignalResponse_Error{Error: &irviov1.IceSignalError{Code: errorCodeToProto(code), Message: message}}}
	if err := writeFrame(stream, response); err != nil {
		_ = stream.Reset()
		return
	}
	_ = stream.Close()
}
