// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const (
	DefaultMaxPeers = 64
	maxAllowedIPs   = 16
	maxSignatureLen = 1024
)

// Key represents AWG Curve25519 material, not an IRVIO identity key.
type Key [32]byte

// Fingerprint returns a short non-secret diagnostic fingerprint.
func (k Key) Fingerprint() string {
	digest := sha256.Sum256(k[:])
	return hex.EncodeToString(digest[:8])
}

func (k Key) isZero() bool {
	var zero Key
	return k == zero
}

// SelectedEndpoint copies Pion's selected pair with generation fencing.
type SelectedEndpoint struct {
	NetworkGeneration networkstate.Generation
	SessionGeneration connsupervisor.SessionGeneration
	RemotePeer        peer.ID
	Address           netip.AddrPort
	Candidate         iceconnect.CandidateSnapshot
}

// SelectedEndpointFromICE verifies the expected peer and generation.
func SelectedEndpointFromICE(
	result iceconnect.Result,
	networkGeneration networkstate.Generation,
	sessionGeneration connsupervisor.SessionGeneration,
	remotePeer peer.ID,
) (SelectedEndpoint, error) {
	if networkGeneration == 0 || sessionGeneration == 0 || remotePeer == "" {
		return SelectedEndpoint{}, fmt.Errorf("select AWG endpoint: %w", ErrInvalidEndpoint)
	}
	if result.TransportGeneration != uint64(networkGeneration) {
		return SelectedEndpoint{}, fmt.Errorf("select AWG endpoint: result=%d current=%d: %w", result.TransportGeneration, networkGeneration, ErrWrongGeneration)
	}
	if result.RemotePeer != remotePeer {
		return SelectedEndpoint{}, fmt.Errorf("select AWG endpoint: %w", ErrWrongPeer)
	}
	if err := validateAddrPort(result.RemoteEndpoint); err != nil {
		return SelectedEndpoint{}, fmt.Errorf("select AWG endpoint: %w", err)
	}
	if !candidateMatchesEndpoint(result.SelectedRemoteCandidate, result.RemoteEndpoint) {
		return SelectedEndpoint{}, fmt.Errorf("select AWG endpoint: selected candidate mismatch: %w", ErrInvalidEndpoint)
	}
	return SelectedEndpoint{
		NetworkGeneration: networkGeneration,
		SessionGeneration: sessionGeneration,
		RemotePeer:        remotePeer,
		Address:           result.RemoteEndpoint,
		Candidate:         result.SelectedRemoteCandidate,
	}, nil
}

func candidateMatchesEndpoint(candidate iceconnect.CandidateSnapshot, endpoint netip.AddrPort) bool {
	if candidate.Address != endpoint.Addr() || candidate.Port != endpoint.Port() || candidate.Component != 1 {
		return false
	}
	if endpoint.Addr().Is4() {
		return candidate.Network == icesession.NetworkUDP4
	}
	return endpoint.Addr().Is6() && !endpoint.Addr().Is4In6() && candidate.Network == icesession.NetworkUDP6
}

// ObfuscationConfig applies before Device.Up; TransportPadding must remain zero pending the upstream fix.
type ObfuscationConfig struct {
	JunkPacketCount  uint32
	JunkMin          uint32
	JunkMax          uint32
	InitPadding      uint16
	ResponsePadding  uint16
	CookiePadding    uint16
	TransportPadding uint16
	InitHeader       uint32
	ResponseHeader   uint32
	CookieHeader     uint32
	TransportHeader  uint32
	SignaturePackets [5]string
}

// PeerConfig defines one AWG peer without owning its ICE or Irvio transport.
type PeerConfig struct {
	RemotePeer peer.ID
	PublicKey  Key
	Endpoint   SelectedEndpoint
	AllowedIPs []netip.Prefix
}

// Config creates one generation-scoped AWG device sharing a Irvio mux.
type Config struct {
	Mux                   *socketmux.Mux
	NetworkGeneration     networkstate.Generation
	TUN                   tun.Device
	PrivateKey            Key
	Peers                 []PeerConfig
	Obfuscation           ObfuscationConfig
	HandshakeRetrySeconds uint32
	MaxPeers              int
	Logger                *device.Logger
}

// PeerStats is a redacted device snapshot. It never contains private keys.
type PeerStats struct {
	PublicKeyFingerprint string
	LastHandshake        time.Time
	TransmitBytes        uint64
	ReceiveBytes         uint64
}

func validateAddrPort(endpoint netip.AddrPort) error {
	if !endpoint.IsValid() || endpoint.Port() == 0 {
		return ErrInvalidEndpoint
	}
	address := endpoint.Addr()
	if address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
		return ErrInvalidEndpoint
	}
	return nil
}
