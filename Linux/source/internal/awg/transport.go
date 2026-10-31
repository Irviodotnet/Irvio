// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
)

type peerState struct {
	remotePeer        peer.ID
	endpoint          SelectedEndpoint
	allowedIPs        []netip.Prefix
	publicFingerprint string
	handshakeAfter    time.Time
	stagedReplacement bool
}

const defaultHandshakeRetrySeconds uint32 = 1

// Transport owns a generation's AWG device, never the borrowed mux.
type Transport struct {
	config Config
	bind   *IrvioBind
	device *device.Device

	mu        sync.RWMutex
	peers     map[Key]peerState
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// NewTransport configures AWG and peers before activation, without injecting user packets.
func NewTransport(config Config) (_ *Transport, resultErr error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create AWG transport: %w", err)
	}
	if config.MaxPeers == 0 {
		config.MaxPeers = DefaultMaxPeers
	}
	if config.HandshakeRetrySeconds == 0 {
		config.HandshakeRetrySeconds = defaultHandshakeRetrySeconds
	}
	if config.Logger == nil {
		config.Logger = device.NewLogger(device.LogLevelSilent, "hydra-awg")
	}
	config.Peers = clonePeerConfigs(config.Peers)
	bind, err := NewIrvioBind(config.Mux, config.NetworkGeneration)
	if err != nil {
		return nil, err
	}
	awgDevice := device.NewDevice(config.TUN, bind, config.Logger)
	transport := &Transport{
		config: config, bind: bind, device: awgDevice,
		peers: make(map[Key]peerState, len(config.Peers)),
	}
	defer func() {
		if resultErr != nil {
			awgDevice.Close()
			_ = bind.Shutdown()
		}
	}()
	if err := awgDevice.IpcSet(buildInitialUAPI(config)); err != nil {
		return nil, fmt.Errorf("configure AWG device: %w", err)
	}
	for _, peerConfig := range config.Peers {
		transport.peers[peerConfig.PublicKey] = peerState{
			remotePeer: peerConfig.RemotePeer, endpoint: peerConfig.Endpoint,
			allowedIPs:        append([]netip.Prefix(nil), peerConfig.AllowedIPs...),
			publicFingerprint: peerConfig.PublicKey.Fingerprint(),
		}
	}
	if err := awgDevice.Up(); err != nil {
		return nil, fmt.Errorf("bring AWG device up: %w", err)
	}
	if bind.Port() != config.Mux.Port() {
		return nil, fmt.Errorf("AWG bind port %d differs from mux port %d: %w", bind.Port(), config.Mux.Port(), ErrInvalidConfig)
	}
	return transport, nil
}

// Bind returns the logical Irvio-backed AWG bind for diagnostics.
func (t *Transport) Bind() *IrvioBind {
	if t == nil {
		return nil
	}
	return t.bind
}

// NetworkGeneration returns the immutable owner generation.
func (t *Transport) NetworkGeneration() uint64 {
	if t == nil {
		return 0
	}
	return uint64(t.config.NetworkGeneration)
}

// TriggerHandshake discards old Noise sessions when the remote AWG device is replaced.
func (t *Transport) TriggerHandshake(publicKey Key) error {
	peerObject, err := t.prepareHandshake(publicKey)
	if err != nil {
		return err
	}
	return peerObject.SendHandshakeInitiation(false)
}

// PrepareHandshake records a baseline and invalidates keys without sending before coordinated rotation.
func (t *Transport) PrepareHandshake(publicKey Key) error {
	_, err := t.prepareHandshake(publicKey)
	return err
}

func (t *Transport) prepareHandshake(publicKey Key) (*device.Peer, error) {
	if t == nil {
		return nil, ErrClosed
	}
	previous, err := t.PeerStats(publicKey)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	state, exists := t.peers[publicKey]
	awgDevice := t.device
	if !exists {
		t.mu.Unlock()
		return nil, ErrPeerNotFound
	}
	state.handshakeAfter = previous.LastHandshake
	t.peers[publicKey] = state
	t.mu.Unlock()
	peerObject := awgDevice.LookupPeer(device.NoisePublicKey(publicKey))
	if peerObject == nil {
		return nil, ErrPeerNotFound
	}
	peerObject.ExpireCurrentKeypairs()
	return peerObject, nil
}

// WaitHandshake observes redacted handshake state until completion or cancellation.
func (t *Transport) WaitHandshake(ctx context.Context, publicKey Key) (PeerStats, error) {
	if ctx == nil {
		return PeerStats{}, fmt.Errorf("wait for AWG handshake: nil context")
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		stats, err := t.PeerStats(publicKey)
		if err != nil {
			return PeerStats{}, err
		}
		t.mu.Lock()
		state, exists := t.peers[publicKey]
		if t.closed {
			t.mu.Unlock()
			return PeerStats{}, ErrClosed
		}
		if !exists {
			t.mu.Unlock()
			return PeerStats{}, ErrPeerNotFound
		}
		required := state.handshakeAfter
		if !stats.LastHandshake.IsZero() && (required.IsZero() || stats.LastHandshake.After(required)) {
			if state.handshakeAfter.Equal(required) {
				state.handshakeAfter = time.Time{}
				t.peers[publicKey] = state
			}
			t.mu.Unlock()
			return stats, nil
		}
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return PeerStats{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitHandshakeActive uses AWG-owned retries on one side only; never call initiation from an external retry loop.
func (t *Transport) WaitHandshakeActive(ctx context.Context, publicKey Key) (PeerStats, error) {
	if ctx == nil {
		return PeerStats{}, fmt.Errorf("wait for active AWG handshake: nil context")
	}
	return t.WaitHandshake(ctx, publicKey)
}

// PeerStats returns only redacted counters for one configured peer.
func (t *Transport) PeerStats(publicKey Key) (PeerStats, error) {
	if t == nil {
		return PeerStats{}, ErrClosed
	}
	t.mu.RLock()
	if t.closed {
		t.mu.RUnlock()
		return PeerStats{}, ErrClosed
	}
	state, exists := t.peers[publicKey]
	awgDevice := t.device
	t.mu.RUnlock()
	if !exists {
		return PeerStats{}, ErrPeerNotFound
	}
	sink := peerStatsSink{target: hex.EncodeToString(publicKey[:])}
	if err := awgDevice.IpcGetOperation(&sink); err != nil {
		return PeerStats{}, fmt.Errorf("read redacted AWG peer stats: %w", err)
	}
	if !sink.found {
		return PeerStats{}, ErrPeerNotFound
	}
	sink.stats.PublicKeyFingerprint = state.publicFingerprint
	return sink.stats, nil
}

// UpdatePeerEndpoint accepts only strictly newer ICE SessionGenerations.
func (t *Transport) UpdatePeerEndpoint(publicKey Key, endpoint SelectedEndpoint) error {
	if t == nil {
		return ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	state, exists := t.peers[publicKey]
	if !exists {
		return ErrPeerNotFound
	}
	if err := validateSelectedEndpoint(endpoint, state.remotePeer.String(), t.config.NetworkGeneration); err != nil {
		return err
	}
	if endpoint.SessionGeneration < state.endpoint.SessionGeneration {
		return ErrStaleSession
	}
	if endpoint.SessionGeneration == state.endpoint.SessionGeneration {
		if endpoint.Address == state.endpoint.Address {
			return nil
		}
		return ErrStaleSession
	}
	uapi := "public_key=" + hex.EncodeToString(publicKey[:]) + "\nupdate_only=true\nendpoint=" + endpoint.Address.String() + "\n\n"
	if err := t.device.IpcSet(uapi); err != nil {
		return fmt.Errorf("update AWG peer endpoint: %w", err)
	}
	state.endpoint = endpoint
	t.peers[publicKey] = state
	return nil
}

// AddPeer installs a fenced peer without creating another bind or socket.
func (t *Transport) AddPeer(config PeerConfig) error {
	if t == nil {
		return ErrClosed
	}
	if err := validatePeerConfig(config, t.config.NetworkGeneration); err != nil {
		return err
	}
	config.AllowedIPs = append([]netip.Prefix(nil), config.AllowedIPs...)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if len(t.peers) >= t.config.MaxPeers {
		return ErrTooManyPeers
	}
	if _, exists := t.peers[config.PublicKey]; exists {
		return ErrPeerExists
	}
	for _, state := range t.peers {
		if state.remotePeer == config.RemotePeer {
			return ErrPeerExists
		}
	}
	if err := t.device.IpcSet(buildPeerUAPI(config)); err != nil {
		return fmt.Errorf("add AWG peer: %w", err)
	}
	t.peers[config.PublicKey] = peerState{
		remotePeer: config.RemotePeer, endpoint: config.Endpoint,
		allowedIPs:        append([]netip.Prefix(nil), config.AllowedIPs...),
		publicFingerprint: config.PublicKey.Fingerprint(),
	}
	return nil
}

// ReplacePeerKey serializes verified key replacement while preserving endpoint and AllowedIPs.
func (t *Transport) ReplacePeerKey(oldKey, newKey Key, remote peer.ID) error {
	if t == nil {
		return ErrClosed
	}
	if oldKey.isZero() || newKey.isZero() || oldKey == newKey || remote == "" {
		return ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	state, exists := t.peers[oldKey]
	if !exists {
		if current, rotated := t.peers[newKey]; rotated && current.remotePeer == remote {
			return nil
		}
		return ErrPeerNotFound
	}
	if state.remotePeer != remote {
		return ErrWrongPeer
	}
	if _, exists := t.peers[newKey]; exists {
		return ErrPeerExists
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "public_key=%s\nremove=true\n", hex.EncodeToString(oldKey[:]))
	builder.WriteString(buildPeerUAPI(PeerConfig{
		RemotePeer: remote, PublicKey: newKey, Endpoint: state.endpoint,
		AllowedIPs: append([]netip.Prefix(nil), state.allowedIPs...),
	}))
	builder.WriteByte('\n')
	if err := t.device.IpcSet(builder.String()); err != nil {
		return fmt.Errorf("replace AWG peer key: %w", err)
	}
	delete(t.peers, oldKey)
	state.publicFingerprint = newKey.Fingerprint()
	state.handshakeAfter = time.Time{}
	t.peers[newKey] = state
	return nil
}

// StagePeerKeyRotation retains old-key routes for COMMIT and stages at most one replacement per peer.
func (t *Transport) StagePeerKeyRotation(oldKey, newKey Key, remote peer.ID) error {
	if t == nil {
		return ErrClosed
	}
	if oldKey.isZero() || newKey.isZero() || oldKey == newKey || remote == "" {
		return ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	oldState, oldExists := t.peers[oldKey]
	newState, newExists := t.peers[newKey]
	if !oldExists {
		if newExists && newState.remotePeer == remote && !newState.stagedReplacement {
			return nil
		}
		return ErrPeerNotFound
	}
	if oldState.remotePeer != remote {
		return ErrWrongPeer
	}
	if newExists {
		if newState.remotePeer == remote && newState.stagedReplacement {
			return nil
		}
		return ErrPeerExists
	}
	for key, state := range t.peers {
		if key != oldKey && state.stagedReplacement {
			return ErrPeerExists
		}
	}
	if err := t.device.IpcSet(buildPeerUAPI(PeerConfig{
		RemotePeer: remote, PublicKey: newKey, Endpoint: oldState.endpoint,
	})); err != nil {
		return fmt.Errorf("stage AWG peer key rotation: %w", err)
	}
	t.peers[newKey] = peerState{
		remotePeer: remote, endpoint: oldState.endpoint,
		allowedIPs:        append([]netip.Prefix(nil), oldState.allowedIPs...),
		publicFingerprint: newKey.Fingerprint(), stagedReplacement: true,
	}
	return nil
}

// FinalizePeerKeyRotation moves AllowedIPs and removes the old key after a fresh handshake.
func (t *Transport) FinalizePeerKeyRotation(oldKey, newKey Key, remote peer.ID) error {
	if t == nil {
		return ErrClosed
	}
	if oldKey.isZero() || newKey.isZero() || oldKey == newKey || remote == "" {
		return ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	oldState, oldExists := t.peers[oldKey]
	newState, newExists := t.peers[newKey]
	if !oldExists {
		if newExists && newState.remotePeer == remote && !newState.stagedReplacement {
			return nil
		}
		return ErrPeerNotFound
	}
	if oldState.remotePeer != remote || !newExists || newState.remotePeer != remote || !newState.stagedReplacement {
		return ErrWrongPeer
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "public_key=%s\nremove=true\n", hex.EncodeToString(oldKey[:]))
	builder.WriteString(buildPeerUAPI(PeerConfig{
		RemotePeer: remote, PublicKey: newKey, Endpoint: newState.endpoint,
		AllowedIPs: append([]netip.Prefix(nil), newState.allowedIPs...),
	}))
	builder.WriteByte('\n')
	if err := t.device.IpcSet(builder.String()); err != nil {
		return fmt.Errorf("finalize AWG peer key rotation: %w", err)
	}
	delete(t.peers, oldKey)
	newState.stagedReplacement = false
	t.peers[newKey] = newState
	return nil
}

// UpdatePrivateKey preserves the generation's bind, endpoints, and peers.
func (t *Transport) UpdatePrivateKey(privateKey Key) error {
	if t == nil {
		return ErrClosed
	}
	if privateKey.isZero() {
		return ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if privateKey == t.config.PrivateKey {
		return nil
	}
	uapi := "private_key=" + hex.EncodeToString(privateKey[:]) + "\n\n"
	if err := t.device.IpcSet(uapi); err != nil {
		return fmt.Errorf("update AWG private key: %w", err)
	}
	t.config.PrivateKey = privateKey
	return nil
}

// RemovePeer removes one AWG peer without disturbing the shared bind or peers.
func (t *Transport) RemovePeer(publicKey Key) error {
	if t == nil {
		return ErrClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if _, exists := t.peers[publicKey]; !exists {
		return ErrPeerNotFound
	}
	uapi := "public_key=" + hex.EncodeToString(publicKey[:]) + "\nremove=true\n\n"
	if err := t.device.IpcSet(uapi); err != nil {
		return fmt.Errorf("remove AWG peer: %w", err)
	}
	delete(t.peers, publicKey)
	return nil
}

// SessionGeneration returns the installed endpoint generation for a peer.
func (t *Transport) SessionGeneration(publicKey Key) (connsupervisor.SessionGeneration, error) {
	if t == nil {
		return 0, ErrClosed
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return 0, ErrClosed
	}
	state, exists := t.peers[publicKey]
	if !exists {
		return 0, ErrPeerNotFound
	}
	return state.endpoint.SessionGeneration, nil
}

// PeerEndpoint returns the selected immutable path without credentials.
func (t *Transport) PeerEndpoint(publicKey Key) (SelectedEndpoint, error) {
	if t == nil {
		return SelectedEndpoint{}, ErrClosed
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return SelectedEndpoint{}, ErrClosed
	}
	state, exists := t.peers[publicKey]
	if !exists {
		return SelectedEndpoint{}, ErrPeerNotFound
	}
	return state.endpoint, nil
}

// PeerCount returns the bounded number of configured peers.
func (t *Transport) PeerCount() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// Close stops AWG, its test/production TUN and IrvioBind, but not the mux.
func (t *Transport) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.peers = nil
		t.mu.Unlock()
		t.device.Close()
		t.closeErr = t.bind.Shutdown()
	})
	return t.closeErr
}

func buildInitialUAPI(config Config) string {
	var builder strings.Builder
	writeLine := func(key string, value any) { fmt.Fprintf(&builder, "%s=%v\n", key, value) }
	writeLine("private_key", hex.EncodeToString(config.PrivateKey[:]))
	writeLine("listen_port", config.Mux.Port())
	writeLine("replace_peers", "true")
	obfuscation := config.Obfuscation
	writeLine("jc", obfuscation.JunkPacketCount)
	writeLine("jmin", obfuscation.JunkMin)
	writeLine("jmax", obfuscation.JunkMax)
	writeLine("s1", obfuscation.InitPadding)
	writeLine("s2", obfuscation.ResponsePadding)
	writeLine("s3", obfuscation.CookiePadding)
	writeLine("s4", obfuscation.TransportPadding)
	writeLine("rekey_timeout", config.HandshakeRetrySeconds)
	if obfuscation.InitHeader != 0 {
		writeLine("h1", obfuscation.InitHeader)
	}
	if obfuscation.ResponseHeader != 0 {
		writeLine("h2", obfuscation.ResponseHeader)
	}
	if obfuscation.CookieHeader != 0 {
		writeLine("h3", obfuscation.CookieHeader)
	}
	if obfuscation.TransportHeader != 0 {
		writeLine("h4", obfuscation.TransportHeader)
	}
	for index, signature := range obfuscation.SignaturePackets {
		if signature != "" {
			writeLine(fmt.Sprintf("i%d", index+1), signature)
		}
	}
	for _, peerConfig := range config.Peers {
		builder.WriteString(buildPeerUAPI(peerConfig))
	}
	builder.WriteByte('\n')
	return builder.String()
}

func buildPeerUAPI(config PeerConfig) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "public_key=%s\n", hex.EncodeToString(config.PublicKey[:]))
	fmt.Fprintf(&builder, "endpoint=%s\n", config.Endpoint.Address)
	builder.WriteString("replace_allowed_ips=true\n")
	for _, prefix := range config.AllowedIPs {
		fmt.Fprintf(&builder, "allowed_ip=%s\n", prefix.Masked())
	}
	return builder.String()
}

func clonePeerConfigs(source []PeerConfig) []PeerConfig {
	result := make([]PeerConfig, len(source))
	for index, config := range source {
		result[index] = config
		result[index].AllowedIPs = append([]netip.Prefix(nil), config.AllowedIPs...)
	}
	return result
}

type peerStatsSink struct {
	target  string
	current bool
	found   bool
	seconds int64
	nanos   int64
	stats   PeerStats
}

func (s *peerStatsSink) Write(payload []byte) (int, error) {
	for _, line := range bytes.Split(payload, []byte{'\n'}) {
		key, value, ok := bytes.Cut(line, []byte{'='})
		if !ok {
			continue
		}
		switch string(key) {
		case "public_key":
			s.current = string(value) == s.target
			if s.current {
				s.found = true
			}
		case "last_handshake_time_sec":
			if s.current {
				s.seconds, _ = strconv.ParseInt(string(value), 10, 64)
			}
		case "last_handshake_time_nsec":
			if s.current {
				s.nanos, _ = strconv.ParseInt(string(value), 10, 64)
				if s.seconds != 0 || s.nanos != 0 {
					s.stats.LastHandshake = time.Unix(s.seconds, s.nanos)
				}
			}
		case "tx_bytes":
			if s.current {
				s.stats.TransmitBytes, _ = strconv.ParseUint(string(value), 10, 64)
			}
		case "rx_bytes":
			if s.current {
				s.stats.ReceiveBytes, _ = strconv.ParseUint(string(value), 10, 64)
			}
		}
	}
	return len(payload), nil
}

var _ io.Writer = (*peerStatsSink)(nil)
