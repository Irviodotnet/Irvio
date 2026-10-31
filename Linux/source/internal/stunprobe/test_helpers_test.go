// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

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

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const testDeadline = 2 * time.Second

type testServerBehavior struct {
	dropFirst        int
	gate             <-chan struct{}
	duplicate        bool
	wrongTransaction bool
	malformed        bool
	missingMapped    bool
	errorResponse    bool
}

type observedRequest struct {
	source netip.AddrPort
	id     transactionID
}

type testSTUNServer struct {
	t         *testing.T
	conn      *net.UDPConn
	addr      netip.AddrPort
	behavior  testServerBehavior
	requests  chan observedRequest
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newTestSTUNServer(t *testing.T, family socketmux.AddressFamily, behavior testServerBehavior) *testSTUNServer {
	t.Helper()
	bind := netip.MustParseAddr("127.0.0.1")
	network := "udp4"
	if family == socketmux.IPv6 {
		requireIPv6(t)
		bind = netip.IPv6Loopback()
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(bind, 0)))
	if err != nil {
		t.Fatal(err)
	}
	server := &testSTUNServer{
		t: t, conn: conn, addr: conn.LocalAddr().(*net.UDPAddr).AddrPort(), behavior: behavior,
		requests: make(chan observedRequest, 1024), done: make(chan struct{}),
	}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(server.close)
	return server
}

func (s *testSTUNServer) serve() {
	defer s.wg.Done()
	buffer := make([]byte, socketmux.MaxUDPPayloadSize)
	requestNumber := 0
	for {
		n, source, err := s.conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.t.Errorf("test STUN server read: %v", err)
			return
		}
		message := stun.NewWithOptions(stun.WithStrict(true))
		if err := message.UnmarshalBinary(buffer[:n]); err != nil || message.Type != stun.BindingRequest {
			continue
		}
		requestNumber++
		observed := observedRequest{source: source, id: transactionID(message.TransactionID)}
		select {
		case s.requests <- observed:
		case <-s.done:
			return
		}
		if requestNumber <= s.behavior.dropFirst {
			continue
		}
		if s.behavior.gate != nil {
			select {
			case <-s.behavior.gate:
			case <-s.done:
				return
			}
		}
		if err := s.respond(observed); err != nil && !errors.Is(err, net.ErrClosed) {
			s.t.Errorf("test STUN server respond: %v", err)
			return
		}
		if s.behavior.duplicate {
			if err := s.respond(observed); err != nil && !errors.Is(err, net.ErrClosed) {
				s.t.Errorf("test STUN server duplicate: %v", err)
				return
			}
		}
	}
}

func (s *testSTUNServer) respond(request observedRequest) error {
	id := request.id
	if s.behavior.wrongTransaction {
		id[0] ^= 0xff
	}
	if s.behavior.malformed {
		payload := malformedAttributeResponse(id)
		_, err := s.conn.WriteToUDPAddrPort(payload, request.source)
		return err
	}
	message, err := buildResponse(id, request.source, s.behavior.missingMapped, s.behavior.errorResponse)
	if err != nil {
		return err
	}
	_, err = s.conn.WriteToUDPAddrPort(message.Raw, request.source)
	return err
}

func (s *testSTUNServer) sendSuccess(request observedRequest) error {
	message, err := buildResponse(request.id, request.source, false, false)
	if err != nil {
		return err
	}
	_, err = s.conn.WriteToUDPAddrPort(message.Raw, request.source)
	return err
}

func (s *testSTUNServer) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.wg.Wait()
	})
}

func buildResponse(id transactionID, mapped netip.AddrPort, missingMapped, errorResponse bool) (*stun.Message, error) {
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = id
	message.WriteTransactionID()
	if errorResponse {
		message.SetType(stun.BindingError)
		attribute := stun.ErrorCodeAttribute{Code: stun.CodeBadRequest, Reason: []byte("bad request")}
		if err := attribute.AddTo(message); err != nil {
			return nil, err
		}
		return message, nil
	}
	if !missingMapped {
		attribute := stun.XORMappedAddress{IP: net.IP(mapped.Addr().AsSlice()), Port: int(mapped.Port())}
		if err := attribute.AddTo(message); err != nil {
			return nil, err
		}
	}
	return message, nil
}

func malformedAttributeResponse(id transactionID) []byte {
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = id
	message.WriteTransactionID()
	payload := append([]byte(nil), message.Raw...)
	payload = append(payload, 0, byte(stun.AttrXORMappedAddress), 0, 4)
	binary.BigEndian.PutUint16(payload[2:4], 4)
	return payload
}

func newTestStack(t *testing.T, dualStack bool, registryConfig RegistryConfig, clientConfig Config) (*Registry, *socketmux.Mux, *Client) {
	t.Helper()
	registry, err := NewRegistry(registryConfig)
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	if dualStack {
		requireIPv6(t)
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = netip.IPv6Loopback()
	}
	muxConfig.Classifier = NewClassifier(registry)
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(mux.Control(), registry, clientConfig)
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = mux.Close()
	})
	return registry, mux, client
}

func fastClientConfig() Config {
	return Config{InitialRTO: 20 * time.Millisecond, MaxRTO: 40 * time.Millisecond, MaxAttempts: 3}
}

func testRegistryConfig() RegistryConfig {
	return RegistryConfig{MaxInFlight: 64, MaxRetired: 128, RetiredGrace: time.Second}
}

func requireIPv6(t *testing.T) {
	t.Helper()
	conn, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), 0)))
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	_ = conn.Close()
}

func nextRequest(t *testing.T, server *testSTUNServer) observedRequest {
	t.Helper()
	select {
	case request := <-server.requests:
		return request
	case <-time.After(testDeadline):
		t.Fatal("timed out waiting for STUN request")
		return observedRequest{}
	}
}

func readData(t *testing.T, mux *socketmux.Mux) socketmux.Datagram {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()
	datagram, err := mux.Data().Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return datagram
}

func assertNoData(t *testing.T, mux *socketmux.Mux) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if datagram, err := mux.Data().Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected Data datagram %+v, error %v", datagram, err)
	}
}
