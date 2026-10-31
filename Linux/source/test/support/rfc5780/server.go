// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package rfc5780 is a four-socket lab responder and must never be linked into hydrad.
package rfc5780

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/pion/stun/v3"
)

const (
	changePortFlag   uint32 = 0x02
	changeIPPortFlag uint32 = 0x06
	defaultLimit            = 4096
	maximumLimit            = 65536
	maximumDatagram         = 65507
	stunHeaderSize          = 20
)

// ChangeRequest identifies the supported RFC 5780 response-source request.
type ChangeRequest uint8

const (
	ChangeNone ChangeRequest = iota
	ChangePort
	ChangeIPAndPort
)

// Script controls responder faults; model firewall behavior in the namespace lab.
type Script struct {
	DropFirst            int
	DropChangePort       bool
	DropChangeIPAndPort  bool
	UnknownChangeRequest bool
	ResponseDelay        time.Duration
	BaselineReadyFile    string
	BaselineContinueFile string
}

// Config defines two real addresses and two real UDP ports.
type Config struct {
	PrimaryIP        netip.Addr
	OtherIP          netip.Addr
	PrimaryPort      uint16
	OtherPort        uint16
	ObservationLimit int
	Script           Script
	ObservationHook  func(Observation)
}

// Topology identifies the four bound responder endpoints.
type Topology struct {
	Primary                  netip.AddrPort `json:"primary"`
	Other                    netip.AddrPort `json:"other"`
	AlternateIPPrimaryPort   netip.AddrPort `json:"alternate_ip_primary_port"`
	PrimaryIPAlternatePort   netip.AddrPort `json:"primary_ip_alternate_port"`
	AlternateIPAlternatePort netip.AddrPort `json:"alternate_ip_alternate_port"`
}

// Observation is bounded evidence recorded for one valid Binding request.
type Observation struct {
	Sequence       uint64         `json:"sequence"`
	ReceivedAt     time.Time      `json:"received_at"`
	Source         netip.AddrPort `json:"source"`
	Destination    netip.AddrPort `json:"destination"`
	ResponseSource netip.AddrPort `json:"response_source,omitempty"`
	TransactionID  string         `json:"transaction_id"`
	ChangeRequest  ChangeRequest  `json:"change_request"`
	Outcome        string         `json:"outcome"`
}

// Server owns four UDP sockets for one test topology.
type Server struct {
	config   Config
	topology Topology
	sockets  map[netip.AddrPort]*net.UDPConn
	done     chan struct{}
	close    sync.Once
	wg       sync.WaitGroup

	mu           sync.Mutex
	observations []Observation
	sequence     uint64
	requestCount int
	changed      chan struct{}
	err          error
	baselineOnce sync.Once
}

// New atomically binds all four responder sockets and starts one reader per socket.
func New(config Config) (*Server, error) {
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("validate RFC5780 responder: %w", err)
	}
	primary := netip.AddrPortFrom(config.PrimaryIP, config.PrimaryPort)
	other := netip.AddrPortFrom(config.OtherIP, config.OtherPort)
	topology := Topology{
		Primary:                  primary,
		Other:                    other,
		AlternateIPPrimaryPort:   netip.AddrPortFrom(config.OtherIP, config.PrimaryPort),
		PrimaryIPAlternatePort:   netip.AddrPortFrom(config.PrimaryIP, config.OtherPort),
		AlternateIPAlternatePort: other,
	}
	server := &Server{
		config:       config,
		topology:     topology,
		sockets:      make(map[netip.AddrPort]*net.UDPConn, 4),
		done:         make(chan struct{}),
		changed:      make(chan struct{}, 1),
		observations: make([]Observation, 0, min(config.ObservationLimit, 64)),
	}
	for _, endpoint := range []netip.AddrPort{
		topology.Primary,
		topology.PrimaryIPAlternatePort,
		topology.AlternateIPPrimaryPort,
		topology.AlternateIPAlternatePort,
	} {
		conn, err := listen(endpoint)
		if err != nil {
			server.closeSockets()
			return nil, fmt.Errorf("bind RFC5780 endpoint %s: %w", endpoint, err)
		}
		server.sockets[endpoint] = conn
	}
	for endpoint, conn := range server.sockets {
		server.wg.Add(1)
		go server.serve(endpoint, conn)
	}
	return server, nil
}

func validateConfig(config *Config) error {
	if !validAddress(config.PrimaryIP) || !validAddress(config.OtherIP) {
		return errors.New("addresses must be valid native unicast addresses")
	}
	if config.PrimaryIP.BitLen() != config.OtherIP.BitLen() {
		return errors.New("addresses must use the same family")
	}
	if config.PrimaryIP == config.OtherIP {
		return errors.New("primary and other addresses must differ")
	}
	if config.PrimaryPort == 0 || config.OtherPort == 0 || config.PrimaryPort == config.OtherPort {
		return errors.New("primary and other ports must be distinct and nonzero")
	}
	if config.ObservationLimit == 0 {
		config.ObservationLimit = defaultLimit
	}
	if config.ObservationLimit < 1 || config.ObservationLimit > maximumLimit {
		return fmt.Errorf("observation limit must be within 1..%d", maximumLimit)
	}
	if config.Script.DropFirst < 0 {
		return errors.New("drop-first count cannot be negative")
	}
	if config.Script.ResponseDelay < 0 {
		return errors.New("response delay cannot be negative")
	}
	if (config.Script.BaselineReadyFile == "") != (config.Script.BaselineContinueFile == "") {
		return errors.New("baseline barrier requires both ready and continue files")
	}
	return nil
}

func validAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsMulticast() && !address.Is4In6()
}

func listen(endpoint netip.AddrPort) (*net.UDPConn, error) {
	network := "udp6"
	if endpoint.Addr().Is4() {
		network = "udp4"
	}
	return net.ListenUDP(network, net.UDPAddrFromAddrPort(endpoint))
}

func (s *Server) serve(destination netip.AddrPort, conn *net.UDPConn) {
	defer s.wg.Done()
	buffer := make([]byte, maximumDatagram)
	for {
		n, source, err := conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.setError(fmt.Errorf("read RFC5780 socket %s: %w", destination, err))
			}
			return
		}
		message, err := decodeRequest(buffer[:n])
		if err != nil {
			continue
		}
		change, err := decodeChangeRequest(message)
		if err != nil {
			continue
		}
		s.handle(conn, destination, source, message, change)
	}
}

func decodeRequest(payload []byte) (*stun.Message, error) {
	if len(payload) < stunHeaderSize || !stun.IsMessage(payload) {
		return nil, errors.New("not STUN")
	}
	if stunHeaderSize+int(binary.BigEndian.Uint16(payload[2:4])) != len(payload) {
		return nil, errors.New("invalid STUN framing")
	}
	message := stun.NewWithOptions(stun.WithStrict(true))
	if err := message.UnmarshalBinary(payload); err != nil {
		return nil, err
	}
	if message.Type != stun.BindingRequest {
		return nil, errors.New("not a Binding request")
	}
	return message, nil
}

func decodeChangeRequest(message *stun.Message) (ChangeRequest, error) {
	value, err := message.Get(stun.AttrChangeRequest)
	if errors.Is(err, stun.ErrAttributeNotFound) {
		return ChangeNone, nil
	}
	if err != nil {
		return ChangeNone, err
	}
	if len(value) != 4 {
		return ChangeNone, errors.New("CHANGE-REQUEST must contain exactly four bytes")
	}
	switch binary.BigEndian.Uint32(value) {
	case changePortFlag:
		return ChangePort, nil
	case changeIPPortFlag:
		return ChangeIPAndPort, nil
	default:
		return ChangeNone, errors.New("CHANGE-REQUEST contains unsupported flags")
	}
}

func (s *Server) handle(receiveConn *net.UDPConn, destination, source netip.AddrPort, request *stun.Message, change ChangeRequest) {
	observation := Observation{
		ReceivedAt: time.Now().UTC(), Source: source, Destination: destination,
		TransactionID: hex.EncodeToString(request.TransactionID[:]), ChangeRequest: change,
	}
	s.mu.Lock()
	s.requestCount++
	requestNumber := s.requestCount
	s.sequence++
	observation.Sequence = s.sequence
	s.mu.Unlock()

	drop := requestNumber <= s.config.Script.DropFirst ||
		(change == ChangePort && s.config.Script.DropChangePort) ||
		(change == ChangeIPAndPort && s.config.Script.DropChangeIPAndPort)
	if drop {
		observation.Outcome = "dropped_by_script"
		s.record(observation)
		return
	}
	if s.config.Script.UnknownChangeRequest && change != ChangeNone {
		observation.ResponseSource = destination
		observation.Outcome = "binding_error_420"
		s.record(observation)
		if err := sendError(receiveConn, source, request.TransactionID, stun.CodeUnknownAttribute); err != nil && !errors.Is(err, net.ErrClosed) {
			s.setError(err)
		}
		return
	}
	if !s.delay() {
		return
	}
	responseEndpoint := destination
	switch change {
	case ChangePort:
		responseEndpoint = s.topology.PrimaryIPAlternatePort
	case ChangeIPAndPort:
		responseEndpoint = s.topology.AlternateIPAlternatePort
	}
	responseConn := s.sockets[responseEndpoint]
	observation.ResponseSource = responseEndpoint
	observation.Outcome = "success"
	if err := sendSuccess(responseConn, source, request.TransactionID, responseEndpoint, s.topology.Other); err != nil {
		if !errors.Is(err, net.ErrClosed) {
			s.setError(err)
		}
		observation.Outcome = "send_error"
	}
	s.record(observation)
	if change == ChangeNone && destination == s.topology.Primary {
		s.baselineOnce.Do(s.baselineBarrier)
	}
}

func (s *Server) delay() bool {
	if s.config.Script.ResponseDelay == 0 {
		return true
	}
	timer := time.NewTimer(s.config.Script.ResponseDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.done:
		return false
	}
}

func sendSuccess(conn *net.UDPConn, destination netip.AddrPort, transactionID [stun.TransactionIDSize]byte, origin, other netip.AddrPort) error {
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = transactionID
	message.WriteTransactionID()
	attributes := []stun.Setter{
		&stun.XORMappedAddress{IP: net.IP(destination.Addr().AsSlice()), Port: int(destination.Port())},
		&stun.MappedAddress{IP: net.IP(destination.Addr().AsSlice()), Port: int(destination.Port())},
		&stun.ResponseOrigin{IP: net.IP(origin.Addr().AsSlice()), Port: int(origin.Port())},
		&stun.OtherAddress{IP: net.IP(other.Addr().AsSlice()), Port: int(other.Port())},
	}
	for _, attribute := range attributes {
		if err := attribute.AddTo(message); err != nil {
			return fmt.Errorf("add STUN response attribute: %w", err)
		}
	}
	if _, err := conn.WriteToUDPAddrPort(message.Raw, destination); err != nil {
		return fmt.Errorf("write Binding success from %s: %w", origin, err)
	}
	return nil
}

func sendError(conn *net.UDPConn, destination netip.AddrPort, transactionID [stun.TransactionIDSize]byte, code stun.ErrorCode) error {
	message := stun.MustBuild(stun.BindingError)
	message.TransactionID = transactionID
	message.WriteTransactionID()
	if err := (stun.ErrorCodeAttribute{Code: code, Reason: []byte("scripted")}).AddTo(message); err != nil {
		return fmt.Errorf("add STUN error code: %w", err)
	}
	if _, err := conn.WriteToUDPAddrPort(message.Raw, destination); err != nil {
		return fmt.Errorf("write Binding error: %w", err)
	}
	return nil
}

func (s *Server) baselineBarrier() {
	ready, release := s.config.Script.BaselineReadyFile, s.config.Script.BaselineContinueFile
	if ready == "" {
		return
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		s.setError(fmt.Errorf("write baseline readiness: %w", err))
		return
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(release); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			s.setError(fmt.Errorf("check baseline release: %w", err))
			return
		}
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) record(observation Observation) {
	s.mu.Lock()
	stored := false
	if len(s.observations) < s.config.ObservationLimit {
		s.observations = append(s.observations, observation)
		stored = true
	}
	s.mu.Unlock()
	if !stored {
		return
	}
	select {
	case s.changed <- struct{}{}:
	default:
	}
	if s.config.ObservationHook != nil {
		s.config.ObservationHook(observation)
	}
}

func (s *Server) setError(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Topology returns the immutable responder layout.
func (s *Server) Topology() Topology { return s.topology }

// Snapshot returns a copy of bounded observations and the first background error.
func (s *Server) Snapshot() ([]Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Observation(nil), s.observations...), s.err
}

// WaitForObservations waits for at least count recorded requests.
func (s *Server) WaitForObservations(ctx context.Context, count int) ([]Observation, error) {
	if count < 0 {
		return nil, errors.New("observation count cannot be negative")
	}
	for {
		observations, err := s.Snapshot()
		if err != nil || len(observations) >= count {
			return observations, err
		}
		select {
		case <-ctx.Done():
			return observations, ctx.Err()
		case <-s.done:
			return observations, net.ErrClosed
		case <-s.changed:
		}
	}
}

func (s *Server) closeSockets() {
	for _, conn := range s.sockets {
		_ = conn.Close()
	}
}

// Close stops all readers and closes all responder sockets. It is idempotent.
func (s *Server) Close() error {
	s.close.Do(func() {
		close(s.done)
		s.closeSockets()
		s.wg.Wait()
	})
	_, err := s.Snapshot()
	return err
}
