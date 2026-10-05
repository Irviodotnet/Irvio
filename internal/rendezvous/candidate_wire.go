// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"fmt"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	ma "github.com/multiformats/go-multiaddr"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func CandidateToProto(candidate Candidate) (*irviov1.BrokeredCandidate, error) {
	if err := validateCandidate(candidate); err != nil {
		return nil, err
	}
	message := &irviov1.BrokeredCandidate{SignedAnnouncement: append([]byte(nil), candidate.Descriptor.SignedEnvelope...)}
	for _, receipt := range candidate.Receipts {
		message.OwnerReceipts = append(message.OwnerReceipts, append([]byte(nil), receipt.SignedEnvelope...))
	}
	for _, route := range candidate.Routes {
		message.OwnerRoutes = append(message.OwnerRoutes, &irviov1.OwnerRoute{
			ServerPeerId: []byte(route.ServerPeer), ServerIncarnation: route.ServerIncarnation,
			RegistrationId:   append([]byte(nil), route.RegistrationID[:]...),
			ControlAddresses: append([]string(nil), route.ControlAddresses...),
		})
	}
	return message, nil
}

func CandidateFromProto(message *irviov1.BrokeredCandidate, now time.Time) (Candidate, error) {
	if message == nil || len(message.SignedAnnouncement) == 0 || len(message.SignedAnnouncement) > MaxDescriptorSize ||
		len(message.OwnerReceipts) == 0 || len(message.OwnerReceipts) > MaxOwnerRoutes ||
		len(message.OwnerRoutes) == 0 || len(message.OwnerRoutes) > MaxOwnerRoutes {
		return Candidate{}, fmt.Errorf("%w: candidate bounds", ErrInvalid)
	}
	envelope, err := record.UnmarshalEnvelope(message.SignedAnnouncement)
	if err != nil {
		return Candidate{}, fmt.Errorf("%w: descriptor envelope", ErrInvalid)
	}
	subject, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil {
		return Candidate{}, fmt.Errorf("%w: descriptor signer", ErrWrongPeer)
	}
	descriptor, err := VerifyDescriptor(message.SignedAnnouncement, subject, now)
	if err != nil {
		return Candidate{}, err
	}
	candidate := Candidate{Descriptor: descriptor}
	for _, signed := range message.OwnerReceipts {
		receipt, err := VerifyReceiptAny(signed, now)
		if err != nil {
			return Candidate{}, err
		}
		if receipt.Receipt.SubjectPeer != subject || receipt.Receipt.DescriptorID != descriptor.ID ||
			receipt.Receipt.DescriptorVersion != descriptor.Version {
			return Candidate{}, fmt.Errorf("%w: receipt descriptor binding", ErrConflict)
		}
		candidate.Receipts = append(candidate.Receipts, receipt)
	}
	for _, wire := range message.OwnerRoutes {
		route, err := ownerRouteFromProto(wire)
		if err != nil {
			return Candidate{}, err
		}
		candidate.Routes = append(candidate.Routes, route)
	}
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func validateCandidate(candidate Candidate) error {
	if candidate.Descriptor.Subject == "" || candidate.Descriptor.Announcement == nil || !candidate.Descriptor.Version.Valid() ||
		candidate.Descriptor.ID == ([32]byte{}) ||
		len(candidate.Descriptor.SignedEnvelope) == 0 || len(candidate.Descriptor.SignedEnvelope) > MaxDescriptorSize ||
		len(candidate.Receipts) == 0 || len(candidate.Receipts) > MaxOwnerRoutes ||
		len(candidate.Routes) == 0 || len(candidate.Routes) > MaxOwnerRoutes {
		return fmt.Errorf("%w: candidate", ErrInvalid)
	}
	receipts := make(map[string]struct{}, len(candidate.Receipts))
	for _, receipt := range candidate.Receipts {
		value := receipt.Receipt
		if value.SubjectPeer != candidate.Descriptor.Subject || value.DescriptorID != candidate.Descriptor.ID ||
			value.DescriptorVersion != candidate.Descriptor.Version || len(receipt.SignedEnvelope) == 0 ||
			len(receipt.SignedEnvelope) > MaxReceiptSize {
			return fmt.Errorf("%w: candidate receipt", ErrConflict)
		}
		key := ownerKey(value.ServerPeer, value.ServerIncarnation, value.RegistrationID)
		if _, duplicate := receipts[key]; duplicate {
			return fmt.Errorf("%w: duplicate candidate receipt", ErrConflict)
		}
		receipts[key] = struct{}{}
	}
	seenRoutes := make(map[peer.ID]struct{}, len(candidate.Routes))
	for _, route := range candidate.Routes {
		if err := validateOwnerRoute(route); err != nil {
			return err
		}
		if _, duplicate := seenRoutes[route.ServerPeer]; duplicate {
			return fmt.Errorf("%w: duplicate owner route", ErrConflict)
		}
		seenRoutes[route.ServerPeer] = struct{}{}
		if _, ok := receipts[ownerKey(route.ServerPeer, route.ServerIncarnation, route.RegistrationID)]; !ok {
			return fmt.Errorf("%w: owner route lacks matching receipt", ErrUnauthorized)
		}
	}
	return nil
}

func ownerRouteFromProto(message *irviov1.OwnerRoute) (OwnerRoute, error) {
	if message == nil || message.ServerIncarnation == 0 || len(message.RegistrationId) != MaxRegistrationIDSize {
		return OwnerRoute{}, fmt.Errorf("%w: owner route", ErrInvalid)
	}
	server, err := peer.IDFromBytes(message.ServerPeerId)
	if err != nil {
		return OwnerRoute{}, fmt.Errorf("%w: owner route server", ErrInvalid)
	}
	var registrationID RegistrationID
	copy(registrationID[:], message.RegistrationId)
	route := OwnerRoute{
		ServerPeer: server, ServerIncarnation: message.ServerIncarnation,
		RegistrationID: registrationID, ControlAddresses: append([]string(nil), message.ControlAddresses...),
	}
	if err := validateOwnerRoute(route); err != nil {
		return OwnerRoute{}, err
	}
	return route, nil
}

func validateOwnerRoute(route OwnerRoute) error {
	if route.ServerPeer == "" || route.ServerIncarnation == 0 || route.RegistrationID == (RegistrationID{}) ||
		len(route.ControlAddresses) == 0 || len(route.ControlAddresses) > MaxControlAddresses {
		return fmt.Errorf("%w: owner route", ErrInvalid)
	}
	total := 0
	for _, value := range route.ControlAddresses {
		total += len(value)
		address, err := ma.NewMultiaddr(value)
		if err != nil {
			return fmt.Errorf("%w: owner address", ErrInvalid)
		}
		info, err := peer.AddrInfoFromP2pAddr(address)
		if err != nil || info.ID != route.ServerPeer {
			return fmt.Errorf("%w: owner address identity", ErrWrongPeer)
		}
	}
	if total > MaxControlAddressBytes {
		return fmt.Errorf("%w: owner address bytes", ErrInvalid)
	}
	return nil
}

func ownerKey(server peer.ID, incarnation uint64, registrationID RegistrationID) string {
	return server.String() + "/" + fmt.Sprint(incarnation) + "/" + string(registrationID[:])
}

func MergeCandidates(candidates ...Candidate) ([]Candidate, error) {
	byPeer := make(map[peer.ID]Candidate)
	order := make([]peer.ID, 0, len(candidates))
	for _, candidate := range candidates {
		if err := validateCandidate(candidate); err != nil {
			return nil, err
		}
		current, exists := byPeer[candidate.Descriptor.Subject]
		if !exists {
			byPeer[candidate.Descriptor.Subject] = cloneCandidate(candidate)
			order = append(order, candidate.Descriptor.Subject)
			continue
		}
		comparison := candidate.Descriptor.Version.Compare(current.Descriptor.Version)
		if comparison < 0 {
			continue
		}
		if comparison > 0 {
			byPeer[candidate.Descriptor.Subject] = cloneCandidate(candidate)
			continue
		}
		if candidate.Descriptor.ID != current.Descriptor.ID {
			return nil, fmt.Errorf("%w: same descriptor version", ErrConflict)
		}
		merged, err := mergeSameDescriptor(current, candidate)
		if err != nil {
			return nil, err
		}
		byPeer[candidate.Descriptor.Subject] = merged
	}
	result := make([]Candidate, 0, len(order))
	for _, subject := range order {
		result = append(result, byPeer[subject])
	}
	return result, nil
}

func (candidate Candidate) CountryStatus() (string, CountryState) {
	country := ""
	observed := false
	for _, signed := range candidate.Receipts {
		observation := signed.Receipt.Country
		if observation.State == CountryConflicting {
			return "", CountryConflicting
		}
		if observation.State != CountryObserved {
			continue
		}
		if !observed {
			country, observed = observation.Country, true
			continue
		}
		if country != observation.Country {
			return "", CountryConflicting
		}
	}
	if !observed {
		return "", CountryUnknown
	}
	return country, CountryObserved
}

func (candidate Candidate) SatisfiesCountry(country string) bool {
	observed, state := candidate.CountryStatus()
	return state == CountryObserved && observed == country
}

func mergeSameDescriptor(a, b Candidate) (Candidate, error) {
	result := cloneCandidate(a)
	receiptKeys := make(map[string]int, len(result.Receipts))
	for index, receipt := range result.Receipts {
		value := receipt.Receipt
		receiptKeys[ownerKey(value.ServerPeer, value.ServerIncarnation, value.RegistrationID)] = index
	}
	for _, receipt := range b.Receipts {
		value := receipt.Receipt
		key := ownerKey(value.ServerPeer, value.ServerIncarnation, value.RegistrationID)
		if index, exists := receiptKeys[key]; exists {
			current := result.Receipts[index]
			if value.Sequence < current.Receipt.Sequence {
				continue
			}
			if value.Sequence == current.Receipt.Sequence {
				if !bytes.Equal(receipt.SignedEnvelope, current.SignedEnvelope) {
					return Candidate{}, fmt.Errorf("%w: same receipt sequence", ErrConflict)
				}
				continue
			}
			result.Receipts[index] = cloneSignedReceipt(receipt)
			continue
		}
		if len(result.Receipts) >= MaxOwnerRoutes {
			continue
		}
		result.Receipts = append(result.Receipts, cloneSignedReceipt(receipt))
		receiptKeys[key] = len(result.Receipts) - 1
	}
	routePeers := make(map[peer.ID]struct{}, len(result.Routes))
	for _, route := range result.Routes {
		routePeers[route.ServerPeer] = struct{}{}
	}
	for _, route := range b.Routes {
		if _, exists := routePeers[route.ServerPeer]; exists {
			continue
		}
		if len(result.Routes) >= MaxOwnerRoutes {
			break
		}
		if _, backed := receiptKeys[ownerKey(route.ServerPeer, route.ServerIncarnation, route.RegistrationID)]; !backed {
			continue
		}
		result.Routes = append(result.Routes, cloneOwnerRoute(route))
		routePeers[route.ServerPeer] = struct{}{}
	}
	return result, nil
}

func cloneCandidate(value Candidate) Candidate {
	value.Descriptor = cloneDescriptor(value.Descriptor)
	value.Receipts = append([]SignedReceipt(nil), value.Receipts...)
	for index := range value.Receipts {
		value.Receipts[index] = cloneSignedReceipt(value.Receipts[index])
	}
	value.Routes = append([]OwnerRoute(nil), value.Routes...)
	for index := range value.Routes {
		value.Routes[index] = cloneOwnerRoute(value.Routes[index])
	}
	return value
}

func observedAddressFromBytes(payload []byte) (netip.Addr, error) {
	address, ok := netip.AddrFromSlice(payload)
	if !ok || address.Is4In6() {
		return netip.Addr{}, ErrInvalid
	}
	return address, nil
}
