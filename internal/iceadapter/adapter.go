// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

var _ ice.UDPMux = (*Adapter)(nil)

// Adapter implements Pion's ice.UDPMux using one Irvio Control endpoint.
type Adapter struct {
	endpoint *socketmux.Endpoint
	registry *Registry
	config   Config
	logger   *slog.Logger

	listen       map[socketmux.AddressFamily]netip.AddrPort
	listenAddrs  []net.Addr
	mu           sync.Mutex
	conns        map[connKey]*connState
	closed       atomic.Bool
	context      context.Context
	cancel       context.CancelFunc
	dispatcher   sync.WaitGroup
	closeOnce    sync.Once
	shutdownOnce sync.Once
}

// New starts one Control dispatcher while leaving endpoint and mux ownership with the caller.
func New(endpoint *socketmux.Endpoint, registry *Registry, config Config) (*Adapter, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("create ICE adapter: nil Control endpoint")
	}
	if registry == nil {
		return nil, fmt.Errorf("create ICE adapter: nil registry")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create ICE adapter: %w", err)
	}
	listen, listenAddrs, err := validateListenAddresses(endpoint, config.ListenAddresses)
	if err != nil {
		return nil, fmt.Errorf("create ICE adapter: %w", err)
	}
	if err := registry.attach(); err != nil {
		return nil, fmt.Errorf("create ICE adapter: %w", err)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &Adapter{
		endpoint: endpoint, registry: registry, config: config, logger: logger,
		listen: listen, listenAddrs: listenAddrs, conns: make(map[connKey]*connState),
		context: ctx, cancel: cancel,
	}
	adapter.dispatcher.Add(1)
	go adapter.dispatch()
	logger.Info("ICE_ADAPTER_STARTED", "port", endpoint.Port(), "listen_addresses", len(listenAddrs))
	return adapter, nil
}

// GetConn returns a logical PacketConn for one local ufrag and configured address.
func (a *Adapter) GetConn(ufrag string, addr net.Addr) (net.PacketConn, error) {
	if a.closed.Load() {
		return nil, ErrClosed
	}
	if !validUfrag(ufrag) {
		return nil, ErrInvalidUfrag
	}
	local, family, err := addrPortFromNetAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("get ICE connection: %w", err)
	}
	configured, exists := a.listen[family]
	if !exists || configured != local {
		return nil, fmt.Errorf("get ICE connection: local address %s is not configured", local)
	}
	key := connKey{ufrag: ufrag, family: family}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return nil, ErrClosed
	}
	state := a.conns[key]
	if state == nil {
		if err := a.registry.registerUfrag(ufrag, family); err != nil {
			return nil, fmt.Errorf("get ICE connection: %w", err)
		}
		state = newConnState(a, key, local, a.config.ConnectionQueueCapacity)
		a.conns[key] = state
	}
	state.refs++
	return newPacketConn(state), nil
}

// RemoveConnByUfrag forcefully removes all logical family connections for ufrag.
func (a *Adapter) RemoveConnByUfrag(ufrag string) {
	var removed []*connState
	a.mu.Lock()
	for key, state := range a.conns {
		if key.ufrag == ufrag {
			delete(a.conns, key)
			removed = append(removed, state)
		}
	}
	a.mu.Unlock()
	for _, state := range removed {
		state.closeWith(ErrClosed)
	}
	a.registry.removeUfrag(ufrag, time.Now())
}

// GetListenAddresses returns copies of explicit host addresses at Mux.Port().
func (a *Adapter) GetListenAddresses() []net.Addr {
	result := make([]net.Addr, 0, len(a.listenAddrs))
	for _, address := range a.listenAddrs {
		udp := address.(*net.UDPAddr)
		result = append(result, &net.UDPAddr{IP: append(net.IP(nil), udp.IP...), Port: udp.Port, Zone: udp.Zone})
	}
	return result
}

// Stats returns a nonblocking, race-safe activity snapshot.
func (a *Adapter) Stats() Stats {
	ufrags, active, retired := a.registry.sizes()
	c := &a.registry.stats
	return Stats{
		UfragsRegistered: c.ufragsRegistered.Load(), UfragsRemoved: c.ufragsRemoved.Load(),
		ActiveUfrags: ufrags, PeakActiveUfrags: c.peakActiveUfrags.Load(),
		ActiveTransactions: active, RetiredTransactions: retired,
		OutboundICERequestsObserved:    c.outboundICERequestsObserved.Load(),
		OutboundTransactionsRegistered: c.outboundTransactionsRegistered.Load(),
		InboundRequestsRouted:          c.inboundRequestsRouted.Load(),
		InboundResponsesRouted:         c.inboundResponsesRouted.Load(),
		UnknownUfragPackets:            c.unknownUfragPackets.Load(),
		UnknownTransactions:            c.unknownTransactions.Load(),
		WrongSourceResponses:           c.wrongSourceResponses.Load(),
		WrongFamilyResponses:           c.wrongFamilyResponses.Load(),
		RetiredResponsesDropped:        c.retiredResponsesDropped.Load(),
		LogicalQueueDrops:              c.logicalQueueDrops.Load(),
		UnsupportedOutboundPayloads:    c.unsupportedOutboundPayloads.Load(),
		DispatcherErrors:               c.dispatcherErrors.Load(),
		MalformedSTUNLikePackets:       c.malformedSTUNLikePackets.Load(),
	}
}

// Close stops ICE routing without closing IrvioSocketMux.
func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		a.cancel()
		a.dispatcher.Wait()
		a.shutdown(ErrClosed)
	})
	return nil
}

func (a *Adapter) dispatch() {
	defer a.dispatcher.Done()
	terminal := error(ErrClosed)
	defer func() { a.shutdown(terminal) }()
	for {
		datagram, err := a.endpoint.Read(a.context)
		if err != nil {
			if a.context.Err() != nil {
				return
			}
			if errors.Is(err, socketmux.ErrClosed) {
				terminal = fmt.Errorf("%w: %v", ErrTransportClosed, err)
				return
			}
			a.registry.stats.dispatcherErrors.Add(1)
			terminal = fmt.Errorf("%w: read Control endpoint: %v", ErrTransportClosed, err)
			return
		}
		a.route(datagram)
	}
}

func (a *Adapter) route(datagram socketmux.Datagram) {
	if a.config.STUNResponseHandler != nil && a.config.STUNResponseHandler(datagram) {
		return
	}
	message, status := decodeSTUN(datagram.Payload)
	if status != decodeValid || message.Type.Method != stun.MethodBinding {
		a.registry.stats.dispatcherErrors.Add(1)
		return
	}
	var ufrag string
	switch message.Type.Class {
	case stun.ClassRequest:
		local, _, ok := usernameParts(message)
		if !ok {
			a.registry.stats.dispatcherErrors.Add(1)
			return
		}
		ufrag = local
	case stun.ClassSuccessResponse, stun.ClassErrorResponse:
		var ok bool
		ufrag, ok = a.registry.responseOwner(transactionID(message.TransactionID), datagram.Source, datagram.Family)
		if !ok {
			a.registry.stats.dispatcherErrors.Add(1)
			return
		}
	default:
		a.registry.stats.dispatcherErrors.Add(1)
		return
	}
	key := connKey{ufrag: ufrag, family: datagram.Family}
	a.mu.Lock()
	state := a.conns[key]
	a.mu.Unlock()
	if state == nil {
		a.registry.stats.unknownUfragPackets.Add(1)
		return
	}
	packet := inboundPacket{payload: datagram.Payload, source: datagram.Source}
	select {
	case <-state.done:
		a.registry.stats.logicalQueueDrops.Add(1)
	case state.queue <- packet:
		if message.Type.Class == stun.ClassRequest {
			a.registry.stats.inboundRequestsRouted.Add(1)
		} else {
			a.registry.stats.inboundResponsesRouted.Add(1)
		}
	default:
		a.registry.stats.logicalQueueDrops.Add(1)
	}
}

func (a *Adapter) release(state *connState) {
	a.mu.Lock()
	current := a.conns[state.key]
	if current != state {
		a.mu.Unlock()
		return
	}
	state.refs--
	if state.refs > 0 {
		a.mu.Unlock()
		return
	}
	delete(a.conns, state.key)
	a.mu.Unlock()
	state.closeWith(ErrClosed)
	a.registry.releaseUfragFamily(state.key.ufrag, state.key.family, true, time.Now())
}

func (a *Adapter) shutdown(reason error) {
	a.shutdownOnce.Do(func() {
		a.closed.Store(true)
		a.cancel()
		a.mu.Lock()
		states := make([]*connState, 0, len(a.conns))
		for key, state := range a.conns {
			delete(a.conns, key)
			states = append(states, state)
		}
		a.mu.Unlock()
		for _, state := range states {
			state.closeWith(reason)
		}
		a.registry.close(time.Now())
		a.logger.Info("ICE_ADAPTER_CLOSED", "reason", reason)
	})
}

func validateListenAddresses(endpoint *socketmux.Endpoint, addresses []netip.Addr) (map[socketmux.AddressFamily]netip.AddrPort, []net.Addr, error) {
	listen := make(map[socketmux.AddressFamily]netip.AddrPort, 2)
	result := make([]net.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
			return nil, nil, fmt.Errorf("listen address %s must be concrete unicast native IP", address)
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		family := socketmux.IPv6
		if address.Is4() {
			family = socketmux.IPv4
		}
		if _, duplicateFamily := listen[family]; duplicateFamily {
			return nil, nil, fmt.Errorf("multiple %s listen addresses are unsupported in M5a", family)
		}
		physical, active := endpoint.LocalAddr(family)
		if !active {
			return nil, nil, fmt.Errorf("%s listen address has no active mux socket", family)
		}
		if !physical.Addr().IsUnspecified() && physical.Addr() != address {
			return nil, nil, fmt.Errorf("listen address %s differs from mux bind %s", address, physical.Addr())
		}
		local := netip.AddrPortFrom(address, endpoint.Port())
		listen[family] = local
		result = append(result, net.UDPAddrFromAddrPort(local))
	}
	if len(result) == 0 {
		return nil, nil, fmt.Errorf("no unique listen addresses")
	}
	return listen, result, nil
}

func addrPortFromNetAddr(address net.Addr) (netip.AddrPort, socketmux.AddressFamily, error) {
	udp, ok := address.(*net.UDPAddr)
	if !ok || udp == nil {
		return netip.AddrPort{}, 0, fmt.Errorf("address type %T is not UDP", address)
	}
	if udp.Port <= 0 || udp.Port > 65535 {
		return netip.AddrPort{}, 0, fmt.Errorf("invalid UDP address %v", udp)
	}
	endpoint := udp.AddrPort()
	addr := endpoint.Addr()
	if !addr.IsValid() || addr.Is4In6() || addr.IsUnspecified() || addr.IsMulticast() {
		return netip.AddrPort{}, 0, fmt.Errorf("ambiguous or non-unicast UDP address %v", udp)
	}
	family := socketmux.IPv6
	if addr.Is4() {
		family = socketmux.IPv4
	}
	return endpoint, family, nil
}
