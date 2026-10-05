// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package rendezvous provides bounded presence, sampling, and signaling.
package rendezvous

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const (
	ProtocolVersion              uint32 = 1
	ProtocolID                          = "/hydra/rendezvous/1.0.0"
	AvailableCountriesProtocolID        = "/hydra/rendezvous/countries/1.0.0"

	MaxFrameSize            = 64 * 1024
	PreferredDescriptorSize = 8 * 1024
	MaxDescriptorSize       = protocol.MaxSignedEnvelopeSize
	MaxReceiptSize          = 2 * 1024
	MaxCandidates           = 10
	MaxOwnerRoutes          = 3
	MaxExcludedPeers        = 32
	MaxRegistrationIDSize   = 16
	MaxRequestIDSize        = 16
	MaxErrorMessageBytes    = 128
	MaxControlAddresses     = 4
	MaxControlAddressBytes  = 1024
	MaxSignalingMessages    = 16
	MaxSignalingBytes       = 64 * 1024
	MaxAvailableCountries   = 26 * 26
)

var (
	ErrClosed          = errors.New("Rendezvous is closed")
	ErrInvalid         = errors.New("invalid Rendezvous message")
	ErrWrongPeer       = errors.New("Rendezvous peer mismatch")
	ErrUnauthorized    = errors.New("Rendezvous unauthorized")
	ErrBusy            = errors.New("Rendezvous capacity busy")
	ErrRateLimited     = errors.New("Rendezvous rate limited")
	ErrStale           = errors.New("stale Rendezvous state")
	ErrConflict        = errors.New("conflicting Rendezvous state")
	ErrNotFound        = errors.New("Rendezvous state not found")
	ErrRouteFailed     = errors.New("Rendezvous signaling route failed")
	ErrTimeout         = errors.New("Rendezvous deadline exceeded")
	ErrCountryDegraded = errors.New("GeoIP country resolver degraded")
)

type RegistrationID [MaxRegistrationIDSize]byte
type RequestID [MaxRequestIDSize]byte
type ChannelToken [16]byte

func randomNonzero[T ~[16]byte](reader io.Reader) (T, error) {
	var value T
	if reader == nil {
		reader = rand.Reader
	}
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return value, err
	}
	if value == (T{}) {
		value[len(value)-1] = 1
	}
	return value, nil
}

func NewRegistrationID(reader io.Reader) (RegistrationID, error) {
	return randomNonzero[RegistrationID](reader)
}

func NewRequestID(reader io.Reader) (RequestID, error) {
	return randomNonzero[RequestID](reader)
}

func NewChannelToken(reader io.Reader) (ChannelToken, error) {
	return randomNonzero[ChannelToken](reader)
}

type DescriptorVersion struct {
	Incarnation uint64
	Sequence    uint64
}

func (v DescriptorVersion) Valid() bool { return v.Incarnation != 0 && v.Sequence != 0 }

func (v DescriptorVersion) Compare(other DescriptorVersion) int {
	if v.Incarnation < other.Incarnation {
		return -1
	}
	if v.Incarnation > other.Incarnation {
		return 1
	}
	if v.Sequence < other.Sequence {
		return -1
	}
	if v.Sequence > other.Sequence {
		return 1
	}
	return 0
}

type Descriptor struct {
	Subject        peer.ID
	Version        DescriptorVersion
	ID             [32]byte
	SignedEnvelope []byte
	RawPayload     []byte
	Announcement   *irviov1.NodeAnnouncement
}

func VerifyDescriptor(signed []byte, expected peer.ID, now time.Time) (Descriptor, error) {
	verified, err := protocol.VerifySignedAnnouncementRecord(signed, expected, now)
	if err != nil {
		return Descriptor{}, err
	}
	message := verified.Announcement
	version := DescriptorVersion{Incarnation: message.Incarnation, Sequence: message.Sequence}
	if !version.Valid() || message.Capabilities&protocol.CapExit == 0 ||
		len(message.AwgPublicKey) != 32 || message.AwgKeyEpoch == 0 {
		return Descriptor{}, fmt.Errorf("%w: descriptor lacks Exit identity", ErrInvalid)
	}
	return Descriptor{
		Subject: expected, Version: version, ID: verified.DescriptorID,
		SignedEnvelope: append([]byte(nil), signed...),
		RawPayload:     append([]byte(nil), verified.RawPayload...),
		Announcement:   message,
	}, nil
}

type CountryState uint8

const (
	CountryUnknown CountryState = iota
	CountryObserved
	CountryConflicting
)

type CountryObservation struct {
	State         CountryState
	Country       string
	ObservedIP    netip.Addr
	Family        irviov1.ReachabilityFamily
	DatabaseType  string
	DatabaseBuild time.Time
	ObservedAt    time.Time
}

func (o CountryObservation) Satisfies(country string) bool {
	return o.State == CountryObserved && o.Country == country
}

type OwnerRoute struct {
	ServerPeer        peer.ID
	ServerIncarnation uint64
	RegistrationID    RegistrationID
	ControlAddresses  []string
}

type Candidate struct {
	Descriptor Descriptor
	Receipts   []SignedReceipt
	Routes     []OwnerRoute
}

type RegistrationState uint8

const (
	RegistrationFresh RegistrationState = iota + 1
	RegistrationStale
	RegistrationExpired
	RegistrationClosed
)

type Owner struct {
	ExitPeer          peer.ID
	ServerIncarnation uint64
	RegistrationID    RegistrationID
	ChannelToken      ChannelToken
}

type Registration struct {
	Owner             Owner
	Descriptor        Descriptor
	Country           CountryObservation
	ObservedIP        netip.Addr
	HeartbeatSequence uint64
	ReceiptSequence   uint64
	SignedReceipt     SignedReceipt
	AcceptedAt        time.Time
	LastHeartbeat     time.Time
	State             RegistrationState
	Route             OwnerRoute
}

type RegistrySnapshot struct {
	Registrations int
	Fresh         int
	Stale         int
	Countries     int
	Closed        bool
}

func addressFamily(address netip.Addr) irviov1.ReachabilityFamily {
	if address.Is4() {
		return irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4
	}
	if address.Is6() && !address.Is4In6() {
		return irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6
	}
	return irviov1.ReachabilityFamily_REACHABILITY_FAMILY_UNSPECIFIED
}
