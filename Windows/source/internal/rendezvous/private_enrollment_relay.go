// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

const PrivateEnrollmentRelayProtocol = "/irvio/private-enrollment-relay/1.0.0"
const PrivateEnrollmentEndpointProtocol = "/irvio/private-enrollment-endpoint/1.0.0"
const privateEnrollmentByteLimit = 64 * 1024

type enrollmentAddress string

func (a enrollmentAddress) Network() string { return "libp2p" }
func (a enrollmentAddress) String() string  { return string(a) }

// EnrollmentConn adapts an authenticated stream while TLS authenticates the end peer independently.
type EnrollmentConn struct {
	network.Stream
	closeOnce sync.Once
	cleanup   func()
}

func (c *EnrollmentConn) LocalAddr() net.Addr {
	return enrollmentAddress(c.Conn().LocalPeer().String())
}
func (c *EnrollmentConn) RemoteAddr() net.Addr {
	return enrollmentAddress(c.Conn().RemotePeer().String())
}
func (c *EnrollmentConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.Stream.Close()
		if c.cleanup != nil {
			c.cleanup()
		}
	})
	return err
}

// OpenPrivateEnrollment opens only an exact registered target, never an arbitrary relay destination.
func (c *Client) OpenPrivateEnrollment(ctx context.Context, target peer.ID, locator [32]byte, route OwnerRoute) (net.Conn, error) {
	return c.openPrivateEnrollment(ctx, target, locator, route, 30*time.Second)
}

func (c *Client) openPrivateEnrollment(ctx context.Context, target peer.ID, locator [32]byte, route OwnerRoute, setupTimeout time.Duration) (net.Conn, error) {
	_, peerErr := peer.IDFromBytes([]byte(target))
	if ctx == nil || peerErr != nil || locator == ([32]byte{}) || setupTimeout <= 0 || setupTimeout > 30*time.Second {
		return nil, ErrInvalid
	}
	wire := &irviov1.OwnerRoute{ServerPeerId: []byte(route.ServerPeer), ServerIncarnation: route.ServerIncarnation, RegistrationId: route.RegistrationID[:], ControlAddresses: append([]string(nil), route.ControlAddresses...)}
	if _, err := ownerRouteFromProto(wire); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	setup, stopSetup := context.WithTimeout(ctx, setupTimeout)
	defer stopSetup()
	stream, err := c.host.NewStream(setup, route.ServerPeer, libprotocol.ID(PrivateEnrollmentRelayProtocol))
	if err != nil {
		cancel()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { stream.Reset() })
	conn := &EnrollmentConn{Stream: stream, cleanup: func() { stop(); cancel() }}
	deadline, _ := setup.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	request := &irviov1.PrivateEnrollmentRelayRequest{ProtocolVersion: ProtocolVersion, TargetPeerId: []byte(target), Locator: locator[:], OwnerRoute: wire}
	if err := writeProtoFrameLimit(stream, request, PrivateMaxFrameSize); err != nil {
		conn.Close()
		return nil, err
	}
	var ready [1]byte
	if _, err := io.ReadFull(stream, ready[:]); err != nil || ready[0] != 1 {
		conn.Close()
		return nil, ErrRouteFailed
	}
	if err := setup.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	deadline, _ = ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *Service) handleEnrollmentRelay(in network.Stream) {
	if !s.track(in) {
		in.Reset()
		return
	}
	defer func() { in.Reset(); s.untrack(in); s.wg.Done() }()
	select {
	case s.enrollmentSlots <- struct{}{}:
		defer func() { <-s.enrollmentSlots }()
	default:
		return
	}
	ctx, cancel := context.WithTimeout(s.context, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { in.Reset() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := in.SetDeadline(deadline); err != nil {
		return
	}
	if !s.admission.Allow(in.Conn().RemotePeer(), in.Conn().RemoteMultiaddr(), admitSignal) {
		return
	}
	request := new(irviov1.PrivateEnrollmentRelayRequest)
	if err := readProtoFrameLimit(in, request, PrivateMaxFrameSize); err != nil {
		return
	}
	if request.ProtocolVersion != ProtocolVersion || len(request.ProtoReflect().GetUnknown()) != 0 {
		return
	}
	locator, err := privateLocator(request.Locator)
	if err != nil {
		return
	}
	target, err := peer.IDFromBytes(request.TargetPeerId)
	if err != nil {
		return
	}
	route, err := ownerRouteFromProto(request.OwnerRoute)
	if err != nil || route.ServerPeer != s.host.ID() {
		return
	}
	candidate, err := s.registry.LookupPrivate(target, locator)
	if err != nil || len(candidate.Routes) != 1 || candidate.Routes[0].RegistrationID != route.RegistrationID || candidate.Routes[0].ServerIncarnation != route.ServerIncarnation {
		return
	}
	registration, exists := s.registry.Lookup(target)
	channel := s.channelFor(target)
	if !exists || channel == nil || channel.owner != registration.Owner || registration.Owner.RegistrationID != route.RegistrationID {
		return
	}
	out, err := s.host.NewStream(network.WithNoDial(ctx, "private enrollment requires existing registration connection"), target, libprotocol.ID(PrivateEnrollmentEndpointProtocol))
	if err != nil {
		return
	}
	defer out.Reset()
	stopOut := context.AfterFunc(ctx, func() { out.Reset() })
	defer stopOut()
	if err := out.SetDeadline(deadline); err != nil {
		return
	}
	if _, err := in.Write([]byte{1}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	copyBounded := func(dst, src network.Stream) {
		n, err := io.Copy(dst, io.LimitReader(src, privateEnrollmentByteLimit))
		if err != nil || n >= privateEnrollmentByteLimit {
			cancel()
		}
		_ = dst.CloseWrite()
		done <- struct{}{}
	}
	go copyBounded(out, in)
	go copyBounded(in, out)
	for finished := 0; finished < 2; finished++ {
		select {
		case <-done:
		case <-ctx.Done():
			in.Reset()
			out.Reset()
			for ; finished < 2; finished++ {
				<-done
			}
			return
		}
	}
}
