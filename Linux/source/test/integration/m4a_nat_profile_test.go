// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestM4AIPv4RFC5780ProfileUsesMuxPort(t *testing.T) {
	server := newM4ATestServer(t, m4aServerBehavior{
		mapping: natprobe.MappingEndpointIndependent, filtering: natprobe.FilteringEndpointIndependent,
	})
	registry, err := stunprobe.NewRegistry(stunprobe.RegistryConfig{
		MaxInFlight: 16, MaxRetired: 64, RetiredGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = stunprobe.NewClassifier(registry)
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := stunprobe.NewClient(mux.Control(), registry, stunprobe.Config{
		InitialRTO: 20 * time.Millisecond, MaxRTO: 40 * time.Millisecond, MaxAttempts: 3,
	})
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	profiler, err := natprobe.New(client, natprobe.Config{MinimumTransactionInterval: time.Millisecond, MaxObservations: 16})
	if err != nil {
		_ = client.Close()
		_ = mux.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = profiler.Close()
		_ = client.Close()
		_ = mux.Close()
	})

	profile, err := profiler.Profile(context.Background(), server.primary)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Status != natprobe.ProfileComplete || profile.Mapping != natprobe.MappingEndpointIndependent ||
		profile.Filtering != natprobe.FilteringEndpointIndependent || profile.Translation != natprobe.TranslationPresent {
		t.Fatalf("unexpected M4a profile: %+v", profile)
	}
	if profile.LocalEndpoint.Port() != mux.Port() || profile.InitialMappedEndpoint.Port() != mux.Port() {
		t.Fatalf("profile ports local=%s mapped=%s mux=%d", profile.LocalEndpoint, profile.InitialMappedEndpoint, mux.Port())
	}

	requests := server.snapshot()
	if len(requests) < 3 {
		t.Fatalf("too few RFC5780 requests: %+v", requests)
	}
	for _, request := range requests {
		if request.source.Port() != mux.Port() {
			t.Fatalf("server observed source %s, mux port %d", request.source, mux.Port())
		}
	}
	if requests[0].destination != server.primary || requests[0].changeFlags != 0 {
		t.Fatalf("first request was not baseline: %+v", requests)
	}
	if requests[1].destination != server.primary || requests[1].changeFlags != m4aChangeIPPort {
		t.Fatalf("second request was not filtering Test II: %+v", requests)
	}
	for index := 0; index < 2; index++ {
		if requests[index].destination != server.primary {
			t.Fatalf("mapping preceded filtering: %+v", requests)
		}
	}
	if requests[2].destination != server.alternateIPPrimaryPort() {
		t.Fatalf("mapping Test II destination = %s", requests[2].destination)
	}

	unsupportedServer := newM4ATestServer(t, m4aServerBehavior{
		mapping: natprobe.MappingEndpointIndependent, filtering: natprobe.FilteringEndpointIndependent, unknownChangeRequest: true,
	})
	unsupported, err := profiler.Profile(context.Background(), unsupportedServer.primary)
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.Status != natprobe.ProfilePartial || unsupported.Filtering != natprobe.FilteringUnknown ||
		unsupported.Mapping != natprobe.MappingEndpointIndependent || unsupported.Reason != natprobe.ReasonServerUnsupported {
		t.Fatalf("unexpected 420 profile: %+v", unsupported)
	}
}

func TestM4AChangeRequestDecoderIsBounded(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
		want  uint32
		ok    bool
	}{
		{name: "absent", want: 0, ok: true},
		{name: "change port", value: []byte{0, 0, 0, 2}, want: m4aChangePort, ok: true},
		{name: "change IP and port", value: []byte{0, 0, 0, 6}, want: m4aChangeIPPort, ok: true},
		{name: "wrong length", value: []byte{0, 0, 6}},
		{name: "unknown bits", value: []byte{0, 0, 0, 4}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
			if test.value != nil {
				message.Add(stun.AttrChangeRequest, test.value)
			}
			got, err := decodeM4AChangeRequest(message)
			if (err == nil) != test.ok || got != test.want {
				t.Fatalf("decode = %#x, %v; want %#x, ok=%v", got, err, test.want, test.ok)
			}
		})
	}
}

const (
	m4aChangePort   uint32 = 0x02
	m4aChangeIPPort uint32 = 0x06
)

type m4aServerBehavior struct {
	mapping               natprobe.MappingBehavior
	filtering             natprobe.FilteringBehavior
	missingOther          bool
	missingResponseOrigin bool
	mappedMismatch        bool
	unknownChangeRequest  bool
	dropFirst             int
	duplicate             bool
	delay                 time.Duration
}

type m4aObservedRequest struct {
	source      netip.AddrPort
	destination netip.AddrPort
	transaction [stun.TransactionIDSize]byte
	changeFlags uint32
}

type m4aTestServer struct {
	t        *testing.T
	primary  netip.AddrPort
	other    netip.AddrPort
	sockets  map[netip.AddrPort]*net.UDPConn
	behavior m4aServerBehavior
	done     chan struct{}
	close    sync.Once
	wg       sync.WaitGroup

	mu       sync.Mutex
	requests []m4aObservedRequest
	count    int
}

func newM4ATestServer(t *testing.T, behavior m4aServerBehavior) *m4aTestServer {
	t.Helper()
	var opened []*net.UDPConn
	constructionComplete := false
	defer func() {
		if !constructionComplete {
			for _, conn := range opened {
				_ = conn.Close()
			}
		}
	}()
	firstAddress := netip.MustParseAddr("127.0.0.1")
	secondAddress := netip.MustParseAddr("127.0.0.2")
	primaryConn := listenM4A(t, firstAddress, 0)
	opened = append(opened, primaryConn)
	primary := primaryConn.LocalAddr().(*net.UDPAddr).AddrPort()
	secondPrimaryConn := listenM4A(t, secondAddress, primary.Port())
	opened = append(opened, secondPrimaryConn)

	var firstAlternateConn *net.UDPConn
	for attempt := 0; attempt < 8; attempt++ {
		candidate := listenM4A(t, firstAddress, 0)
		if candidate.LocalAddr().(*net.UDPAddr).AddrPort().Port() != primary.Port() {
			firstAlternateConn = candidate
			opened = append(opened, candidate)
			break
		}
		_ = candidate.Close()
	}
	if firstAlternateConn == nil {
		_ = primaryConn.Close()
		_ = secondPrimaryConn.Close()
		t.Fatal("could not allocate a distinct RFC5780 alternate port")
	}
	firstAlternate := firstAlternateConn.LocalAddr().(*net.UDPAddr).AddrPort()
	secondAlternateConn := listenM4A(t, secondAddress, firstAlternate.Port())
	opened = append(opened, secondAlternateConn)
	other := secondAlternateConn.LocalAddr().(*net.UDPAddr).AddrPort()

	server := &m4aTestServer{
		t: t, primary: primary, other: other, behavior: behavior, done: make(chan struct{}),
		sockets: map[netip.AddrPort]*net.UDPConn{
			primary: primaryConn,
			secondPrimaryConn.LocalAddr().(*net.UDPAddr).AddrPort(): secondPrimaryConn,
			firstAlternate: firstAlternateConn,
			other:          secondAlternateConn,
		},
	}
	for destination, conn := range server.sockets {
		server.wg.Add(1)
		go server.serve(destination, conn)
	}
	t.Cleanup(server.Close)
	constructionComplete = true
	return server
}

func listenM4A(t *testing.T, address netip.Addr, port uint16) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, port)))
	if err != nil {
		t.Fatalf("bind RFC5780 test socket %s:%d: %v", address, port, err)
	}
	return conn
}

func (s *m4aTestServer) serve(destination netip.AddrPort, conn *net.UDPConn) {
	defer s.wg.Done()
	buffer := make([]byte, socketmux.MaxUDPPayloadSize)
	for {
		n, source, err := conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.t.Errorf("RFC5780 test read: %v", err)
			}
			return
		}
		message := stun.NewWithOptions(stun.WithStrict(true))
		if err := message.UnmarshalBinary(buffer[:n]); err != nil || message.Type != stun.BindingRequest {
			continue
		}
		flags, err := decodeM4AChangeRequest(message)
		if err != nil {
			continue
		}
		request := m4aObservedRequest{source: source, destination: destination, transaction: message.TransactionID, changeFlags: flags}
		s.mu.Lock()
		s.requests = append(s.requests, request)
		s.count++
		count := s.count
		s.mu.Unlock()
		if count <= s.behavior.dropFirst || !s.filterAllows(flags) {
			continue
		}
		if s.behavior.delay > 0 {
			timer := time.NewTimer(s.behavior.delay)
			select {
			case <-timer.C:
			case <-s.done:
				timer.Stop()
				return
			}
		}
		if s.behavior.unknownChangeRequest && flags != 0 {
			if err := s.sendError(s.sockets[destination], request, stun.CodeUnknownAttribute); err != nil && !errors.Is(err, net.ErrClosed) {
				s.t.Errorf("RFC5780 error response: %v", err)
			}
			continue
		}
		responseConn := s.responseSocket(destination, flags)
		if err := s.sendSuccess(responseConn, request); err != nil && !errors.Is(err, net.ErrClosed) {
			s.t.Errorf("RFC5780 success response: %v", err)
			return
		}
		if s.behavior.duplicate {
			if err := s.sendSuccess(responseConn, request); err != nil && !errors.Is(err, net.ErrClosed) {
				s.t.Errorf("RFC5780 duplicate response: %v", err)
				return
			}
		}
	}
}

func (s *m4aTestServer) responseSocket(destination netip.AddrPort, flags uint32) *net.UDPConn {
	if flags == m4aChangeIPPort {
		return s.sockets[s.other]
	}
	if flags == m4aChangePort {
		return s.sockets[netip.AddrPortFrom(s.primary.Addr(), s.other.Port())]
	}
	return s.sockets[destination]
}

func (s *m4aTestServer) filterAllows(flags uint32) bool {
	if flags == m4aChangeIPPort {
		return s.behavior.filtering == natprobe.FilteringEndpointIndependent
	}
	if flags == m4aChangePort {
		return s.behavior.filtering != natprobe.FilteringAddressAndPortDependent
	}
	return true
}

func (s *m4aTestServer) sendSuccess(conn *net.UDPConn, request m4aObservedRequest) error {
	origin := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	mapped := s.mappedFor(request.destination, request.source.Port())
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = request.transaction
	message.WriteTransactionID()
	legacyMapped := mapped
	if s.behavior.mappedMismatch {
		legacyMapped = netip.AddrPortFrom(mapped.Addr(), mapped.Port()+1)
	}
	attributes := []stun.Setter{
		&stun.XORMappedAddress{IP: net.IP(mapped.Addr().AsSlice()), Port: int(mapped.Port())},
		&stun.MappedAddress{IP: net.IP(legacyMapped.Addr().AsSlice()), Port: int(legacyMapped.Port())},
	}
	if !s.behavior.missingOther {
		attributes = append(attributes, &stun.OtherAddress{IP: net.IP(s.other.Addr().AsSlice()), Port: int(s.other.Port())})
	}
	if !s.behavior.missingResponseOrigin {
		attributes = append(attributes, &stun.ResponseOrigin{IP: net.IP(origin.Addr().AsSlice()), Port: int(origin.Port())})
	}
	for _, attribute := range attributes {
		if err := attribute.AddTo(message); err != nil {
			return err
		}
	}
	_, err := conn.WriteToUDPAddrPort(message.Raw, request.source)
	return err
}

func (s *m4aTestServer) sendError(conn *net.UDPConn, request m4aObservedRequest, code stun.ErrorCode) error {
	message := stun.MustBuild(stun.BindingError)
	message.TransactionID = request.transaction
	message.WriteTransactionID()
	if err := (stun.ErrorCodeAttribute{Code: code, Reason: []byte("scripted")}).AddTo(message); err != nil {
		return err
	}
	_, err := conn.WriteToUDPAddrPort(message.Raw, request.source)
	return err
}

func (s *m4aTestServer) mappedFor(destination netip.AddrPort, port uint16) netip.AddrPort {
	mappedPort := port
	if destination == s.alternateIPPrimaryPort() &&
		(s.behavior.mapping == natprobe.MappingAddressDependent || s.behavior.mapping == natprobe.MappingAddressAndPortDependent) {
		mappedPort++
	}
	if destination == s.other {
		if s.behavior.mapping == natprobe.MappingAddressDependent {
			mappedPort++
		} else if s.behavior.mapping == natprobe.MappingAddressAndPortDependent {
			mappedPort += 2
		}
	}
	return netip.AddrPortFrom(netip.MustParseAddr("198.51.100.44"), mappedPort)
}

func (s *m4aTestServer) alternateIPPrimaryPort() netip.AddrPort {
	return netip.AddrPortFrom(s.other.Addr(), s.primary.Port())
}

func (s *m4aTestServer) snapshot() []m4aObservedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]m4aObservedRequest(nil), s.requests...)
}

func (s *m4aTestServer) Close() {
	s.close.Do(func() {
		close(s.done)
		for _, conn := range s.sockets {
			_ = conn.Close()
		}
		s.wg.Wait()
	})
}

func decodeM4AChangeRequest(message *stun.Message) (uint32, error) {
	value, err := message.Get(stun.AttrChangeRequest)
	if errors.Is(err, stun.ErrAttributeNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(value) != 4 {
		return 0, errors.New("invalid CHANGE-REQUEST length")
	}
	flags := binary.BigEndian.Uint32(value)
	if flags != m4aChangePort && flags != m4aChangeIPPort {
		return 0, errors.New("unsupported CHANGE-REQUEST flags")
	}
	return flags, nil
}
