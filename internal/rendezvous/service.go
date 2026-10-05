// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type SignalForwarder interface {
	ForwardSignal(context.Context, OwnerRoute, *irviov1.RendezvousSignal) (*irviov1.RendezvousSignal, error)
}

type RemoteSampler interface {
	SampleRemote(context.Context, peer.ID, string, int, []peer.ID) ([]Candidate, bool, error)
}

type SampleAuthorizer func(context.Context, peer.ID) error

type ServiceOptions struct {
	Host            host.Host
	Registry        *Registry
	Forwarder       SignalForwarder
	RemoteSampler   RemoteSampler
	AuthorizeSample SampleAuthorizer
	Logger          *slog.Logger
	MaxHandlers     int
	StreamTimeout   time.Duration
}

type Service struct {
	host            host.Host
	registry        *Registry
	forwarder       SignalForwarder
	sampler         RemoteSampler
	authorizeSample SampleAuthorizer
	logger          *slog.Logger
	timeout         time.Duration
	semaphore       chan struct{}
	admission       *requestAdmission

	context context.Context
	cancel  context.CancelFunc
	once    sync.Once
	wg      sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	streams  map[network.Stream]struct{}
	channels map[peer.ID]*registrationChannel
	pending  map[RequestID]chan *irviov1.RendezvousSignal
}

type registrationChannel struct {
	stream network.Stream
	owner  Owner
	write  sync.Mutex
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Host == nil || options.Registry == nil {
		return nil, fmt.Errorf("%w: service dependencies", ErrInvalid)
	}
	if options.MaxHandlers == 0 {
		options.MaxHandlers = 512
	}
	if options.StreamTimeout == 0 {
		options.StreamTimeout = 20 * time.Second
	}
	if options.MaxHandlers < 1 || options.MaxHandlers > 65_536 ||
		options.StreamTimeout <= 0 || options.StreamTimeout > time.Minute {
		return nil, fmt.Errorf("%w: service limits", ErrInvalid)
	}
	ctx, cancel := context.WithCancel(context.Background())
	admission, err := newRequestAdmission(options.Registry.config.effectiveAdmissionPolicy(), options.Registry.config.Now)
	if err != nil {
		cancel()
		return nil, err
	}
	service := &Service{
		host: options.Host, registry: options.Registry, forwarder: options.Forwarder, sampler: options.RemoteSampler,
		authorizeSample: options.AuthorizeSample,
		logger:          options.Logger,
		timeout:         options.StreamTimeout, semaphore: make(chan struct{}, options.MaxHandlers), admission: admission,
		context: ctx, cancel: cancel, streams: make(map[network.Stream]struct{}),
		channels: make(map[peer.ID]*registrationChannel),
		pending:  make(map[RequestID]chan *irviov1.RendezvousSignal),
	}
	options.Host.SetStreamHandler(libprotocol.ID(ProtocolID), service.handle)
	options.Host.SetStreamHandler(libprotocol.ID(AvailableCountriesProtocolID), service.handleAvailableCountries)
	return service, nil
}

func (s *Service) handle(stream network.Stream) {
	if !s.track(stream) {
		_ = stream.Reset()
		return
	}
	defer func() {
		s.untrack(stream)
		s.wg.Done()
	}()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_BUSY, "service busy")
		_ = stream.Close()
		return
	}
	_ = stream.SetDeadline(time.Now().Add(s.timeout))
	frame, err := ReadFrame(stream)
	if err != nil {
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid frame")
		_ = stream.Reset()
		return
	}
	operation := admitSignal
	switch frame.Message.(type) {
	case *irviov1.RendezvousFrame_Register:
		operation = admitRegister
	case *irviov1.RendezvousFrame_GetCandidates:
		operation = admitGET
	case *irviov1.RendezvousFrame_SampleLocal:
		operation = admitSample
	}
	if !s.admission.Allow(stream.Conn().RemotePeer(), stream.Conn().RemoteMultiaddr(), operation) {
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RATE_LIMITED, "request rate limited")
		_ = stream.Close()
		return
	}
	switch message := frame.Message.(type) {
	case *irviov1.RendezvousFrame_Register:
		s.handleRegistration(stream, message.Register)
	case *irviov1.RendezvousFrame_GetCandidates:
		s.handleGet(stream, message.GetCandidates)
	case *irviov1.RendezvousFrame_SampleLocal:
		s.handleSampleLocal(stream, message.SampleLocal)
	case *irviov1.RendezvousFrame_Signal:
		s.handleSignal(stream, message.Signal)
	default:
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "unexpected first message")
		_ = stream.Close()
	}
}

func (s *Service) handleAvailableCountries(stream network.Stream) {
	if !s.track(stream) {
		_ = stream.Reset()
		return
	}
	defer func() {
		s.untrack(stream)
		s.wg.Done()
	}()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		_ = stream.Reset()
		return
	}
	_ = stream.SetDeadline(time.Now().Add(s.timeout))
	frame, err := readAvailableCountriesFrame(stream)
	if err != nil {
		_ = stream.Reset()
		return
	}
	request, ok := frame.Message.(*irviov1.AvailableCountriesFrame_Request)
	if !ok || request.Request.GetProtocolVersion() != ProtocolVersion ||
		!s.admission.Allow(stream.Conn().RemotePeer(), stream.Conn().RemoteMultiaddr(), admitGET) {
		_ = stream.Reset()
		return
	}
	countries := s.registry.CountriesExcluding(stream.Conn().RemotePeer())
	_ = writeAvailableCountriesFrame(stream, &irviov1.AvailableCountriesFrame{
		Message: &irviov1.AvailableCountriesFrame_Response{Response: &irviov1.ListLocalAvailableCountriesResponse{
			ProtocolVersion: ProtocolVersion,
			CountryCodes:    countries,
		}},
	})
	_ = stream.Close()
}

func (s *Service) handleRegistration(stream network.Stream, message *irviov1.RendezvousRegister) {
	remote := stream.Conn().RemotePeer()
	id, err := registrationID(message.GetRegistrationId())
	if err != nil || message.GetProtocolVersion() != ProtocolVersion || len(message.GetSignedAnnouncement()) == 0 {
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid registration")
		_ = stream.Reset()
		return
	}
	observed, err := observedStreamAddress(stream.Conn().RemoteMultiaddr(), s.registry.config.ObservedIPPolicy)
	if err != nil {
		s.writeError(stream, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid observed address")
		_ = stream.Reset()
		return
	}
	token, err := NewChannelToken(nil)
	if err != nil {
		_ = stream.Reset()
		return
	}
	registration, err := s.registry.Register(s.context, remote, id, token, message.SignedAnnouncement, observed)
	if err != nil {
		s.writeRegistryError(stream, nil, err)
		_ = stream.Close()
		return
	}
	if s.logger != nil {
		s.logger.Info("RENDEZVOUS_REGISTRATION_ACCEPTED",
			"country", registration.Country.Country,
			"country_state", registration.Country.State,
			"address_family", registration.Country.Family,
			"fresh_registrations", s.registry.Snapshot().Fresh,
		)
	}
	channel := &registrationChannel{stream: stream, owner: registration.Owner}
	previous := s.installChannel(remote, channel)
	if previous != nil {
		_ = previous.stream.Reset()
	}
	defer func() {
		s.removeChannel(remote, channel)
		s.registry.ChannelClosed(channel.owner)
	}()
	if err := channel.writeFrame(registeredFrame(registration, s.registry.config)); err != nil {
		_ = stream.Reset()
		return
	}
	_ = stream.SetDeadline(time.Time{})
	for {
		_ = stream.SetReadDeadline(time.Now().Add(s.registry.config.RegistrationExpiry))
		frame, err := ReadFrame(stream)
		if err != nil {
			_ = stream.Reset()
			return
		}
		switch value := frame.Message.(type) {
		case *irviov1.RendezvousFrame_Heartbeat:
			if !s.admission.Allow(remote, stream.Conn().RemoteMultiaddr(), admitHeartbeat) {
				s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RATE_LIMITED, "heartbeat rate limited")
				continue
			}
			heartbeat := value.Heartbeat
			if heartbeat.GetProtocolVersion() != ProtocolVersion ||
				heartbeat.GetServerIncarnation() != registration.Owner.ServerIncarnation ||
				!equalRegistrationID(heartbeat.GetRegistrationId(), registration.Owner.RegistrationID) {
				s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_STALE, "stale registration owner")
				continue
			}
			previousReceiptSequence := registration.ReceiptSequence
			current, err := s.registry.Heartbeat(registration.Owner, heartbeat.Sequence)
			if err != nil {
				s.writeRegistryErrorChannel(channel, nil, err)
				continue
			}
			registration = current
			var renewedReceipt []byte
			if current.ReceiptSequence > previousReceiptSequence {
				renewedReceipt = append([]byte(nil), current.SignedReceipt.SignedEnvelope...)
			}
			_ = channel.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_HeartbeatAck{
				HeartbeatAck: &irviov1.RendezvousHeartbeatAck{
					ProtocolVersion: ProtocolVersion, ServerIncarnation: current.Owner.ServerIncarnation,
					RegistrationId: append([]byte(nil), current.Owner.RegistrationID[:]...),
					Sequence:       heartbeat.Sequence, AcceptedAtUnix: s.registry.config.Now().Unix(),
					SignedReceipt: renewedReceipt,
				},
			}})
		case *irviov1.RendezvousFrame_Update:
			if !s.admission.Allow(remote, stream.Conn().RemoteMultiaddr(), admitRegister) {
				s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RATE_LIMITED, "update rate limited")
				continue
			}
			update := value.Update
			if update.GetProtocolVersion() != ProtocolVersion ||
				update.GetServerIncarnation() != registration.Owner.ServerIncarnation ||
				!equalRegistrationID(update.GetRegistrationId(), registration.Owner.RegistrationID) {
				s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_STALE, "stale registration owner")
				continue
			}
			current, err := s.registry.Update(s.context, registration.Owner, update.SignedAnnouncement)
			if err != nil {
				s.writeRegistryErrorChannel(channel, nil, err)
				continue
			}
			registration = current
			_ = channel.writeFrame(registeredFrame(current, s.registry.config))
		case *irviov1.RendezvousFrame_Unregister:
			unregister := value.Unregister
			if unregister.GetProtocolVersion() != ProtocolVersion ||
				unregister.GetServerIncarnation() != registration.Owner.ServerIncarnation ||
				!equalRegistrationID(unregister.GetRegistrationId(), registration.Owner.RegistrationID) {
				s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_STALE, "stale registration owner")
				continue
			}
			_ = s.registry.Unregister(registration.Owner)
			_ = stream.Close()
			return
		case *irviov1.RendezvousFrame_Signal:
			if !value.Signal.GetResponse() || !s.deliverResponse(value.Signal) {
				s.writeErrorChannel(channel, value.Signal.GetRequestHandle(), irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND, "unknown signal request")
			}
		default:
			s.writeErrorChannel(channel, nil, irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid registration message")
		}
	}
}

func (s *Service) handleGet(stream network.Stream, message *irviov1.RendezvousGetCandidates) {
	requestID, excluded, err := validateCandidateRequest(message.GetProtocolVersion(), message.GetCountry(), message.GetLimit(), message.GetExcludePeerIds(), message.GetRequestId())
	if err != nil {
		s.writeError(stream, message.GetRequestId(), irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid candidate request")
		_ = stream.Close()
		return
	}
	candidates, err := s.registry.Sample(message.Country, int(message.Limit), excluded)
	if err != nil {
		s.writeRegistryError(stream, requestID[:], err)
		_ = stream.Close()
		return
	}
	localCount := len(candidates)
	remoteCount := 0
	var remoteErr error
	partial := false
	if s.sampler != nil {
		ctx, cancel := context.WithTimeout(s.context, s.timeout)
		defer cancel()
		exclude := make([]peer.ID, 0, len(excluded)+len(candidates))
		for value := range excluded {
			exclude = append(exclude, value)
		}
		for _, candidate := range candidates {
			exclude = append(exclude, candidate.Descriptor.Subject)
		}
		remote, remotePartial, sampleErr := s.sampler.SampleRemote(
			ctx, stream.Conn().RemotePeer(), message.Country, int(message.Limit), exclude,
		)
		remoteErr = sampleErr
		remoteCount = len(remote)
		partial = remotePartial || remoteErr != nil
		if merged, mergeErr := MergeCandidates(append(candidates, remote...)...); mergeErr == nil {
			candidates = merged
		} else {
			partial = true
		}
	}
	if err := shuffleCandidates(s.registry.random, candidates); err != nil {
		s.writeRegistryError(stream, requestID[:], err)
		_ = stream.Close()
		return
	}
	available := len(candidates)
	if len(candidates) > int(message.Limit) {
		candidates = candidates[:message.Limit]
	}
	wire, fitPartial := fitCandidates(candidates, int(message.Limit), false)
	partial = partial || fitPartial
	if s.logger != nil {
		s.logger.Info("RENDEZVOUS_CANDIDATES_SERVED",
			"country", message.Country, "local", localCount, "remote", remoteCount,
			"returned", len(wire), "partial", partial, "remote_error", remoteErr,
		)
	}
	response := &irviov1.RendezvousCandidates{
		ProtocolVersion: ProtocolVersion, Country: message.Country,
		AvailablePeerCount: uint64(available), Candidates: wire, Partial: partial,
		RequestId: append([]byte(nil), requestID[:]...),
	}
	_ = WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Candidates_{Candidates_: response}})
	_ = stream.Close()
}

func (s *Service) handleSampleLocal(stream network.Stream, message *irviov1.RendezvousSampleLocal) {
	if s.authorizeSample == nil || s.authorizeSample(s.context, stream.Conn().RemotePeer()) != nil {
		s.writeError(stream, message.GetRequestId(), irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNAUTHORIZED, "sample requester is not a verified Rendezvous")
		_ = stream.Close()
		return
	}
	requestID, excluded, err := validateCandidateRequest(message.GetProtocolVersion(), message.GetCountry(), message.GetLimit(), message.GetExcludePeerIds(), message.GetRequestId())
	if err != nil {
		s.writeError(stream, message.GetRequestId(), irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid sample request")
		_ = stream.Close()
		return
	}
	candidates, err := s.registry.Sample(message.Country, int(message.Limit), excluded)
	if err != nil {
		s.writeRegistryError(stream, requestID[:], err)
		_ = stream.Close()
		return
	}
	wire, partial := fitCandidates(candidates, int(message.Limit), true)
	if s.logger != nil {
		s.logger.Info("RENDEZVOUS_LOCAL_SAMPLE_SERVED",
			"country", message.Country, "returned", len(wire), "partial", partial,
		)
	}
	response := &irviov1.RendezvousSampleResult{
		ProtocolVersion: ProtocolVersion, Country: message.Country, Candidates: wire,
		Partial: partial, RequestId: append([]byte(nil), requestID[:]...),
	}
	_ = WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_SampleResult{SampleResult: response}})
	_ = stream.Close()
}

func (s *Service) handleSignal(stream network.Stream, signal *irviov1.RendezvousSignal) {
	requestID, owner, deadline, err := validateSignal(signal, s.registry.config.Now())
	if err != nil || signal.Response {
		s.writeError(stream, signal.GetRequestHandle(), irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid signal")
		_ = stream.Close()
		return
	}
	contextDeadline := deadline
	if maximum := time.Now().Add(s.registry.config.PendingTimeout); contextDeadline.After(maximum) {
		contextDeadline = maximum
	}
	ctx, cancel := context.WithDeadline(s.context, contextDeadline)
	defer cancel()

	if owner.ServerPeer != s.host.ID() {
		if signal.HopCount != 0 || s.forwarder == nil {
			s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_ROUTE_FAILED, "owner route unavailable")
			_ = stream.Close()
			return
		}
		forwarded := proto.Clone(signal).(*irviov1.RendezvousSignal)
		forwarded.HopCount = 1
		response, err := s.forwarder.ForwardSignal(ctx, owner, forwarded)
		if err != nil {
			s.writeRegistryError(stream, requestID[:], ErrRouteFailed)
		} else {
			_ = WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Signal{Signal: response}})
		}
		_ = stream.Close()
		return
	}
	target, err := peer.IDFromBytes(signal.TargetPeerId)
	if err != nil {
		s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, "invalid target")
		_ = stream.Close()
		return
	}
	registration, exists := s.registry.Lookup(target)
	channel := s.channelFor(target)
	if !exists || channel == nil || registration.Owner != channel.owner ||
		registration.Owner.ServerIncarnation != owner.ServerIncarnation ||
		registration.Owner.RegistrationID != owner.RegistrationID {
		s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND, "registration unavailable")
		_ = stream.Close()
		return
	}
	responseChannel, ok := s.reservePending(requestID)
	if !ok {
		s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_BUSY, "signaling busy")
		_ = stream.Close()
		return
	}
	defer s.releasePending(requestID)
	if err := channel.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Signal{Signal: signal}}); err != nil {
		s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_ROUTE_FAILED, "delivery failed")
		_ = stream.Close()
		return
	}
	select {
	case response := <-responseChannel:
		_ = WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Signal{Signal: response}})
	case <-ctx.Done():
		s.writeError(stream, requestID[:], irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_TIMEOUT, "signaling timeout")
	}
	_ = stream.Close()
}

func validateCandidateRequest(version uint32, country string, limit uint32, excluded [][]byte, request []byte) (RequestID, map[peer.ID]struct{}, error) {
	id, err := requestID(request)
	if err != nil || version != ProtocolVersion || !validCountry(country) || limit == 0 || limit > MaxCandidates || len(excluded) > MaxExcludedPeers {
		return RequestID{}, nil, ErrInvalid
	}
	result := make(map[peer.ID]struct{}, len(excluded))
	for _, value := range excluded {
		peerID, err := peer.IDFromBytes(value)
		if err != nil {
			return RequestID{}, nil, ErrInvalid
		}
		result[peerID] = struct{}{}
	}
	return id, result, nil
}

func validateSignal(signal *irviov1.RendezvousSignal, now time.Time) (RequestID, OwnerRoute, time.Time, error) {
	if signal == nil || signal.ProtocolVersion != ProtocolVersion || len(signal.SignedMessage) == 0 ||
		len(signal.SignedMessage) > MaxSignalingBytes || signal.HopCount > 1 {
		return RequestID{}, OwnerRoute{}, time.Time{}, ErrInvalid
	}
	id, err := requestID(signal.RequestHandle)
	if err != nil {
		return RequestID{}, OwnerRoute{}, time.Time{}, err
	}
	owner, err := ownerRouteFromProto(signal.OwnerRoute)
	if err != nil {
		return RequestID{}, OwnerRoute{}, time.Time{}, err
	}
	deadline := time.UnixMilli(signal.DeadlineUnixMilli)
	if !deadline.After(now) || deadline.After(now.Add(20*time.Second)) {
		return RequestID{}, OwnerRoute{}, time.Time{}, ErrTimeout
	}
	return id, owner, deadline, nil
}

func fitCandidates(candidates []Candidate, limit int, sample bool) ([]*irviov1.BrokeredCandidate, bool) {
	result := make([]*irviov1.BrokeredCandidate, 0, min(limit, len(candidates)))
	partial := false
	for _, candidate := range candidates {
		wire, err := CandidateToProto(candidate)
		if err != nil {
			partial = true
			continue
		}
		trial := append(append([]*irviov1.BrokeredCandidate(nil), result...), wire)
		var frame *irviov1.RendezvousFrame
		if sample {
			frame = &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_SampleResult{SampleResult: &irviov1.RendezvousSampleResult{ProtocolVersion: ProtocolVersion, Candidates: trial}}}
		} else {
			frame = &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Candidates_{Candidates_: &irviov1.RendezvousCandidates{ProtocolVersion: ProtocolVersion, Candidates: trial}}}
		}
		if proto.Size(frame) > MaxFrameSize {
			partial = true
			break
		}
		result = trial
		if len(result) == limit {
			break
		}
	}
	return result, partial || len(result) < min(limit, len(candidates))
}

func registeredFrame(registration Registration, config Config) *irviov1.RendezvousFrame {
	now := config.Now()
	return &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Registered{Registered: &irviov1.RendezvousRegistered{
		ProtocolVersion: ProtocolVersion, ServerIncarnation: registration.Owner.ServerIncarnation,
		RegistrationId:   append([]byte(nil), registration.Owner.RegistrationID[:]...),
		SignedReceipt:    append([]byte(nil), registration.SignedReceipt.SignedEnvelope...),
		StaleAfterUnix:   now.Add(config.RegistrationStale).Unix(),
		ExpiresAfterUnix: now.Add(config.RegistrationExpiry).Unix(),
	}}}
}

func registrationID(payload []byte) (RegistrationID, error) {
	var value RegistrationID
	if len(payload) != len(value) {
		return value, ErrInvalid
	}
	copy(value[:], payload)
	if value == (RegistrationID{}) {
		return value, ErrInvalid
	}
	return value, nil
}

func requestID(payload []byte) (RequestID, error) {
	var value RequestID
	if len(payload) != len(value) {
		return value, ErrInvalid
	}
	copy(value[:], payload)
	if value == (RequestID{}) {
		return value, ErrInvalid
	}
	return value, nil
}

func equalRegistrationID(payload []byte, expected RegistrationID) bool {
	value, err := registrationID(payload)
	return err == nil && value == expected
}

func observedStreamAddress(address ma.Multiaddr, policy ObservedIPPolicy) (netip.Addr, error) {
	for _, code := range []int{ma.P_IP4, ma.P_IP6} {
		value, err := address.ValueForProtocol(code)
		if err != nil {
			continue
		}
		parsed, err := netip.ParseAddr(value)
		if err == nil && validObservedAddress(parsed, policy) {
			return parsed, nil
		}
	}
	return netip.Addr{}, ErrInvalid
}

func (channel *registrationChannel) writeFrame(frame *irviov1.RendezvousFrame) error {
	channel.write.Lock()
	defer channel.write.Unlock()
	return WriteFrame(channel.stream, frame)
}

func (s *Service) track(stream network.Stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.streams[stream] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Service) untrack(stream network.Stream) {
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
}

func (s *Service) installChannel(remote peer.ID, channel *registrationChannel) *registrationChannel {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.channels[remote]
	s.channels[remote] = channel
	return previous
}

func (s *Service) removeChannel(remote peer.ID, channel *registrationChannel) {
	s.mu.Lock()
	if s.channels[remote] == channel {
		delete(s.channels, remote)
	}
	s.mu.Unlock()
}

func (s *Service) channelFor(remote peer.ID) *registrationChannel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channels[remote]
}

func (s *Service) reservePending(id RequestID) (chan *irviov1.RendezvousSignal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.pending) >= s.registry.config.MaxPendingSignaling {
		return nil, false
	}
	if _, exists := s.pending[id]; exists {
		return nil, false
	}
	channel := make(chan *irviov1.RendezvousSignal, 1)
	s.pending[id] = channel
	return channel, true
}

func (s *Service) releasePending(id RequestID) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Service) deliverResponse(signal *irviov1.RendezvousSignal) bool {
	id, err := requestID(signal.GetRequestHandle())
	if err != nil {
		return false
	}
	s.mu.Lock()
	channel := s.pending[id]
	s.mu.Unlock()
	if channel == nil {
		return false
	}
	select {
	case channel <- proto.Clone(signal).(*irviov1.RendezvousSignal):
		return true
	default:
		return false
	}
}

func (s *Service) writeError(stream network.Stream, request []byte, code irviov1.RendezvousErrorCode, message string) {
	if len(message) > MaxErrorMessageBytes {
		message = message[:MaxErrorMessageBytes]
	}
	_ = WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Error{Error: &irviov1.RendezvousError{
		ProtocolVersion: ProtocolVersion, Code: code, Message: message, RequestId: append([]byte(nil), request...),
	}}})
}

func (s *Service) writeErrorChannel(channel *registrationChannel, request []byte, code irviov1.RendezvousErrorCode, message string) {
	if len(message) > MaxErrorMessageBytes {
		message = message[:MaxErrorMessageBytes]
	}
	_ = channel.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Error{Error: &irviov1.RendezvousError{
		ProtocolVersion: ProtocolVersion, Code: code, Message: message, RequestId: append([]byte(nil), request...),
	}}})
}

func (s *Service) writeRegistryError(stream network.Stream, request []byte, err error) {
	s.writeError(stream, request, rendezvousErrorCode(err), "request rejected")
}

func (s *Service) writeRegistryErrorChannel(channel *registrationChannel, request []byte, err error) {
	s.writeErrorChannel(channel, request, rendezvousErrorCode(err), "request rejected")
}

func rendezvousErrorCode(err error) irviov1.RendezvousErrorCode {
	switch {
	case errors.Is(err, ErrBusy):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_BUSY
	case errors.Is(err, ErrRateLimited):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RATE_LIMITED
	case errors.Is(err, ErrStale):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_STALE
	case errors.Is(err, ErrConflict):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_CONFLICT
	case errors.Is(err, ErrNotFound):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND
	case errors.Is(err, ErrUnauthorized):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNAUTHORIZED
	case errors.Is(err, ErrRouteFailed):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_ROUTE_FAILED
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_TIMEOUT
	default:
		return irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST
	}
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.host.RemoveStreamHandler(libprotocol.ID(ProtocolID))
		s.host.RemoveStreamHandler(libprotocol.ID(AvailableCountriesProtocolID))
		streams := make([]network.Stream, 0, len(s.streams))
		for stream := range s.streams {
			streams = append(streams, stream)
		}
		s.mu.Unlock()
		s.cancel()
		for _, stream := range streams {
			_ = stream.Reset()
		}
		s.wg.Wait()
		s.registry.Close()
	})
	return nil
}
