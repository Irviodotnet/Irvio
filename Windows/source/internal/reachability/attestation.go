// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const attestationDomain = "hydra/reachability-attestation/1.0.0\x00"

func signAttestation(privateKey crypto.PrivKey, value *irviov1.ReachabilityAttestation) (*irviov1.SignedReachabilityAttestation, error) {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
	if err != nil || len(payload) == 0 || len(payload) > MaxFrameSize/2 {
		return nil, ErrInvalidMessage
	}
	signature, err := privateKey.Sign(append([]byte(attestationDomain), payload...))
	if err != nil {
		return nil, fmt.Errorf("sign reachability attestation: %w", err)
	}
	publicKey, err := crypto.MarshalPublicKey(privateKey.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("marshal verifier public key: %w", err)
	}
	return &irviov1.SignedReachabilityAttestation{
		Attestation: payload, VerifierPublicKey: publicKey, Signature: signature,
	}, nil
}

func VerifyAttestation(signed *irviov1.SignedReachabilityAttestation, now time.Time) (*irviov1.ReachabilityAttestation, error) {
	if signed == nil || len(signed.Attestation) == 0 || len(signed.Attestation) > MaxFrameSize/2 ||
		len(signed.VerifierPublicKey) == 0 || len(signed.Signature) == 0 {
		return nil, ErrInvalidMessage
	}
	publicKey, err := crypto.UnmarshalPublicKey(signed.VerifierPublicKey)
	if err != nil {
		return nil, ErrInvalidMessage
	}
	valid, err := publicKey.Verify(append([]byte(attestationDomain), signed.Attestation...), signed.Signature)
	if err != nil || !valid {
		return nil, ErrIdentityMismatch
	}
	value := new(irviov1.ReachabilityAttestation)
	if err := (proto.UnmarshalOptions{RecursionLimit: 8}).Unmarshal(signed.Attestation, value); err != nil {
		return nil, ErrInvalidMessage
	}
	verifier, err := peer.IDFromBytes(value.VerifierPeerId)
	if err != nil {
		return nil, ErrInvalidMessage
	}
	signer, err := peer.IDFromPublicKey(publicKey)
	if err != nil || signer != verifier {
		return nil, ErrIdentityMismatch
	}
	if err := validateAttestation(value, now); err != nil {
		return nil, err
	}
	return value, nil
}

func validateAttestation(value *irviov1.ReachabilityAttestation, now time.Time) error {
	if value == nil || value.ProtocolVersion != ProtocolVersion || len(value.AttemptId) != AttemptIDSize ||
		value.ExternalControlPort == 0 || value.ExternalControlPort > 65535 ||
		value.NetworkGeneration == 0 || value.PublicEpoch == 0 {
		return ErrInvalidMessage
	}
	if _, err := peer.IDFromBytes(value.SubjectPeerId); err != nil {
		return ErrInvalidMessage
	}
	if _, err := peer.IDFromBytes(value.VerifierPeerId); err != nil {
		return ErrInvalidMessage
	}
	address, ok := netip.AddrFromSlice(value.ObservedIp)
	if !ok || !address.IsValid() || address.Is4In6() || familyFor(address) != value.Family {
		return ErrInvalidMessage
	}
	verified := time.Unix(value.VerifiedAtUnix, 0)
	expires := time.Unix(value.ExpiresAtUnix, 0)
	if value.VerifiedAtUnix <= 0 || value.ExpiresAtUnix <= 0 || !expires.After(verified) ||
		expires.Sub(verified) > DefaultProofTTL || verified.After(now.Add(time.Minute)) {
		return ErrInvalidMessage
	}
	if !expires.After(now) {
		return ErrExpired
	}
	return nil
}

func BuildProof(subject peer.ID, attestations []*irviov1.SignedReachabilityAttestation, policy ProofPolicy) (Proof, error) {
	if subject == "" || policy.Quorum <= 0 || policy.Quorum > DefaultVerifierLimit || len(attestations) > DefaultVerifierLimit {
		return Proof{}, ErrInvalidConfig
	}
	if policy.Now == nil {
		policy.Now = time.Now
	}
	now := policy.Now().UTC()
	var proof Proof
	seen := make(map[peer.ID]struct{}, len(attestations))
	for _, signed := range attestations {
		value, err := VerifyAttestation(signed, now)
		if err != nil {
			return Proof{}, err
		}
		valueSubject, _ := peer.IDFromBytes(value.SubjectPeerId)
		verifier, _ := peer.IDFromBytes(value.VerifierPeerId)
		address, _ := netip.AddrFromSlice(value.ObservedIp)
		endpoint := Endpoint{
			Family: value.Family, Address: netip.AddrPortFrom(address.Unmap(), uint16(value.ExternalControlPort)),
			NetworkGeneration: value.NetworkGeneration, PublicEpoch: value.PublicEpoch,
		}
		if valueSubject != subject || verifier == subject {
			return Proof{}, ErrWrongPeer
		}
		if len(proof.Attestations) == 0 {
			proof.Subject, proof.Endpoint = subject, endpoint
			proof.ExpiresAt = time.Unix(value.ExpiresAtUnix, 0)
		} else if proof.Endpoint != endpoint {
			return Proof{}, ErrStaleGeneration
		}
		if _, duplicate := seen[verifier]; duplicate {
			continue
		}
		seen[verifier] = struct{}{}
		proof.Attestations = append(proof.Attestations, proto.Clone(signed).(*irviov1.SignedReachabilityAttestation))
		if expiry := time.Unix(value.ExpiresAtUnix, 0); expiry.Before(proof.ExpiresAt) {
			proof.ExpiresAt = expiry
		}
	}
	if len(proof.Attestations) < policy.Quorum {
		return Proof{}, ErrInsufficient
	}
	slices.SortFunc(proof.Attestations, func(a, b *irviov1.SignedReachabilityAttestation) int {
		return bytes.Compare(a.VerifierPublicKey, b.VerifierPublicKey)
	})
	return proof, nil
}

func ProofMessage(proof Proof) *irviov1.PublicReachabilityProof {
	result := &irviov1.PublicReachabilityProof{Attestations: make([]*irviov1.SignedReachabilityAttestation, len(proof.Attestations))}
	for index, signed := range proof.Attestations {
		result.Attestations[index] = proto.Clone(signed).(*irviov1.SignedReachabilityAttestation)
	}
	return result
}

func familyFor(address netip.Addr) irviov1.ReachabilityFamily {
	if address.Is4() {
		return irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4
	}
	return irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6
}
