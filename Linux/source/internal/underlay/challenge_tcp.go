// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
)

type TCPChallengeProbe struct{ dial tcptransport.DialerForAddr }

func NewTCPChallengeProbe(dial tcptransport.DialerForAddr) (*TCPChallengeProbe, error) {
	if dial == nil {
		return nil, ErrInvalidConfig
	}
	return &TCPChallengeProbe{dial: dial}, nil
}

// Probe retains the existing nonce exchange over a caller-owned platform socket policy.
func (p *TCPChallengeProbe) Probe(ctx context.Context, target Target) error {
	if p == nil || p.dial == nil {
		return ErrInvalidConfig
	}
	return probeTCPChallenge(ctx, target, rand.Reader, func(ctx context.Context, endpoint netip.AddrPort) (net.Conn, error) {
		return controlsocket.DialEndpoint(ctx, p.dial, endpoint)
	})
}

func probeTCPChallenge(ctx context.Context, target Target, random io.Reader, dial func(context.Context, netip.AddrPort) (net.Conn, error)) (result error) {
	if ctx == nil || random == nil || dial == nil {
		return ErrInvalidConfig
	}
	if !target.Endpoint.IsValid() || target.Endpoint.Port() == 0 {
		return ErrInvalidTarget
	}
	a := target.Endpoint.Addr()
	if a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.Is4In6() || a.Zone() != "" {
		return ErrInvalidTarget
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request := make([]byte, challengeSize)
	copy(request, challengeMagic[:])
	if _, err := io.ReadFull(random, request[len(challengeMagic):]); err != nil {
		return fmt.Errorf("create underlay challenge: %w", err)
	}
	connection, err := dial(ctx, target.Endpoint)
	if err != nil {
		return err
	}
	if connection == nil {
		return ErrInvalidConfig
	}
	defer func() { result = errors.Join(result, connection.Close()) }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
		close(finished)
	})
	defer func() {
		if !stop() {
			<-finished
		}
		if ctx.Err() != nil {
			result = errors.Join(ctx.Err(), result)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeAll(connection, request); err != nil {
		return err
	}
	response := make([]byte, len(request))
	if _, err := io.ReadFull(connection, response); err != nil {
		return err
	}
	if !bytes.Equal(response, request) {
		return ErrChallengeMismatch
	}
	return nil
}
