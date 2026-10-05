// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"fmt"
	"strings"

	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/networkstate"
)

const maxHandshakeRetrySeconds = 30

func (c Config) validate() error {
	if c.Mux == nil || c.NetworkGeneration == 0 || c.TUN == nil || c.PrivateKey.isZero() {
		return ErrInvalidConfig
	}
	if c.HandshakeRetrySeconds > maxHandshakeRetrySeconds {
		return ErrInvalidConfig
	}
	maxPeers := c.MaxPeers
	if maxPeers == 0 {
		maxPeers = DefaultMaxPeers
	}
	if maxPeers < 1 || maxPeers > DefaultMaxPeers || len(c.Peers) > maxPeers {
		return ErrTooManyPeers
	}
	if err := c.Obfuscation.validate(); err != nil {
		return err
	}
	seenKeys := make(map[Key]struct{}, len(c.Peers))
	seenRemotePeers := make(map[string]struct{}, len(c.Peers))
	for _, peerConfig := range c.Peers {
		if err := validatePeerConfig(peerConfig, c.NetworkGeneration); err != nil {
			return err
		}
		if _, exists := seenKeys[peerConfig.PublicKey]; exists {
			return ErrPeerExists
		}
		seenKeys[peerConfig.PublicKey] = struct{}{}
		if _, exists := seenRemotePeers[peerConfig.RemotePeer.String()]; exists {
			return fmt.Errorf("duplicate remote peer: %w", ErrPeerExists)
		}
		seenRemotePeers[peerConfig.RemotePeer.String()] = struct{}{}
	}
	return nil
}

func (c ObfuscationConfig) validate() error {
	if c.JunkMin > c.JunkMax {
		return fmt.Errorf("AWG junk minimum exceeds maximum: %w", ErrInvalidConfig)
	}
	if c.TransportPadding != 0 {
		return ErrTransportPaddingUnsupported
	}
	for _, signature := range c.SignaturePackets {
		if len(signature) > maxSignatureLen || strings.ContainsAny(signature, "\r\n\x00") {
			return fmt.Errorf("invalid AWG signature packet: %w", ErrInvalidConfig)
		}
	}
	return nil
}

func validatePeerConfig(config PeerConfig, generation networkstate.Generation) error {
	if config.RemotePeer == "" || config.PublicKey.isZero() {
		return ErrInvalidConfig
	}
	if config.Endpoint.NetworkGeneration != generation {
		return ErrWrongGeneration
	}
	if config.Endpoint.SessionGeneration == 0 {
		return ErrStaleSession
	}
	if config.Endpoint.RemotePeer != config.RemotePeer {
		return ErrWrongPeer
	}
	if err := validateAddrPort(config.Endpoint.Address); err != nil {
		return err
	}
	if !candidateMatchesEndpoint(config.Endpoint.Candidate, config.Endpoint.Address) {
		return ErrInvalidEndpoint
	}
	switch config.Endpoint.Candidate.Type {
	case ice.CandidateTypeHost, ice.CandidateTypeServerReflexive, ice.CandidateTypePeerReflexive:
	default:
		return ErrInvalidEndpoint
	}
	if len(config.AllowedIPs) == 0 || len(config.AllowedIPs) > maxAllowedIPs {
		return ErrInvalidConfig
	}
	seen := make(map[string]struct{}, len(config.AllowedIPs))
	for _, prefix := range config.AllowedIPs {
		if !prefix.IsValid() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() && prefix.Bits() != 0 {
			return ErrInvalidConfig
		}
		canonical := prefix.Masked().String()
		if _, exists := seen[canonical]; exists {
			return ErrInvalidConfig
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

func validateSelectedEndpoint(endpoint SelectedEndpoint, remotePeer string, generation networkstate.Generation) error {
	if remotePeer == "" || endpoint.RemotePeer.String() != remotePeer {
		return ErrWrongPeer
	}
	if endpoint.NetworkGeneration != generation {
		return ErrWrongGeneration
	}
	if endpoint.SessionGeneration == 0 {
		return ErrStaleSession
	}
	if err := validateAddrPort(endpoint.Address); err != nil {
		return err
	}
	if !candidateMatchesEndpoint(endpoint.Candidate, endpoint.Address) {
		return ErrInvalidEndpoint
	}
	switch endpoint.Candidate.Type {
	case ice.CandidateTypeHost, ice.CandidateTypeServerReflexive, ice.CandidateTypePeerReflexive:
		return nil
	default:
		return ErrInvalidEndpoint
	}
}
