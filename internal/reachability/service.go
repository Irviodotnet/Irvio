// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type FreshChallenger interface {
	Challenge(context.Context, peer.AddrInfo, *irviov1.ReachabilityChallenge) error
}

type ServiceConfig struct {
	Host        host.Host
	PrivateKey  crypto.PrivKey
	Challenger  FreshChallenger
	ObservedIP  ObservedIPPolicy
	AttemptTTL  time.Duration
	ProofTTL    time.Duration
	DialTimeout time.Duration
	MaxActive   int
	MaxPerPeer  int
	MaxPerIP    int
	Now         func() time.Time
	Random      io.Reader
}

type Service struct {
	config ServiceConfig

	mu       sync.Mutex
	active   int
	perPeer  map[peer.ID]int
	perIP    map[netip.Addr]int
	seen     map[AttemptID]time.Time
	closed   bool
	handlers sync.WaitGroup
	once     sync.Once
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Host == nil || config.PrivateKey == nil || config.Challenger == nil || config.Host.ID() == "" {
		return nil, ErrInvalidConfig
	}
	derived, err := peer.IDFromPrivateKey(config.PrivateKey)
	if err != nil || derived != config.Host.ID() {
		return nil, ErrWrongPeer
	}
	if config.AttemptTTL == 0 {
		config.AttemptTTL = DefaultAttemptTTL
	}
	if config.ProofTTL == 0 {
		config.ProofTTL = DefaultProofTTL
	}
	if config.DialTimeout == 0 {
		config.DialTimeout = DefaultAttemptTTL
	}
	if config.MaxActive == 0 {
		config.MaxActive = DefaultMaxActive
	}
	if config.MaxPerPeer == 0 {
		config.MaxPerPeer = DefaultPerPeer
	}
	if config.MaxPerIP == 0 {
		config.MaxPerIP = DefaultPerIP
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.AttemptTTL <= 0 || config.ProofTTL <= 0 || config.ProofTTL > DefaultProofTTL ||
		config.DialTimeout <= 0 || config.DialTimeout > time.Minute || config.MaxActive <= 0 ||
		config.MaxPerPeer <= 0 || config.MaxPerIP <= 0 {
		return nil, ErrInvalidConfig
	}
	service := &Service{config: config, perPeer: make(map[peer.ID]int), perIP: make(map[netip.Addr]int), seen: make(map[AttemptID]time.Time)}
	config.Host.SetStreamHandler(libprotocol.ID(ProtocolID), service.handle)
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
	now := s.config.Now().UTC()
	_ = stream.SetDeadline(now.Add(s.config.AttemptTTL))
	request := new(irviov1.ReachabilityRequest)
	if err := readFrame(stream, request); err != nil {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INVALID_REQUEST)
		return
	}
	remote := stream.Conn().RemotePeer()
	observed, err := observedAddress(stream.Conn().RemoteMultiaddr())
	if err != nil || validateRequest(request, remote) != nil || familyFor(observed) != request.Family ||
		validateObservedIP(observed, s.config.ObservedIP) != nil {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INVALID_REQUEST)
		return
	}
	var attempt AttemptID
	copy(attempt[:], request.AttemptId)
	if !s.acquire(remote, observed, attempt, now) {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_RATE_LIMITED)
		return
	}
	defer s.release(remote, observed)
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(s.config.Random, nonce); err != nil {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INTERNAL)
		return
	}
	target, err := targetInfo(remote, observed, uint16(request.ExternalControlPort))
	if err != nil {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INVALID_REQUEST)
		return
	}
	challenge := &irviov1.ReachabilityChallenge{
		ProtocolVersion: ProtocolVersion, AttemptId: append([]byte(nil), attempt[:]...),
		Nonce: nonce, VerifierPeerId: []byte(s.config.Host.ID()),
	}
	challengeContext, cancel := context.WithTimeout(context.Background(), s.config.DialTimeout)
	err = s.config.Challenger.Challenge(challengeContext, target, challenge)
	cancel()
	if err != nil {
		code := irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_UNREACHABLE
		if errors.Is(err, ErrIdentityMismatch) {
			code = irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_IDENTITY_MISMATCH
		}
		s.reject(stream, code)
		return
	}
	verifiedAt := s.config.Now().UTC()
	attestation, err := signAttestation(s.config.PrivateKey, &irviov1.ReachabilityAttestation{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(remote), VerifierPeerId: []byte(s.config.Host.ID()),
		Family: request.Family, ObservedIp: append([]byte(nil), observed.AsSlice()...),
		ExternalControlPort: request.ExternalControlPort, NetworkGeneration: request.NetworkGeneration,
		PublicEpoch: request.PublicEpoch, AttemptId: append([]byte(nil), attempt[:]...),
		VerifiedAtUnix: verifiedAt.Unix(), ExpiresAtUnix: verifiedAt.Add(s.config.ProofTTL).Unix(),
	})
	if err != nil {
		s.reject(stream, irviov1.ReachabilityErrorCode_REACHABILITY_ERROR_CODE_INTERNAL)
		return
	}
	_ = writeFrame(stream, &irviov1.ReachabilityResponse{
		ProtocolVersion: ProtocolVersion,
		Result:          &irviov1.ReachabilityResponse_Attestation{Attestation: attestation},
	})
}

func validateRequest(request *irviov1.ReachabilityRequest, remote peer.ID) error {
	if request == nil || request.ProtocolVersion != ProtocolVersion || len(request.AttemptId) != AttemptIDSize ||
		request.ExternalControlPort == 0 || request.ExternalControlPort > 65535 ||
		request.NetworkGeneration == 0 || request.PublicEpoch == 0 ||
		request.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 &&
			request.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
		return ErrInvalidMessage
	}
	subject, err := peer.IDFromBytes(request.SubjectPeerId)
	if err != nil || subject != remote {
		return ErrWrongPeer
	}
	var id AttemptID
	copy(id[:], request.AttemptId)
	if id.IsZero() {
		return ErrInvalidMessage
	}
	return nil
}

func (s *Service) acquire(remote peer.ID, address netip.Addr, id AttemptID, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for seen, expiry := range s.seen {
		if !expiry.After(now) {
			delete(s.seen, seen)
		}
	}
	if s.closed || s.active >= s.config.MaxActive || s.perPeer[remote] >= s.config.MaxPerPeer ||
		s.perIP[address] >= s.config.MaxPerIP || len(s.seen) >= 4*s.config.MaxActive {
		return false
	}
	if _, exists := s.seen[id]; exists {
		return false
	}
	s.seen[id] = now.Add(s.config.AttemptTTL)
	s.active++
	s.perPeer[remote]++
	s.perIP[address]++
	return true
}

func (s *Service) release(remote peer.ID, address netip.Addr) {
	s.mu.Lock()
	s.active--
	s.perPeer[remote]--
	s.perIP[address]--
	if s.perPeer[remote] == 0 {
		delete(s.perPeer, remote)
	}
	if s.perIP[address] == 0 {
		delete(s.perIP, address)
	}
	s.mu.Unlock()
}

func (s *Service) reject(stream network.Stream, code irviov1.ReachabilityErrorCode) {
	_ = writeFrame(stream, &irviov1.ReachabilityResponse{
		ProtocolVersion: ProtocolVersion, Result: &irviov1.ReachabilityResponse_Error{Error: code},
	})
}

func observedAddress(address ma.Multiaddr) (netip.Addr, error) {
	for _, code := range []int{ma.P_IP4, ma.P_IP6} {
		value, err := address.ValueForProtocol(code)
		if err != nil {
			continue
		}
		parsed, err := netip.ParseAddr(value)
		if err == nil && parsed.IsValid() && !parsed.Is4In6() {
			return parsed, nil
		}
	}
	return netip.Addr{}, ErrInvalidMessage
}

func validateObservedIP(address netip.Addr, policy ObservedIPPolicy) error {
	if !address.IsValid() || address.Is4In6() || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return ErrInvalidMessage
	}
	if address.IsLoopback() {
		if policy.AllowLoopback {
			return nil
		}
		return ErrInvalidMessage
	}
	if address.IsPrivate() {
		if policy.AllowPrivate {
			return nil
		}
		return ErrInvalidMessage
	}
	testOnly := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range testOnly {
		if prefix.Contains(address) {
			if policy.AllowDocumentation {
				return nil
			}
			return ErrInvalidMessage
		}
	}
	nonPublic := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	}
	for _, prefix := range nonPublic {
		if prefix.Contains(address) {
			return ErrInvalidMessage
		}
	}
	if !address.IsGlobalUnicast() {
		return ErrInvalidMessage
	}
	return nil
}

func targetInfo(subject peer.ID, address netip.Addr, port uint16) (peer.AddrInfo, error) {
	family := "ip6"
	if address.Is4() {
		family = "ip4"
	}
	value, err := ma.NewMultiaddr("/" + family + "/" + address.String() + "/tcp/" + strconv.Itoa(int(port)))
	if err != nil {
		return peer.AddrInfo{}, ErrInvalidMessage
	}
	return peer.AddrInfo{ID: subject, Addrs: []ma.Multiaddr{value}}, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.config.Host.RemoveStreamHandler(libprotocol.ID(ProtocolID))
		s.mu.Unlock()
		s.handlers.Wait()
	})
	return nil
}
