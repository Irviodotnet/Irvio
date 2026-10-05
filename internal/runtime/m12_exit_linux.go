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
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/sessioncontrol"
	"github.com/Irviodotnet/Irvio/internal/sessionestablish"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/tunnelsignal"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const m12ProvisionalLifetime = 30 * time.Second

type m12ExitServices struct {
	server    *ExitServer
	responder *sessionestablish.Responder
	direct    *sessionestablish.DirectService
	controls  []*sessioncontrol.Server
	addresses [][]byte
	profile   [sha256.Size]byte
	workers   sync.WaitGroup
}

type m12PrepareAuthorizer struct{ server *ExitServer }

func (a m12PrepareAuthorizer) AuthorizePrepare(_ context.Context, end sessionestablish.EndPeerContext, prepare *irviov1.SessionPrepare) error {
	if a.server == nil || end.RemotePeer == "" || end.RemotePeer == a.server.config.LocalPeer ||
		prepare == nil || prepare.Purpose != irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT {
		return errors.New("session establishment is outside Irvio Core authorization")
	}
	if a.server.rotationPending() {
		return tunnellease.ErrExhausted
	}
	if len(a.server.slots) >= cap(a.server.slots) {
		return tunnellease.ErrExhausted
	}
	return nil
}

type m12ControlAuthority struct {
	sessioncontrol.LifecycleAuthority
	server *ExitServer
}

func (a *m12ControlAuthority) RotationProposal(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (sessioncontrol.RotationProposal, error) {
	if _, err := a.Lookup(client, id, revision, token); err != nil {
		return sessioncontrol.RotationProposal{}, err
	}
	return a.server.rotationProposal(client, id, token)
}

func (a *m12ControlAuthority) ValidateAWGRotation(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken, epoch uint64, digest [32]byte) error {
	return a.server.validateAWGRotation(client, id, revision, token, epoch, digest)
}

func (a *m12ControlAuthority) CommitAWGRotation(ctx context.Context, client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken, epoch uint64, digest [32]byte) (tunnellease.Lease, error) {
	return a.server.commitAWGRotation(ctx, client, id, revision, token, epoch, digest)
}

type m12OfferProcessor struct{ services *m12ExitServices }

func newM12ExitServices(ctx context.Context, server *ExitServer) (_ *m12ExitServices, resultErr error) {
	config := server.config
	if config.IdentityPrivate == nil || config.SignedDescriptor == nil || config.SessionControlPort == 0 {
		return nil, fmt.Errorf("M12 Exit identity, descriptor, and session-control port are required: %w", ErrInvalidConfig)
	}
	profile, err := awg.ObfuscationProfileHash(config.Obfuscation)
	if err != nil {
		return nil, err
	}
	services := &m12ExitServices{server: server, profile: profile}
	authority := &m12ControlAuthority{
		LifecycleAuthority: sessioncontrol.LifecycleAuthority{Lifecycle: server.lifecycle}, server: server,
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, services.Close())
		}
	}()
	seen := make(map[netip.Addr]struct{}, len(config.TUNAddresses))
	for _, prefix := range config.TUNAddresses {
		address := prefix.Addr()
		if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
			continue
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		listener, err := sessioncontrol.ListenTunnel(ctx, "tcp", net.JoinHostPort(address.String(), fmt.Sprint(config.SessionControlPort)), config.TUNName)
		if err != nil {
			return nil, fmt.Errorf("listen for in-tunnel session control on %s: %w", address, err)
		}
		control, err := sessioncontrol.NewServer(
			listener, config.IdentityPrivate, authority, config.RequestTimeout,
		)
		if err != nil {
			_ = listener.Close()
			return nil, err
		}
		services.controls = append(services.controls, control)
		services.addresses = append(services.addresses, append([]byte(nil), address.AsSlice()...))
	}
	if len(services.controls) == 0 {
		return nil, fmt.Errorf("M12 Exit has no valid inner control address: %w", ErrInvalidConfig)
	}
	descriptor := func() sessionestablish.DescriptorBinding {
		value, err := sessionestablish.BindingFromSignedAnnouncement(
			config.SignedDescriptor(), config.LocalPeer, time.Now().UTC(),
		)
		if err != nil {
			return sessionestablish.DescriptorBinding{}
		}
		return value
	}
	services.responder, err = sessionestablish.NewResponder(sessionestablish.ResponderOptions{
		Local: config.LocalPeer, PrivateKey: config.IdentityPrivate, Descriptor: descriptor,
		NetworkGeneration: func() uint64 {
			if generation := config.Transports.Current(); generation != nil {
				return uint64(generation.ID())
			}
			return 0
		},
		Authorizer: m12PrepareAuthorizer{server: server}, Processor: m12OfferProcessor{services: services},
		Policy: sessionestablish.DefaultPolicy(), MaxPending: config.MaxPending,
	})
	if err != nil {
		return nil, err
	}
	services.direct, err = sessionestablish.NewDirectService(config.Host, services.responder, config.RequestTimeout, config.MaxPending)
	if err != nil {
		return nil, err
	}
	return services, nil
}

func (p m12OfferProcessor) ProcessOffer(ctx context.Context, end sessionestablish.EndPeerContext, message *irviov1.SessionOffer) (sessionestablish.AnswerResult, error) {
	services := p.services
	if services == nil || services.server == nil || message == nil ||
		!bytes.Equal(message.AwgProfileHash, services.profile[:]) {
		return sessionestablish.AnswerResult{}, ErrInvalidConfig
	}
	server := services.server
	server.rotationMu.Lock()
	defer server.rotationMu.Unlock()
	if server.rotation != nil && !server.rotation.committed {
		return sessionestablish.AnswerResult{}, tunnellease.ErrExhausted
	}
	record, err := protocol.VerifySignedAnnouncementRecord(end.ClientDescriptor.SignedEnvelope, end.RemotePeer, time.Now().UTC())
	if err != nil || record.Announcement.ProtocolVersion != protocol.ProtocolVersion ||
		record.Announcement.Capabilities&protocol.CapDiscovery == 0 ||
		!containsSessionFeature(record.Announcement.SessionFeatures, "session-establish-v1") ||
		!containsSessionFeature(record.Announcement.SessionFeatures, "session-control-v1") {
		return sessionestablish.AnswerResult{}, errors.New("signed Client announcement is incompatible")
	}
	clientBinding, err := server.config.Bindings.ResolveSigned(end.ClientDescriptor.SignedEnvelope, end.RemotePeer)
	if err != nil || clientBinding.Binding.KeyEpoch != end.ClientDescriptor.AWGKeyEpoch {
		return sessionestablish.AnswerResult{}, errors.New("signed Client AWG binding is stale or conflicting")
	}
	offer, err := icesignal.OfferFromProto(message.IceOffer, server.config.Validation)
	if err != nil || icesignal.ValidateOffer(offer, end.RemotePeer, server.config.LocalPeer, time.Now(), server.config.SignalConfig) != nil {
		return sessionestablish.AnswerResult{}, errors.New("signed ICE offer is invalid")
	}
	generation := server.config.Transports.Current()
	if generation == nil || uint64(generation.ID()) != end.ExitNetworkGeneration {
		return sessionestablish.AnswerResult{}, ErrGenerationStale
	}
	prepared, err := generation.NewPreparedSession(ctx)
	if err != nil {
		return sessionestablish.AnswerResult{}, err
	}
	owned := true
	defer func() {
		if owned {
			_ = prepared.Close()
		}
	}()
	localDescription, err := icesession.ValidateDescription(prepared.LocalDescription(), server.config.Validation)
	if err != nil {
		return sessionestablish.AnswerResult{}, err
	}
	if err := prepared.BindConnectivity(icesession.ConnectivityBinding{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		LocalPeer: server.config.LocalPeer, RemotePeer: end.RemotePeer, Purpose: offer.Purpose,
		LocalRole: icesession.RoleControlled, RemoteRole: offer.Role,
	}); err != nil {
		return sessionestablish.AnswerResult{}, err
	}
	now := time.Now().UTC()
	activationDeadline := m12ActivationDeadline(ctx, now)
	if !activationDeadline.After(now.Add(time.Second)) {
		return sessionestablish.AnswerResult{}, context.DeadlineExceeded
	}
	token := m12LeaseToken(end)
	lease, err := server.lifecycle.AllocateProvisionalForSession(end.RemotePeer, token, activationDeadline)
	if err != nil {
		return sessionestablish.AnswerResult{}, err
	}
	accepted := icesignal.AcceptedSession{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		RemotePeer: end.RemotePeer, Local: prepared, RemoteDescription: offer.Description.Clone(),
		Purpose: offer.Purpose, LocalRole: icesession.RoleControlled, RemoteRole: offer.Role,
	}
	select {
	case server.slots <- struct{}{}:
		services.workers.Add(1)
		go func() {
			defer services.workers.Done()
			defer func() { <-server.slots }()
			server.prepareM12Accepted(accepted, lease, clientBinding.Key, clientBinding.Binding.KeyEpoch)
		}()
		owned = false
	default:
		releaseContext, cancel := context.WithTimeout(context.Background(), server.config.RequestTimeout)
		_ = server.lifecycle.ReleaseSession(releaseContext, lease.ClientPeer, lease.ID, lease.Generation, lease.Session)
		cancel()
		return sessionestablish.AnswerResult{}, tunnellease.ErrExhausted
	}
	answer := icesignal.Answer{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		Purpose: offer.Purpose, Role: icesession.RoleControlled, Description: localDescription,
		CreatedAt: now.Truncate(time.Second), ExpiresAt: now.Truncate(time.Second).Add(server.config.SignalConfig.DescriptionTTL),
	}
	return sessionestablish.AnswerResult{
		ICEAnswer: icesignal.AnswerToProto(answer), ProvisionalGrant: tunnelsignal.LeaseToProto(lease),
		ExitControlAddresses: cloneControlAddresses(services.addresses), ExitControlPort: uint32(server.config.SessionControlPort),
		AWGProfileHash: services.profile, ActivationDeadline: activationDeadline,
	}, nil
}

// Signaling bounds ANSWER delivery; ICE/AWG/BIND use a separate bounded provisional lifetime.
func m12ActivationDeadline(_ context.Context, now time.Time) time.Time {
	return now.Add(m12ProvisionalLifetime)
}

func (s *ExitServer) prepareM12Accepted(accepted icesignal.AcceptedSession, lease tunnellease.Lease, key awg.Key, keyEpoch uint64) {
	s.config.Logger.Info("BROKERED_ICE_ACCEPT_STARTED", "peer_id", accepted.RemotePeer,
		"local_candidates", m12CandidateEndpoints(accepted.Local.LocalDescription().Candidates),
		"remote_candidates", m12CandidateEndpoints(accepted.RemoteDescription.Candidates))
	attempt, err := iceconnect.New(accepted.Local, iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: s.config.LocalPeer, RemotePeer: accepted.RemotePeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: accepted.RemotePeer,
		Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, iceConnectConfig(s.config.Validation))
	if err == nil {
		err = attempt.LoadRemoteCandidates(s.ctx)
	}
	var connected *iceconnect.ConnectedSession
	if err == nil {
		connected, err = attempt.Connect(s.ctx)
	}
	if err == nil {
		s.config.Logger.Info("BROKERED_ICE_ACCEPT_CONNECTED", "peer_id", accepted.RemotePeer,
			"local_endpoint", connected.Result().LocalEndpoint, "remote_endpoint", connected.Result().RemoteEndpoint)
	}
	if err == nil {
		sessionGeneration := connsupervisor.SessionGeneration(s.next.Add(1))
		var endpoint awg.SelectedEndpoint
		endpoint, err = awg.SelectedEndpointFromICE(
			connected.Result(), networkstate.Generation(accepted.Local.TransportGeneration()), sessionGeneration, accepted.RemotePeer,
		)
		if err == nil {
			err = s.plane.registerPending(&pendingExitSession{
				token: lease.Session, remote: accepted.RemotePeer, key: key, keyEpoch: keyEpoch,
				endpoint: endpoint, ice: connected, attempt: attempt, createdAt: time.Now().UTC(),
			})
		}
	}
	if err == nil {
		_, err = s.lifecycle.ActivateSession(lease.ClientPeer, lease.ID, lease.Generation, lease.Session)
	}
	if err == nil {
		return
	}
	s.config.Logger.Warn("BROKERED_ICE_ACCEPT_FAILED", "peer_id", accepted.RemotePeer, "reason", err)
	if connected != nil {
		_ = connected.Close()
	} else if attempt != nil {
		_ = attempt.Close()
	} else {
		_ = accepted.Local.Close()
	}
	releaseContext, cancel := context.WithTimeout(context.Background(), s.config.RequestTimeout)
	_ = s.lifecycle.ReleaseSession(releaseContext, lease.ClientPeer, lease.ID, lease.Generation, lease.Session)
	cancel()
}

func m12LeaseToken(end sessionestablish.EndPeerContext) tunnellease.SessionToken {
	return m12LeaseTokenFor(end.AttemptID, end.OwnerToken, end.RemotePeer)
}

func m12LeaseTokenFor(attempt sessionestablish.AttemptID, owner [sessionestablish.OwnerTokenSize]byte, client peer.ID) tunnellease.SessionToken {
	hash := sha256.New()
	_, _ = hash.Write([]byte("hydra/m12/lease-token/v1\x00"))
	_, _ = hash.Write(attempt[:])
	_, _ = hash.Write(owner[:])
	_, _ = hash.Write([]byte(client))
	var token tunnellease.SessionToken
	copy(token[:], hash.Sum(nil))
	return token
}

func containsSessionFeature(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneControlAddresses(values [][]byte) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = append([]byte(nil), value...)
	}
	return result
}

// SessionEstablishmentHandler verifies signed end peers over direct or registered Rendezvous channels.
func (s *ExitServer) SessionEstablishmentHandler() *sessionestablish.Responder {
	if s == nil || s.m12 == nil {
		return nil
	}
	return s.m12.responder
}

func (s *m12ExitServices) Close() error {
	if s == nil {
		return nil
	}
	if s.direct != nil {
		s.direct.Close()
		s.direct = nil
	}
	if s.responder != nil {
		s.responder.Close()
		s.responder = nil
	}
	s.workers.Wait()
	var result error
	for _, control := range s.controls {
		result = errors.Join(result, control.Close())
	}
	s.controls = nil
	return result
}
