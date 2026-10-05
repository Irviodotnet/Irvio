// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsignal

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/tunnellease"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

func writeFrame(writer io.Writer, message proto.Message) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > MaxFrameSize {
		return ErrInvalidMessage
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := writer.Write(payload)
		if n <= 0 || n > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[n:]
		if err != nil {
			return err
		}
	}
	return nil
}

func readFrame(reader io.Reader, message proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxFrameSize {
		return ErrInvalidMessage
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return err
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 16, DiscardUnknown: false}).Unmarshal(payload, message); err != nil {
		return ErrInvalidMessage
	}
	return nil
}

func leaseToProto(lease tunnellease.Lease) *irviov1.TunnelLease {
	message := &irviov1.TunnelLease{
		LeaseId: append([]byte(nil), lease.ID[:]...), ExitPeerId: []byte(lease.ExitPeer),
		ClientPeerId: []byte(lease.ClientPeer), ValidUntilUnix: lease.ValidUntil.Unix(),
		Generation: lease.Generation,
	}
	if !lease.Session.IsZero() {
		message.SessionToken = append([]byte(nil), lease.Session[:]...)
	}
	if lease.IPv4.IsValid() {
		message.Ipv4Address = append([]byte(nil), lease.IPv4.Addr().AsSlice()...)
		message.Ipv4PrefixBits = uint32(lease.IPv4.Bits())
	}
	if lease.IPv6.IsValid() {
		message.Ipv6Address = append([]byte(nil), lease.IPv6.Addr().AsSlice()...)
		message.Ipv6PrefixBits = uint32(lease.IPv6.Bits())
	}
	return message
}

// LeaseToProto encodes fenced leases for signed M12 grants and authenticated tunnel control.
func LeaseToProto(lease tunnellease.Lease) *irviov1.TunnelLease {
	return leaseToProto(lease)
}

func leaseFromProto(message *irviov1.TunnelLease, authenticatedExit, client peer.ID, now time.Time) (tunnellease.Lease, error) {
	if message == nil || len(message.LeaseId) != len(tunnellease.ID{}) {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	exitPeer, err := peer.IDFromBytes(message.ExitPeerId)
	if err != nil || exitPeer != authenticatedExit {
		return tunnellease.Lease{}, ErrWrongPeer
	}
	clientPeer, err := peer.IDFromBytes(message.ClientPeerId)
	if err != nil || clientPeer != client || clientPeer == exitPeer {
		return tunnellease.Lease{}, ErrWrongPeer
	}
	var lease tunnellease.Lease
	copy(lease.ID[:], message.LeaseId)
	if lease.ID.IsZero() {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	lease.ExitPeer, lease.ClientPeer = exitPeer, clientPeer
	lease.Generation = message.Generation
	if len(message.SessionToken) != 0 {
		if len(message.SessionToken) != len(lease.Session) {
			return tunnellease.Lease{}, ErrInvalidMessage
		}
		copy(lease.Session[:], message.SessionToken)
		if lease.Session.IsZero() {
			return tunnellease.Lease{}, ErrInvalidMessage
		}
	}
	lease.IPv4, err = wirePrefix(message.Ipv4Address, message.Ipv4PrefixBits, true)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	lease.IPv6, err = wirePrefix(message.Ipv6Address, message.Ipv6PrefixBits, false)
	if err != nil {
		return tunnellease.Lease{}, err
	}
	if !lease.IPv4.IsValid() && !lease.IPv6.IsValid() {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	lease.ValidUntil = time.Unix(message.ValidUntilUnix, 0).UTC()
	if !lease.ValidUntil.After(now) || lease.ValidUntil.After(now.Add(24*time.Hour)) {
		return tunnellease.Lease{}, ErrInvalidMessage
	}
	return lease, nil
}

// LeaseFromProto validates a TunnelLease against the authenticated end peers.
func LeaseFromProto(message *irviov1.TunnelLease, authenticatedExit, client peer.ID, now time.Time) (tunnellease.Lease, error) {
	return leaseFromProto(message, authenticatedExit, client, now)
}

func wirePrefix(payload []byte, bits uint32, ipv4 bool) (netip.Prefix, error) {
	if len(payload) == 0 && bits == 0 {
		return netip.Prefix{}, nil
	}
	want := 16
	wantBits := uint32(128)
	if ipv4 {
		want, wantBits = 4, 32
	}
	if len(payload) != want || bits != wantBits {
		return netip.Prefix{}, ErrInvalidMessage
	}
	address, ok := netip.AddrFromSlice(payload)
	if !ok || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() || address.Is4() != ipv4 {
		return netip.Prefix{}, ErrInvalidMessage
	}
	return netip.PrefixFrom(address, int(bits)), nil
}

func request(operation irviov1.TunnelLeaseOperation, exit, client peer.ID, id tunnellease.ID, generation uint64, token tunnellease.SessionToken) *irviov1.TunnelLeaseRequest {
	message := &irviov1.TunnelLeaseRequest{
		ProtocolVersion: ProtocolVersion, Operation: operation,
		ExitPeerId: []byte(exit), ClientPeerId: []byte(client),
	}
	if operation == irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RELEASE ||
		operation == irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RENEW {
		message.LeaseId = append([]byte(nil), id[:]...)
		message.LeaseGeneration = generation
	}
	if !token.IsZero() {
		message.SessionToken = append([]byte(nil), token[:]...)
	}
	return message
}

func validateRequest(message *irviov1.TunnelLeaseRequest, authenticatedClient, localExit peer.ID) (tunnellease.ID, uint64, tunnellease.SessionToken, error) {
	if message == nil || message.ProtocolVersion != ProtocolVersion {
		return tunnellease.ID{}, 0, tunnellease.SessionToken{}, ErrInvalidMessage
	}
	exitPeer, err := peer.IDFromBytes(message.ExitPeerId)
	if err != nil || exitPeer != localExit {
		return tunnellease.ID{}, 0, tunnellease.SessionToken{}, ErrWrongPeer
	}
	clientPeer, err := peer.IDFromBytes(message.ClientPeerId)
	if err != nil || clientPeer != authenticatedClient || clientPeer == localExit {
		return tunnellease.ID{}, 0, tunnellease.SessionToken{}, ErrWrongPeer
	}
	var id tunnellease.ID
	var token tunnellease.SessionToken
	if len(message.SessionToken) != 0 {
		if len(message.SessionToken) != len(token) {
			return id, 0, token, ErrInvalidMessage
		}
		copy(token[:], message.SessionToken)
		if token.IsZero() {
			return id, 0, token, ErrInvalidMessage
		}
	}
	switch message.Operation {
	case irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_ACQUIRE:
		if len(message.LeaseId) != 0 || message.LeaseGeneration != 0 {
			return id, 0, token, ErrInvalidMessage
		}
	case irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RELEASE,
		irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_RENEW:
		if len(message.LeaseId) != len(id) || message.LeaseGeneration == 0 {
			return id, 0, token, ErrInvalidMessage
		}
		copy(id[:], message.LeaseId)
		if id.IsZero() {
			return id, 0, token, ErrInvalidMessage
		}
	default:
		return id, 0, token, fmt.Errorf("%w: operation", ErrInvalidMessage)
	}
	return id, message.LeaseGeneration, token, nil
}
