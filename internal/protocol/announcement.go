// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package protocol implements signed IRVIO node announcements and the node stream protocol.
package protocol

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/reachability"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

const (
	ProtocolVersion uint32 = 1

	CapDiscovery       uint64 = 1 << 0
	CapExit            uint64 = 1 << 1
	CapSTUNBasic       uint64 = 1 << 2
	CapSTUNRFC5780     uint64 = 1 << 3
	CapClientOnly      uint64 = 1 << 4
	CapPublicControlV4 uint64 = 1 << 5
	CapPublicControlV6 uint64 = 1 << 6
	CapRendezvous      uint64 = 1 << 7

	AnnouncementDomain         = "hydra/node-announcement"
	SessionEpochSize           = 16
	MaxAnnouncementPayloadSize = 32 * 1024
	MaxSignedEnvelopeSize      = 64 * 1024
	MaxAnnouncementAddresses   = 32
	MaxPublicControlEndpoints  = 2
	MaxAddressBytes            = 1024
	MaxSessionFeatures         = 16
	MaxSessionFeatureBytes     = 64
	MaxAnnouncementTTL         = 24 * time.Hour
	MaxClockSkew               = 5 * time.Minute
	// announcementCodec is private-use multicodec 0x300001 encoded as an unsigned varint.
	announcementCodec = "\x81\x80\xc0\x01"
)

// VerifiedAnnouncement hashes original signed bytes, never protobuf reserialization.
type VerifiedAnnouncement struct {
	Announcement   *irviov1.NodeAnnouncement
	SignedEnvelope []byte
	RawPayload     []byte
	DescriptorID   [sha256.Size]byte
}

// AnnouncementRecord adapts NodeAnnouncement to libp2p record.Envelope.
type AnnouncementRecord struct {
	Announcement *irviov1.NodeAnnouncement
}

// Domain returns the stable signature domain.
func (*AnnouncementRecord) Domain() string { return AnnouncementDomain }

// Codec returns the stable private-use payload codec.
func (*AnnouncementRecord) Codec() []byte { return []byte(announcementCodec) }

// MarshalRecord deterministically serializes the protobuf payload.
func (r *AnnouncementRecord) MarshalRecord() ([]byte, error) {
	if r.Announcement == nil {
		return nil, fmt.Errorf("announcement is nil")
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(r.Announcement)
	if err != nil {
		return nil, fmt.Errorf("marshal node announcement: %w", err)
	}
	if len(data) > MaxAnnouncementPayloadSize {
		return nil, fmt.Errorf("announcement payload size %d exceeds %d", len(data), MaxAnnouncementPayloadSize)
	}
	return data, nil
}

// UnmarshalRecord parses a size-bounded protobuf payload.
func (r *AnnouncementRecord) UnmarshalRecord(data []byte) error {
	if len(data) == 0 || len(data) > MaxAnnouncementPayloadSize {
		return fmt.Errorf("announcement payload size %d is invalid", len(data))
	}
	announcement := new(irviov1.NodeAnnouncement)
	if err := (proto.UnmarshalOptions{RecursionLimit: 32}).Unmarshal(data, announcement); err != nil {
		return fmt.Errorf("unmarshal node announcement: %w", err)
	}
	r.Announcement = announcement
	return nil
}

// NewSessionEpoch generates a cryptographically random 128-bit process epoch.
func NewSessionEpoch() ([]byte, error) {
	epoch := make([]byte, SessionEpochSize)
	if _, err := rand.Read(epoch); err != nil {
		return nil, fmt.Errorf("generate session epoch: %w", err)
	}
	return epoch, nil
}

// NetworkPathEpoch identifies process and locators, excluding metadata sequence and descriptor identity.
func NetworkPathEpoch(sessionEpoch []byte, locators []string) uint64 {
	hash := sha256.New()
	hash.Write([]byte("hydra/network-path-epoch/v1"))
	writeEpochField(hash, sessionEpoch)

	unique := make(map[string]struct{}, len(locators))
	canonical := make([]string, 0, len(locators))
	for _, locator := range locators {
		if _, exists := unique[locator]; exists {
			continue
		}
		unique[locator] = struct{}{}
		canonical = append(canonical, locator)
	}
	sort.Strings(canonical)
	for _, locator := range canonical {
		writeEpochField(hash, []byte(locator))
	}
	digest := hash.Sum(nil)
	epoch := binary.BigEndian.Uint64(digest[:8])
	if epoch == 0 {
		return 1
	}
	return epoch
}

func writeEpochField(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = hash.Write(size[:])
	_, _ = hash.Write(value)
}

// SealAnnouncement validates and signs a node announcement.
func SealAnnouncement(privateKey crypto.PrivKey, announcement *irviov1.NodeAnnouncement, now time.Time) ([]byte, error) {
	signer, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("derive announcement signer PeerID: %w", err)
	}
	if err := ValidateAnnouncement(announcement, signer, now); err != nil {
		return nil, err
	}
	envelope, err := record.Seal(&AnnouncementRecord{Announcement: announcement}, privateKey)
	if err != nil {
		return nil, fmt.Errorf("seal node announcement: %w", err)
	}
	data, err := envelope.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal signed node announcement: %w", err)
	}
	if len(data) > MaxSignedEnvelopeSize {
		return nil, fmt.Errorf("signed announcement size %d exceeds %d", len(data), MaxSignedEnvelopeSize)
	}
	return data, nil
}

// VerifySignedAnnouncement authenticates, validates, and binds a provider to an announcement.
func VerifySignedAnnouncement(data []byte, providerID peer.ID, now time.Time) (*irviov1.NodeAnnouncement, error) {
	verified, err := VerifySignedAnnouncementRecord(data, providerID, now)
	if err != nil {
		return nil, err
	}
	return verified.Announcement, nil
}

// VerifySignedAnnouncementRecord preserves exact authenticated bytes for descriptor identity.
func VerifySignedAnnouncementRecord(data []byte, providerID peer.ID, now time.Time) (VerifiedAnnouncement, error) {
	if providerID == "" {
		return VerifiedAnnouncement{}, fmt.Errorf("provider PeerID is empty")
	}
	if len(data) == 0 || len(data) > MaxSignedEnvelopeSize {
		return VerifiedAnnouncement{}, fmt.Errorf("signed announcement size %d is invalid", len(data))
	}
	destination := new(AnnouncementRecord)
	envelope, err := record.ConsumeTypedEnvelope(data, destination)
	if err != nil {
		return VerifiedAnnouncement{}, fmt.Errorf("verify signed announcement envelope: %w", err)
	}
	if !bytes.Equal(envelope.PayloadType, []byte(announcementCodec)) {
		return VerifiedAnnouncement{}, fmt.Errorf("unexpected announcement codec %x", envelope.PayloadType)
	}
	signerID, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil {
		return VerifiedAnnouncement{}, fmt.Errorf("derive envelope signer PeerID: %w", err)
	}
	if signerID != providerID {
		return VerifiedAnnouncement{}, fmt.Errorf("provider PeerID %s does not match signer %s", providerID, signerID)
	}
	if err := ValidateAnnouncement(destination.Announcement, signerID, now); err != nil {
		return VerifiedAnnouncement{}, err
	}
	raw := append([]byte(nil), envelope.RawPayload...)
	return VerifiedAnnouncement{
		Announcement:   destination.Announcement,
		SignedEnvelope: append([]byte(nil), data...),
		RawPayload:     raw,
		DescriptorID:   sha256.Sum256(raw),
	}, nil
}

// ValidateAnnouncement validates authenticated announcement contents.
func ValidateAnnouncement(announcement *irviov1.NodeAnnouncement, expectedNodeID peer.ID, now time.Time) error {
	if announcement == nil {
		return fmt.Errorf("announcement is nil")
	}
	if size := proto.Size(announcement); size > MaxAnnouncementPayloadSize {
		return fmt.Errorf("announcement payload size %d exceeds %d", size, MaxAnnouncementPayloadSize)
	}
	if announcement.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %d", announcement.ProtocolVersion)
	}
	nodeID, err := peer.IDFromBytes(announcement.NodeId)
	if err != nil {
		return fmt.Errorf("invalid announcement NodeID: %w", err)
	}
	if nodeID != expectedNodeID {
		return fmt.Errorf("announcement NodeID %s does not match signer %s", nodeID, expectedNodeID)
	}
	if len(announcement.SessionEpoch) != SessionEpochSize {
		return fmt.Errorf("session epoch size %d, want %d", len(announcement.SessionEpoch), SessionEpochSize)
	}
	if announcement.Incarnation == 0 {
		return fmt.Errorf("announcement incarnation must be positive")
	}
	if announcement.Sequence == 0 {
		return fmt.Errorf("announcement sequence must be positive")
	}
	if announcement.Country != "" && !validUpperCountry(announcement.Country) {
		return fmt.Errorf("country must be exactly two ASCII uppercase letters")
	}
	if announcement.Capabilities&CapExit != 0 && announcement.Country == "" {
		return fmt.Errorf("exit capability requires country")
	}
	if len(announcement.Libp2PAddrs) > MaxAnnouncementAddresses {
		return fmt.Errorf("announcement has %d addresses, maximum is %d", len(announcement.Libp2PAddrs), MaxAnnouncementAddresses)
	}
	for _, address := range announcement.Libp2PAddrs {
		if len(address) == 0 || len(address) > MaxAddressBytes {
			return fmt.Errorf("libp2p address size %d is invalid", len(address))
		}
		if _, err := multiaddr.NewMultiaddr(address); err != nil {
			return fmt.Errorf("invalid libp2p address: %w", err)
		}
	}
	if len(announcement.AwgPublicKey) != 0 && len(announcement.AwgPublicKey) != 32 {
		return fmt.Errorf("AWG public key must be exactly 32 bytes")
	}
	if len(announcement.AwgPublicKey) == 0 && announcement.AwgKeyEpoch != 0 || len(announcement.AwgPublicKey) == 32 && announcement.AwgKeyEpoch == 0 {
		return fmt.Errorf("AWG public key and epoch must be present together")
	}
	if len(announcement.PublicControlEndpoints) > MaxPublicControlEndpoints {
		return fmt.Errorf("announcement has too many public control endpoints")
	}
	if len(announcement.SessionFeatures) > MaxSessionFeatures {
		return fmt.Errorf("announcement has too many session features")
	}
	if announcement.Capabilities&CapRendezvous != 0 {
		if announcement.Capabilities&(CapPublicControlV4|CapPublicControlV6) == 0 ||
			!containsString(announcement.SessionFeatures, "rendezvous-v1") {
			return fmt.Errorf("Rendezvous capability requires verified public control and rendezvous-v1")
		}
	}
	for _, feature := range announcement.SessionFeatures {
		if len(feature) == 0 || len(feature) > MaxSessionFeatureBytes {
			return fmt.Errorf("announcement session feature size %d is invalid", len(feature))
		}
	}
	seenPublicFamilies := make(map[irviov1.ReachabilityFamily]struct{}, len(announcement.PublicControlEndpoints))
	for _, endpoint := range announcement.PublicControlEndpoints {
		if endpoint == nil || endpoint.Proof == nil {
			return fmt.Errorf("public control endpoint proof is missing")
		}
		if _, duplicate := seenPublicFamilies[endpoint.Family]; duplicate {
			return fmt.Errorf("duplicate public control address family")
		}
		seenPublicFamilies[endpoint.Family] = struct{}{}
		capability := CapPublicControlV6
		if endpoint.Family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 {
			capability = CapPublicControlV4
		} else if endpoint.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
			return fmt.Errorf("invalid public control address family")
		}
		if announcement.Capabilities&capability == 0 {
			return fmt.Errorf("public control endpoint requires matching capability")
		}
		policy := reachability.DefaultProofPolicy()
		policy.Now = func() time.Time { return now }
		proof, err := reachability.BuildProof(expectedNodeID, endpoint.Proof.Attestations, policy)
		if err != nil {
			return fmt.Errorf("verify public control endpoint: %w", err)
		}
		if endpoint.Family != proof.Endpoint.Family || endpoint.ExternalControlPort != uint32(proof.Endpoint.Address.Port()) ||
			!bytes.Equal(endpoint.ObservedIp, proof.Endpoint.Address.Addr().AsSlice()) ||
			endpoint.NetworkGeneration != proof.Endpoint.NetworkGeneration || endpoint.PublicEpoch != proof.Endpoint.PublicEpoch ||
			endpoint.ExpiresAtUnix != proof.ExpiresAt.Unix() {
			return fmt.Errorf("public control endpoint does not match verifier proof")
		}
	}
	if announcement.Capabilities&CapPublicControlV4 != 0 {
		if _, ok := seenPublicFamilies[irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4]; !ok {
			return fmt.Errorf("PUBLIC_CONTROL_V4 requires a verified endpoint")
		}
	}
	if announcement.Capabilities&CapPublicControlV6 != 0 {
		if _, ok := seenPublicFamilies[irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6]; !ok {
			return fmt.Errorf("PUBLIC_CONTROL_V6 requires a verified endpoint")
		}
	}
	if len(announcement.PublicControlEndpoints) != 0 && announcement.Capabilities&(CapPublicControlV4|CapPublicControlV6) == 0 {
		return fmt.Errorf("verified public endpoint requires public capability")
	}
	createdAt := time.Unix(announcement.CreatedAtUnix, 0)
	expiresAt := time.Unix(announcement.ExpiresAtUnix, 0)
	if announcement.CreatedAtUnix <= 0 || announcement.ExpiresAtUnix <= 0 {
		return fmt.Errorf("announcement timestamps must be positive")
	}
	if !expiresAt.After(createdAt) {
		return fmt.Errorf("announcement expiration must follow creation")
	}
	if expiresAt.Sub(createdAt) > MaxAnnouncementTTL {
		return fmt.Errorf("announcement lifetime exceeds %s", MaxAnnouncementTTL)
	}
	if createdAt.After(now.Add(MaxClockSkew)) {
		return fmt.Errorf("announcement creation is too far in the future")
	}
	if !expiresAt.After(now) {
		return fmt.Errorf("announcement expired at %s", expiresAt.UTC())
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func validUpperCountry(country string) bool {
	return len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z'
}
