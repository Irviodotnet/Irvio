// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"fmt"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

func (c *Client) privateStream(ctx context.Context, server peer.ID) (network.Stream, func(), error) {
	if ctx == nil || server == "" {
		return nil, nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	stream, err := c.host.NewStream(ctx, server, libprotocol.ID(PrivateProtocolID))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if stream.Conn().RemotePeer() != server {
		cancel()
		_ = stream.Reset()
		return nil, nil, ErrWrongPeer
	}
	deadline, _ := ctx.Deadline()
	_ = stream.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	return stream, func() { stop(); cancel(); _ = stream.Close() }, nil
}

func (c *Client) LookupPrivate(ctx context.Context, server, target peer.ID, locator [32]byte) (Candidate, error) {
	if target == "" || locator == ([32]byte{}) {
		return Candidate{}, ErrInvalid
	}
	id, err := NewRequestID(nil)
	if err != nil {
		return Candidate{}, err
	}
	stream, closeStream, err := c.privateStream(ctx, server)
	if err != nil {
		return Candidate{}, err
	}
	defer closeStream()
	err = writePrivateRequest(stream, &irviov1.PrivateRendezvousRequest{ProtocolVersion: ProtocolVersion,
		Message: &irviov1.PrivateRendezvousRequest_Lookup{Lookup: &irviov1.PrivateRendezvousLookup{Locator: locator[:], TargetPeerId: []byte(target), RequestId: id[:]}},
	})
	if err != nil {
		return Candidate{}, err
	}
	response := new(irviov1.PrivateRendezvousResponse)
	if err := readProtoFrameLimit(stream, response, PrivateMaxFrameSize); err != nil {
		return Candidate{}, err
	}
	if response.ProtocolVersion != ProtocolVersion {
		return Candidate{}, ErrInvalid
	}
	if failure := response.GetError(); failure != nil {
		if !bytes.Equal(failure.RequestId, id[:]) {
			return Candidate{}, ErrInvalid
		}
		return Candidate{}, errorFromWire(failure)
	}
	result := response.GetLookup()
	if result == nil || !bytes.Equal(result.RequestId, id[:]) {
		return Candidate{}, ErrInvalid
	}
	candidate, err := PrivateCandidateFromProto(result.Candidate, target, locator, c.now())
	if err != nil {
		return Candidate{}, err
	}
	if len(candidate.Routes) != 1 || candidate.Routes[0].ServerPeer != server || len(candidate.Receipts) != 1 {
		return Candidate{}, ErrWrongPeer
	}
	return candidate, nil
}

func (c *Client) SignalPrivate(ctx context.Context, owner OwnerRoute, signal *irviov1.RendezvousSignal) (*irviov1.RendezvousSignal, error) {
	_, route, _, err := validateSignal(signal, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: private signal validation: %v", ErrInvalid, err)
	}
	if signal.Response || route.ServerPeer != owner.ServerPeer || route.ServerIncarnation != owner.ServerIncarnation || route.RegistrationID != owner.RegistrationID {
		return nil, fmt.Errorf("%w: private signal owner mismatch", ErrInvalid)
	}
	stream, closeStream, err := c.privateStream(ctx, owner.ServerPeer)
	if err != nil {
		return nil, err
	}
	defer closeStream()
	if err := writePrivateRequest(stream, &irviov1.PrivateRendezvousRequest{ProtocolVersion: ProtocolVersion, Message: &irviov1.PrivateRendezvousRequest_Signal{Signal: signal}}); err != nil {
		return nil, err
	}
	frame, err := readRegistrationFrame(stream, true)
	if err != nil {
		return nil, err
	}
	if failure := frame.GetError(); failure != nil {
		return nil, privateSignalBrokerError(failure)
	}
	response := frame.GetSignal()
	if response == nil || !response.Response || !bytes.Equal(response.RequestHandle, signal.RequestHandle) || !bytes.Equal(response.TargetPeerId, signal.TargetPeerId) {
		return nil, fmt.Errorf("%w: private signal response mismatch", ErrInvalid)
	}
	_, responseRoute, _, err := validateSignal(response, c.now())
	if err != nil || responseRoute.ServerPeer != owner.ServerPeer || responseRoute.ServerIncarnation != owner.ServerIncarnation || responseRoute.RegistrationID != owner.RegistrationID {
		return nil, ErrWrongPeer
	}
	return response, nil
}

func privateSignalBrokerError(failure *irviov1.RendezvousError) error {
	reason := "unspecified"
	if failure != nil && failure.ProtocolVersion == ProtocolVersion {
		switch failure.Message {
		case "invalid signal", "invalid target":
			reason = failure.Message
		}
	}
	return fmt.Errorf("private signal broker response (%s): %w", reason, errorFromWire(failure))
}
