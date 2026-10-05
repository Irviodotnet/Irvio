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
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/sessionestablish"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type BrokerCarrier struct {
	client    *Client
	candidate Candidate
	hops      []brokerHop

	mu        sync.Mutex
	messages  int
	bytes     int
	failed    map[string]struct{}
	preferred string
	logger    *slog.Logger
}

type brokerHop struct {
	ingress peer.ID
	owner   OwnerRoute
	receipt SignedReceipt
}

func NewBrokerCarrier(client *Client, ingress peer.ID, candidate Candidate) (*BrokerCarrier, error) {
	if client == nil || ingress == "" {
		return nil, ErrInvalid
	}
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	hops := make([]brokerHop, 0, min(MaxOwnerRoutes, len(candidate.Routes)))
	for _, owner := range candidate.Routes[:min(MaxOwnerRoutes, len(candidate.Routes))] {
		receipt, exists := candidateReceipt(candidate, owner)
		if !exists {
			return nil, ErrUnauthorized
		}
		hops = append(hops, brokerHop{ingress: ingress, owner: cloneOwnerRoute(owner), receipt: cloneSignedReceipt(receipt)})
	}
	return newBrokerCarrier(client, candidate, hops)
}

func newBrokerCarrier(client *Client, candidate Candidate, hops []brokerHop) (*BrokerCarrier, error) {
	if client == nil || len(hops) == 0 || len(hops) > MaxOwnerRoutes {
		return nil, ErrInvalid
	}
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	cloned := make([]brokerHop, 0, len(hops))
	for _, hop := range hops {
		if hop.ingress == "" || !brokerHopMatchesDescriptor(candidate.Descriptor, hop) {
			return nil, ErrUnauthorized
		}
		cloned = append(cloned, cloneBrokerHop(hop))
	}
	return &BrokerCarrier{
		client: client, candidate: cloneCandidate(candidate), hops: cloned,
		failed: make(map[string]struct{}, len(cloned)),
	}, nil
}

func (c *BrokerCarrier) Exchange(ctx context.Context, target peer.ID, signed []byte) ([]byte, error) {
	if ctx == nil || target != c.candidate.Descriptor.Subject ||
		len(signed) == 0 || len(signed) > sessionestablish.MaxWireSize {
		return nil, ErrInvalid
	}
	hops := c.availableHops()
	var failures []error
	for index, hop := range hops {
		routeStarted := time.Now()
		if !hop.receipt.Receipt.ExpiresAt.After(time.Now().UTC()) {
			failures = append(failures, ErrStale)
			c.markRouteFailed(hop)
			c.logRouteFailure(hop, index, len(hops), ErrStale, time.Since(routeStarted), 0)
			continue
		}
		c.mu.Lock()
		if c.messages >= MaxSignalingMessages || c.bytes+len(signed) > MaxSignalingBytes {
			c.mu.Unlock()
			return nil, ErrBusy
		}
		c.messages++
		c.bytes += len(signed)
		c.mu.Unlock()
		deadline := time.Now().Add(20 * time.Second)
		if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
			deadline = caller
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errors.Join(append(failures, context.DeadlineExceeded)...)
		}
		routesLeft := len(hops) - index
		perRoute := remaining / time.Duration(routesLeft)
		routeContext, cancel := context.WithTimeout(ctx, perRoute)
		request, err := NewRequestID(nil)
		if err != nil {
			cancel()
			return nil, err
		}
		response, err := c.client.Signal(routeContext, hop.ingress, &irviov1.RendezvousSignal{
			ProtocolVersion: ProtocolVersion, RequestHandle: append([]byte(nil), request[:]...),
			TargetPeerId: []byte(target),
			OwnerRoute: &irviov1.OwnerRoute{
				ServerPeerId: []byte(hop.owner.ServerPeer), ServerIncarnation: hop.owner.ServerIncarnation,
				RegistrationId:   append([]byte(nil), hop.owner.RegistrationID[:]...),
				ControlAddresses: append([]string(nil), hop.owner.ControlAddresses...),
			},
			SignedMessage:     append([]byte(nil), signed...),
			DeadlineUnixMilli: deadline.UnixMilli(),
		})
		cancel()
		if err != nil {
			failures = append(failures, err)
			c.markRouteFailed(hop)
			c.logRouteFailure(hop, index, len(hops), err, time.Since(routeStarted), max(0, time.Until(deadline)))
			continue
		}
		c.mu.Lock()
		nextBytes := c.bytes + len(response.SignedMessage)
		if c.messages >= MaxSignalingMessages || nextBytes > MaxSignalingBytes {
			c.mu.Unlock()
			return nil, ErrBusy
		}
		c.messages++
		c.bytes = nextBytes
		c.preferred = brokerHopKey(hop)
		c.mu.Unlock()
		return append([]byte(nil), response.SignedMessage...), nil
	}
	if len(failures) == 0 {
		return nil, ErrRouteFailed
	}
	return nil, fmt.Errorf("%w: %v", ErrRouteFailed, errors.Join(failures...))
}

func (c *BrokerCarrier) availableHops() []brokerHop {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]brokerHop, 0, len(c.hops))
	for _, hop := range c.hops {
		key := brokerHopKey(hop)
		if _, failed := c.failed[key]; failed || key == c.preferred {
			continue
		}
		result = append(result, cloneBrokerHop(hop))
	}
	if c.preferred != "" {
		for _, hop := range c.hops {
			if brokerHopKey(hop) == c.preferred {
				result = append([]brokerHop{cloneBrokerHop(hop)}, result...)
				break
			}
		}
	}
	return result
}

func (c *BrokerCarrier) markRouteFailed(hop brokerHop) {
	c.mu.Lock()
	key := brokerHopKey(hop)
	c.failed[key] = struct{}{}
	if c.preferred == key {
		c.preferred = ""
	}
	c.mu.Unlock()
}

func (c *BrokerCarrier) logRouteFailure(hop brokerHop, index, count int, err error, elapsed, remaining time.Duration) {
	if c.logger == nil {
		return
	}
	c.logger.Warn("RENDEZVOUS_SIGNAL_ROUTE_FAILED",
		"peer_id", c.candidate.Descriptor.Subject,
		"route_peer_id", hop.ingress,
		"owner_peer_id", hop.owner.ServerPeer,
		"route_index", index,
		"route_count", count,
		"failure_scope", "broker_route",
		"error_class", signalingRouteErrorClass(err),
		"elapsed", elapsed,
		"remaining_budget", remaining,
		"suppression_scope", "route",
	)
}

func brokerHopKey(hop brokerHop) string {
	return hop.ingress.String() + "/" + ownerKey(hop.owner.ServerPeer, hop.owner.ServerIncarnation, hop.owner.RegistrationID)
}

func signalingRouteErrorClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrStale):
		return "stale"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrBusy):
		return "busy"
	default:
		return "route_failed"
	}
}

func candidateReceipt(candidate Candidate, owner OwnerRoute) (SignedReceipt, bool) {
	key := ownerKey(owner.ServerPeer, owner.ServerIncarnation, owner.RegistrationID)
	for _, receipt := range candidate.Receipts {
		if ownerKey(receipt.Receipt.ServerPeer, receipt.Receipt.ServerIncarnation, receipt.Receipt.RegistrationID) == key {
			return receipt, true
		}
	}
	return SignedReceipt{}, false
}

func brokerHopMatchesDescriptor(descriptor Descriptor, hop brokerHop) bool {
	receipt := hop.receipt.Receipt
	return receipt.SubjectPeer == descriptor.Subject && receipt.DescriptorID == descriptor.ID &&
		receipt.DescriptorVersion == descriptor.Version && receipt.ServerPeer == hop.owner.ServerPeer &&
		receipt.ServerIncarnation == hop.owner.ServerIncarnation && receipt.RegistrationID == hop.owner.RegistrationID
}

func cloneBrokerHop(value brokerHop) brokerHop {
	return brokerHop{ingress: value.ingress, owner: cloneOwnerRoute(value.owner), receipt: cloneSignedReceipt(value.receipt)}
}

func (c *BrokerCarrier) Usage() (messages, bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.messages, c.bytes
}

var _ sessionestablish.Carrier = (*BrokerCarrier)(nil)
