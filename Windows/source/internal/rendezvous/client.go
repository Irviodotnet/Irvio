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
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type Client struct {
	host    host.Host
	timeout time.Duration
	now     func() time.Time
}

func (c *Client) ListLocalAvailableCountries(ctx context.Context, server peer.ID) ([]string, error) {
	if ctx == nil || server == "" {
		return nil, ErrInvalid
	}
	deadline := time.Now().Add(c.timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	stream, err := c.host.NewStream(ctx, server, libprotocol.ID(AvailableCountriesProtocolID))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(deadline)
	if stream.Conn().RemotePeer() != server {
		_ = stream.Reset()
		return nil, ErrWrongPeer
	}
	if err := writeAvailableCountriesFrame(stream, &irviov1.AvailableCountriesFrame{
		Message: &irviov1.AvailableCountriesFrame_Request{Request: &irviov1.ListLocalAvailableCountriesRequest{
			ProtocolVersion: ProtocolVersion,
		}},
	}); err != nil {
		_ = stream.Reset()
		return nil, err
	}
	response, err := readAvailableCountriesFrame(stream)
	if err != nil {
		_ = stream.Reset()
		return nil, err
	}
	wire, ok := response.Message.(*irviov1.AvailableCountriesFrame_Response)
	if !ok || wire.Response.GetProtocolVersion() != ProtocolVersion || len(wire.Response.GetCountryCodes()) > MaxAvailableCountries {
		return nil, ErrInvalid
	}
	result := append([]string(nil), wire.Response.GetCountryCodes()...)
	for index, country := range result {
		if !validCountry(country) || index > 0 && result[index-1] >= country {
			return nil, ErrInvalid
		}
	}
	if !sort.StringsAreSorted(result) {
		return nil, ErrInvalid
	}
	return result, nil
}

func NewClient(host host.Host, timeout time.Duration) (*Client, error) {
	if host == nil {
		return nil, fmt.Errorf("%w: client host", ErrInvalid)
	}
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, fmt.Errorf("%w: client timeout", ErrInvalid)
	}
	return &Client{host: host, timeout: timeout, now: time.Now}, nil
}

func (c *Client) GetCandidates(ctx context.Context, server peer.ID, country string, limit int, excluded []peer.ID) ([]Candidate, bool, error) {
	if ctx == nil || server == "" || !validCountry(country) || limit < 1 || limit > MaxCandidates || len(excluded) > MaxExcludedPeers {
		return nil, false, ErrInvalid
	}
	request, err := NewRequestID(nil)
	if err != nil {
		return nil, false, err
	}
	excludeBytes := make([][]byte, 0, len(excluded))
	for _, value := range excluded {
		if value == "" {
			return nil, false, ErrInvalid
		}
		excludeBytes = append(excludeBytes, []byte(value))
	}
	response, err := c.exchange(ctx, server, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_GetCandidates{
		GetCandidates: &irviov1.RendezvousGetCandidates{
			ProtocolVersion: ProtocolVersion, Country: country, Limit: uint32(limit),
			ExcludePeerIds: excludeBytes, RequestId: append([]byte(nil), request[:]...),
		},
	}})
	if err != nil {
		return nil, false, err
	}
	wire, ok := response.Message.(*irviov1.RendezvousFrame_Candidates_)
	if !ok || wire.Candidates_.GetProtocolVersion() != ProtocolVersion ||
		wire.Candidates_.GetCountry() != country ||
		!bytes.Equal(wire.Candidates_.GetRequestId(), request[:]) ||
		len(wire.Candidates_.GetCandidates()) > limit {
		return nil, false, ErrInvalid
	}
	candidates := make([]Candidate, 0, len(wire.Candidates_.Candidates))
	for _, message := range wire.Candidates_.Candidates {
		candidate, err := CandidateFromProto(message, c.now())
		if err != nil {
			return nil, false, err
		}
		if candidate.Descriptor.Subject == c.host.ID() || !candidate.SatisfiesCountry(country) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	candidates, err = MergeCandidates(candidates...)
	if err != nil {
		return nil, false, err
	}
	return candidates, wire.Candidates_.Partial, nil
}

func (c *Client) SampleLocal(ctx context.Context, server peer.ID, country string, limit int, excluded []peer.ID) ([]Candidate, bool, error) {
	if ctx == nil || server == "" || !validCountry(country) || limit < 1 || limit > MaxCandidates || len(excluded) > MaxExcludedPeers {
		return nil, false, ErrInvalid
	}
	request, err := NewRequestID(nil)
	if err != nil {
		return nil, false, err
	}
	excludeBytes := make([][]byte, 0, len(excluded))
	for _, value := range excluded {
		if value == "" {
			return nil, false, ErrInvalid
		}
		excludeBytes = append(excludeBytes, []byte(value))
	}
	response, err := c.exchange(ctx, server, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_SampleLocal{
		SampleLocal: &irviov1.RendezvousSampleLocal{
			ProtocolVersion: ProtocolVersion, Country: country, Limit: uint32(limit),
			ExcludePeerIds: excludeBytes, RequestId: append([]byte(nil), request[:]...),
		},
	}})
	if err != nil {
		return nil, false, err
	}
	wire, ok := response.Message.(*irviov1.RendezvousFrame_SampleResult)
	if !ok || wire.SampleResult.GetProtocolVersion() != ProtocolVersion ||
		wire.SampleResult.GetCountry() != country ||
		!bytes.Equal(wire.SampleResult.GetRequestId(), request[:]) ||
		len(wire.SampleResult.GetCandidates()) > limit {
		return nil, false, ErrInvalid
	}
	candidates := make([]Candidate, 0, len(wire.SampleResult.Candidates))
	for _, message := range wire.SampleResult.Candidates {
		candidate, err := CandidateFromProto(message, c.now())
		if err != nil {
			return nil, false, err
		}
		candidates = append(candidates, candidate)
	}
	candidates, err = MergeCandidates(candidates...)
	if err != nil {
		return nil, false, err
	}
	return candidates, wire.SampleResult.Partial, nil
}

func (c *Client) Signal(ctx context.Context, server peer.ID, signal *irviov1.RendezvousSignal) (*irviov1.RendezvousSignal, error) {
	if ctx == nil || server == "" {
		return nil, ErrInvalid
	}
	if _, _, _, err := validateSignal(signal, c.now()); err != nil || signal.Response {
		return nil, ErrInvalid
	}
	response, err := c.exchange(ctx, server, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Signal{Signal: signal}})
	if err != nil {
		return nil, err
	}
	wire, ok := response.Message.(*irviov1.RendezvousFrame_Signal)
	if !ok || !wire.Signal.GetResponse() ||
		!bytes.Equal(wire.Signal.GetRequestHandle(), signal.GetRequestHandle()) {
		return nil, ErrInvalid
	}
	return wire.Signal, nil
}

func (c *Client) exchange(ctx context.Context, server peer.ID, request *irviov1.RendezvousFrame) (*irviov1.RendezvousFrame, error) {
	deadline := time.Now().Add(c.timeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	stream, err := c.host.NewStream(ctx, server, libprotocol.ID(ProtocolID))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(deadline)
	if stream.Conn().RemotePeer() != server {
		_ = stream.Reset()
		return nil, ErrWrongPeer
	}
	if err := WriteFrame(stream, request); err != nil {
		_ = stream.Reset()
		return nil, err
	}
	response, err := ReadFrame(stream)
	if err != nil {
		_ = stream.Reset()
		return nil, err
	}
	if failure, ok := response.Message.(*irviov1.RendezvousFrame_Error); ok {
		return nil, errorFromWire(failure.Error)
	}
	return response, nil
}

func errorFromWire(message *irviov1.RendezvousError) error {
	if message == nil || message.ProtocolVersion != ProtocolVersion || len(message.Message) > MaxErrorMessageBytes {
		return ErrInvalid
	}
	switch message.Code {
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_BUSY:
		return ErrBusy
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_RATE_LIMITED:
		return ErrRateLimited
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_STALE:
		return ErrStale
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_CONFLICT:
		return ErrConflict
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_NOT_FOUND:
		return ErrNotFound
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_UNAUTHORIZED:
		return ErrUnauthorized
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_ROUTE_FAILED:
		return ErrRouteFailed
	case irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_TIMEOUT:
		return ErrTimeout
	default:
		return ErrInvalid
	}
}

type VerifiedForwarder struct {
	client *Client
	max    int
	mu     sync.Mutex
	routes map[string]verifiedForwardRoute
}

type verifiedForwardRoute struct {
	owner     OwnerRoute
	expiresAt time.Time
}

func NewVerifiedForwarder(client *Client, max int) (*VerifiedForwarder, error) {
	if client == nil || max < 1 || max > 1024 {
		return nil, ErrInvalid
	}
	return &VerifiedForwarder{client: client, max: max, routes: make(map[string]verifiedForwardRoute)}, nil
}

func (f *VerifiedForwarder) Remember(candidate Candidate) error {
	if err := validateCandidate(candidate); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	for key, route := range f.routes {
		if !route.expiresAt.After(now) {
			delete(f.routes, key)
		}
	}
	for _, route := range candidate.Routes {
		key := ownerKey(route.ServerPeer, route.ServerIncarnation, route.RegistrationID)
		if _, exists := f.routes[key]; !exists && len(f.routes) >= f.max {
			return ErrBusy
		}
		expiresAt := ownerRouteExpiry(candidate, route)
		if !expiresAt.After(now) {
			return ErrStale
		}
		f.routes[key] = verifiedForwardRoute{owner: cloneOwnerRoute(route), expiresAt: expiresAt}
		for _, address := range route.ControlAddresses {
			info, err := peer.AddrInfoFromString(address)
			if err != nil || info.ID != route.ServerPeer {
				return ErrWrongPeer
			}
			f.client.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
		}
	}
	return nil
}

func (f *VerifiedForwarder) ForwardSignal(ctx context.Context, owner OwnerRoute, signal *irviov1.RendezvousSignal) (*irviov1.RendezvousSignal, error) {
	f.mu.Lock()
	verified, exists := f.routes[ownerKey(owner.ServerPeer, owner.ServerIncarnation, owner.RegistrationID)]
	if exists && !verified.expiresAt.After(time.Now().UTC()) {
		delete(f.routes, ownerKey(owner.ServerPeer, owner.ServerIncarnation, owner.RegistrationID))
		exists = false
	}
	f.mu.Unlock()
	if !exists || verified.owner.ServerIncarnation != owner.ServerIncarnation ||
		verified.owner.RegistrationID != owner.RegistrationID {
		return nil, ErrUnauthorized
	}
	return f.client.Signal(ctx, owner.ServerPeer, signal)
}

func ownerRouteExpiry(candidate Candidate, route OwnerRoute) time.Time {
	expiresAt := time.Unix(candidate.Descriptor.Announcement.ExpiresAtUnix, 0).UTC()
	key := ownerKey(route.ServerPeer, route.ServerIncarnation, route.RegistrationID)
	for _, receipt := range candidate.Receipts {
		if ownerKey(receipt.Receipt.ServerPeer, receipt.Receipt.ServerIncarnation, receipt.Receipt.RegistrationID) == key &&
			receipt.Receipt.ExpiresAt.Before(expiresAt) {
			expiresAt = receipt.Receipt.ExpiresAt
		}
	}
	return expiresAt
}

var _ SignalForwarder = (*VerifiedForwarder)(nil)
