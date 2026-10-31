// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (s *Service) handlePrivate(stream network.Stream) {
	if !s.track(stream) {
		_ = stream.Reset()
		return
	}
	defer func() { s.untrack(stream); s.wg.Done() }()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		_ = stream.Reset()
		return
	}
	_ = stream.SetDeadline(time.Now().Add(s.timeout))
	request, err := readPrivateRequest(stream)
	if err != nil {
		_ = stream.Reset()
		return
	}
	operation := admitSignal
	switch request.Message.(type) {
	case *irviov1.PrivateRendezvousRequest_Register:
		operation = admitRegister
	case *irviov1.PrivateRendezvousRequest_Lookup:
		operation = admitGET
	}
	if !s.admission.Allow(stream.Conn().RemotePeer(), stream.Conn().RemoteMultiaddr(), operation) {
		_ = stream.Reset()
		return
	}
	switch request := request.Message.(type) {
	case *irviov1.PrivateRendezvousRequest_Register:
		locator, err := privateLocator(request.Register.GetLocator())
		if err != nil {
			_ = stream.Reset()
			return
		}
		s.handleRegistrationWithLocator(stream, request.Register.GetRegistration(), locator)
	case *irviov1.PrivateRendezvousRequest_Lookup:
		s.handlePrivateLookup(stream, request.Lookup)
	case *irviov1.PrivateRendezvousRequest_Signal:
		s.handleSignal(stream, request.Signal)
	default:
		_ = stream.Reset()
	}
}

func (s *Service) handlePrivateLookup(stream network.Stream, request *irviov1.PrivateRendezvousLookup) {
	defer stream.Close()
	locator, err := privateLocator(request.GetLocator())
	id, idErr := requestID(request.GetRequestId())
	target, peerErr := peer.IDFromBytes(request.GetTargetPeerId())
	if err != nil || idErr != nil || peerErr != nil {
		_ = stream.Reset()
		return
	}
	response := &irviov1.PrivateRendezvousResponse{ProtocolVersion: ProtocolVersion}
	candidate, lookupErr := s.registry.LookupPrivate(target, locator)
	if lookupErr != nil {
		response.Message = &irviov1.PrivateRendezvousResponse_Error{Error: &irviov1.RendezvousError{
			ProtocolVersion: ProtocolVersion, Code: irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND, RequestId: id[:],
		}}
	} else {
		wire, err := PrivateCandidateToProto(candidate)
		if err != nil {
			_ = stream.Reset()
			return
		}
		response.Message = &irviov1.PrivateRendezvousResponse_Lookup{Lookup: &irviov1.PrivateRendezvousLookupResult{RequestId: id[:], Candidate: wire}}
	}
	_ = writeProtoFrameLimit(stream, response, PrivateMaxFrameSize)
}
