// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type RegistryOptions struct {
	Config            Config
	ServerPeer        peer.ID
	ServerIncarnation uint64
	Signer            crypto.PrivKey
	Resolver          CountryResolver
	ControlAddresses  []string
	ServerPublicProof []byte
	Random            io.Reader
}

type Registry struct {
	mu                sync.Mutex
	config            Config
	serverPeer        peer.ID
	serverIncarnation uint64
	signer            crypto.PrivKey
	resolver          CountryResolver
	controlAddresses  []string
	serverPublicProof []byte
	random            io.Reader
	registrations     map[peer.ID]*Registration
	countries         map[string]map[peer.ID]struct{}
	closed            bool
}

func NewRegistry(o RegistryOptions) (*Registry, error) {
	if err := o.Config.validate(); err != nil {
		return nil, err
	}
	if o.ServerPeer == "" || o.ServerIncarnation == 0 || o.Signer == nil || o.Resolver == nil {
		return nil, fmt.Errorf("%w: registry identity", ErrInvalid)
	}
	signerPeer, err := peer.IDFromPrivateKey(o.Signer)
	if err != nil || signerPeer != o.ServerPeer {
		return nil, fmt.Errorf("%w: registry signer", ErrWrongPeer)
	}
	if len(o.ControlAddresses) == 0 || len(o.ControlAddresses) > MaxControlAddresses || len(o.ServerPublicProof) > 1024 {
		return nil, fmt.Errorf("%w: registry route", ErrInvalid)
	}
	total := 0
	for _, address := range o.ControlAddresses {
		if address == "" {
			return nil, fmt.Errorf("%w: empty control address", ErrInvalid)
		}
		total += len(address)
	}
	if total > MaxControlAddressBytes {
		return nil, fmt.Errorf("%w: control address bytes", ErrInvalid)
	}
	random := o.Random
	if random == nil {
		random = rand.Reader
	}
	return &Registry{
		config: o.Config, serverPeer: o.ServerPeer, serverIncarnation: o.ServerIncarnation,
		signer: o.Signer, resolver: o.Resolver, controlAddresses: append([]string(nil), o.ControlAddresses...),
		serverPublicProof: append([]byte(nil), o.ServerPublicProof...), random: random,
		registrations: make(map[peer.ID]*Registration), countries: make(map[string]map[peer.ID]struct{}),
	}, nil
}

func (r *Registry) Register(ctx context.Context, remote peer.ID, id RegistrationID, channel ChannelToken, signed []byte, observedIP netip.Addr) (Registration, error) {
	if ctx == nil || remote == "" || id == (RegistrationID{}) || channel == (ChannelToken{}) ||
		!validObservedAddress(observedIP, r.config.ObservedIPPolicy) {
		return Registration{}, fmt.Errorf("%w: registration", ErrInvalid)
	}
	now := r.config.Now()
	descriptor, err := VerifyDescriptor(signed, remote, now)
	if err != nil {
		return Registration{}, err
	}
	if err := validateDescriptorLifetime(descriptor, r.config.DescriptorTTL); err != nil {
		return Registration{}, err
	}
	if !descriptor.Announcement.AcceptingNewSessions {
		return Registration{}, fmt.Errorf("%w: Exit is not accepting", ErrUnauthorized)
	}
	observation, resolveErr := r.resolver.Resolve(ctx, observedIP)
	if resolveErr != nil {
		observation = CountryObservation{State: CountryUnknown, ObservedIP: observedIP, Family: addressFamily(observedIP), ObservedAt: now.UTC()}
	}
	if observation.ObservedIP != observedIP || observation.Family != addressFamily(observedIP) {
		return Registration{}, fmt.Errorf("%w: resolver observation binding", ErrInvalid)
	}
	if observation.State == CountryObserved && !validCountry(observation.Country) {
		return Registration{}, fmt.Errorf("%w: resolver country", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Registration{}, ErrClosed
	}
	r.refreshLocked(now)
	if current := r.registrations[remote]; current != nil {
		sameOwner := current.Owner.RegistrationID == id && current.Owner.ChannelToken == channel
		if sameOwner && current.Descriptor.ID == descriptor.ID && current.ObservedIP == observedIP {
			return cloneRegistration(*current), nil
		}
		if current.Owner.RegistrationID == id {
			return Registration{}, ErrConflict
		}
		r.removeIndexLocked(current)
	}
	if len(r.registrations) >= r.config.MaxRegistrations {
		return Registration{}, ErrBusy
	}
	registration := &Registration{
		Owner:      Owner{ExitPeer: remote, ServerIncarnation: r.serverIncarnation, RegistrationID: id, ChannelToken: channel},
		Descriptor: descriptor, Country: observation, ObservedIP: observedIP, ReceiptSequence: 1,
		AcceptedAt: now, LastHeartbeat: now, State: RegistrationFresh,
		Route: OwnerRoute{ServerPeer: r.serverPeer, ServerIncarnation: r.serverIncarnation, RegistrationID: id, ControlAddresses: append([]string(nil), r.controlAddresses...)},
	}
	if err := r.issueReceiptLocked(registration, now); err != nil {
		return Registration{}, err
	}
	r.registrations[remote] = registration
	r.addIndexLocked(registration)
	return cloneRegistration(*registration), nil
}

func (r *Registry) Update(ctx context.Context, owner Owner, signed []byte) (Registration, error) {
	if ctx == nil {
		return Registration{}, ErrInvalid
	}
	now := r.config.Now()
	descriptor, err := VerifyDescriptor(signed, owner.ExitPeer, now)
	if err != nil {
		return Registration{}, err
	}
	if err := validateDescriptorLifetime(descriptor, r.config.DescriptorTTL); err != nil {
		return Registration{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Registration{}, ErrClosed
	}
	r.refreshLocked(now)
	current, err := r.matchLocked(owner)
	if err != nil {
		return Registration{}, err
	}
	switch descriptor.Version.Compare(current.Descriptor.Version) {
	case -1:
		return Registration{}, ErrStale
	case 0:
		if descriptor.ID != current.Descriptor.ID {
			return Registration{}, ErrConflict
		}
		return cloneRegistration(*current), nil
	}
	r.removeIndexLocked(current)
	current.Descriptor = descriptor
	current.ReceiptSequence++
	if err := r.issueReceiptLocked(current, now); err != nil {
		return Registration{}, err
	}
	if descriptor.Announcement.AcceptingNewSessions && current.State == RegistrationFresh {
		r.addIndexLocked(current)
	}
	return cloneRegistration(*current), nil
}

func validateDescriptorLifetime(descriptor Descriptor, maximum time.Duration) error {
	if descriptor.Announcement == nil || maximum <= 0 {
		return ErrInvalid
	}
	issued := time.Unix(descriptor.Announcement.CreatedAtUnix, 0)
	expires := time.Unix(descriptor.Announcement.ExpiresAtUnix, 0)
	if expires.Sub(issued) > maximum {
		return fmt.Errorf("%w: descriptor lifetime exceeds %s", ErrInvalid, maximum)
	}
	return nil
}

func (r *Registry) Heartbeat(owner Owner, sequence uint64) (Registration, error) {
	if sequence == 0 {
		return Registration{}, ErrInvalid
	}
	now := r.config.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Registration{}, ErrClosed
	}
	r.refreshLocked(now)
	current, err := r.matchLocked(owner)
	if err != nil {
		return Registration{}, err
	}
	if sequence <= current.HeartbeatSequence {
		return Registration{}, ErrStale
	}
	wasFresh := current.State == RegistrationFresh
	current.HeartbeatSequence, current.LastHeartbeat, current.State = sequence, now, RegistrationFresh
	if err := r.renewReceiptLocked(current, now); err != nil {
		return Registration{}, err
	}
	if !wasFresh && current.Descriptor.Announcement.AcceptingNewSessions {
		r.addIndexLocked(current)
	}
	return cloneRegistration(*current), nil
}

func (r *Registry) renewReceiptLocked(registration *Registration, now time.Time) error {
	if registration.SignedReceipt.Receipt.ExpiresAt.Sub(now) > r.config.ReceiptRenewLead {
		return nil
	}
	nextExpiry := now.Add(r.config.ReceiptTTL)
	descriptorExpiry := time.Unix(registration.Descriptor.Announcement.ExpiresAtUnix, 0)
	if descriptorExpiry.Before(nextExpiry) {
		nextExpiry = descriptorExpiry
	}
	if !nextExpiry.After(registration.SignedReceipt.Receipt.ExpiresAt) {
		return nil
	}
	registration.ReceiptSequence++
	return r.issueReceiptLocked(registration, now)
}

func (r *Registry) Unregister(owner Owner) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	current, err := r.matchLocked(owner)
	if err != nil {
		return err
	}
	r.removeIndexLocked(current)
	current.State = RegistrationClosed
	delete(r.registrations, owner.ExitPeer)
	return nil
}

func (r *Registry) ChannelClosed(owner Owner) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	current, err := r.matchLocked(owner)
	if err != nil {
		return false
	}
	r.removeIndexLocked(current)
	current.State = RegistrationClosed
	delete(r.registrations, owner.ExitPeer)
	return true
}

func (r *Registry) Lookup(subject peer.ID) (Registration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Registration{}, false
	}
	r.refreshLocked(r.config.Now())
	current := r.registrations[subject]
	if current == nil {
		return Registration{}, false
	}
	return cloneRegistration(*current), true
}

func (r *Registry) Sample(country string, limit int, excluded map[peer.ID]struct{}) ([]Candidate, error) {
	if !validCountry(country) || limit < 1 || limit > MaxCandidates || len(excluded) > MaxExcludedPeers {
		return nil, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	r.refreshLocked(r.config.Now())
	bucket := r.countries[country]
	candidates := make([]Candidate, 0, min(limit, len(bucket)))
	for subject := range bucket {
		if _, skip := excluded[subject]; skip {
			continue
		}
		registration := r.registrations[subject]
		if registration == nil || registration.State != RegistrationFresh || !registration.Descriptor.Announcement.AcceptingNewSessions || !registration.Country.Satisfies(country) {
			continue
		}
		candidates = append(candidates, Candidate{
			Descriptor: cloneDescriptor(registration.Descriptor),
			Receipts:   []SignedReceipt{cloneSignedReceipt(registration.SignedReceipt)},
			Routes:     []OwnerRoute{cloneOwnerRoute(registration.Route)},
		})
	}
	if err := shuffleCandidates(r.random, candidates); err != nil {
		return nil, fmt.Errorf("sample country pool: %w", err)
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (r *Registry) Sweep() RegistrySnapshot { return r.Snapshot() }

func (r *Registry) Snapshot() RegistrySnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.refreshLocked(r.config.Now())
	}
	return r.snapshotLocked()
}

// Countries exposes only country names backed by fresh local registrations.
func (r *Registry) Countries() []string {
	return r.CountriesExcluding("")
}

// CountriesExcluding filters requester PeerID, never IP, using the existing fresh/accepting index.
func (r *Registry) CountriesExcluding(excluded peer.ID) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.refreshLocked(r.config.Now())
	result := make([]string, 0, len(r.countries))
	for country, peers := range r.countries {
		for subject := range peers {
			if subject != excluded {
				result = append(result, country)
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for _, registration := range r.registrations {
		registration.State = RegistrationClosed
	}
	clear(r.registrations)
	clear(r.countries)
}

func (r *Registry) issueReceiptLocked(registration *Registration, now time.Time) error {
	expiresAt := now.Add(r.config.ReceiptTTL)
	descriptorExpiry := time.Unix(registration.Descriptor.Announcement.ExpiresAtUnix, 0)
	if descriptorExpiry.Before(expiresAt) {
		expiresAt = descriptorExpiry
	}
	signed, err := SealReceipt(r.signer, Receipt{
		ServerPeer: r.serverPeer, ServerIncarnation: r.serverIncarnation,
		SubjectPeer: registration.Owner.ExitPeer, RegistrationID: registration.Owner.RegistrationID,
		Sequence: registration.ReceiptSequence, DescriptorID: registration.Descriptor.ID,
		DescriptorVersion: registration.Descriptor.Version, ObservedIP: registration.ObservedIP,
		ObservedFamily: addressFamily(registration.ObservedIP), Country: registration.Country,
		IssuedAt: now.UTC(), ExpiresAt: expiresAt.UTC(), ServerPublicProof: r.serverPublicProof,
	}, now)
	if err != nil {
		return err
	}
	registration.SignedReceipt = signed
	return nil
}

func (r *Registry) matchLocked(owner Owner) (*Registration, error) {
	if owner.ExitPeer == "" || owner.ServerIncarnation != r.serverIncarnation || owner.RegistrationID == (RegistrationID{}) || owner.ChannelToken == (ChannelToken{}) {
		return nil, ErrNotFound
	}
	current := r.registrations[owner.ExitPeer]
	if current == nil || current.Owner != owner {
		return nil, ErrNotFound
	}
	return current, nil
}

func (r *Registry) refreshLocked(now time.Time) {
	for subject, registration := range r.registrations {
		age := now.Sub(registration.LastHeartbeat)
		switch {
		case age >= r.config.RegistrationExpiry:
			r.removeIndexLocked(registration)
			registration.State = RegistrationExpired
			delete(r.registrations, subject)
		case age >= r.config.RegistrationStale && registration.State == RegistrationFresh:
			r.removeIndexLocked(registration)
			registration.State = RegistrationStale
		}
	}
}

func (r *Registry) addIndexLocked(registration *Registration) {
	if registration.State != RegistrationFresh || !registration.Descriptor.Announcement.AcceptingNewSessions || registration.Country.State != CountryObserved || !validCountry(registration.Country.Country) {
		return
	}
	bucket := r.countries[registration.Country.Country]
	if bucket == nil {
		bucket = make(map[peer.ID]struct{})
		r.countries[registration.Country.Country] = bucket
	}
	bucket[registration.Owner.ExitPeer] = struct{}{}
}

func (r *Registry) removeIndexLocked(registration *Registration) {
	bucket := r.countries[registration.Country.Country]
	if bucket == nil {
		return
	}
	delete(bucket, registration.Owner.ExitPeer)
	if len(bucket) == 0 {
		delete(r.countries, registration.Country.Country)
	}
}

func (r *Registry) snapshotLocked() RegistrySnapshot {
	snapshot := RegistrySnapshot{Registrations: len(r.registrations), Countries: len(r.countries), Closed: r.closed}
	for _, registration := range r.registrations {
		if registration.State == RegistrationFresh {
			snapshot.Fresh++
		}
		if registration.State == RegistrationStale {
			snapshot.Stale++
		}
	}
	return snapshot
}

func validObservedAddress(address netip.Addr, policy ObservedIPPolicy) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() ||
		address.IsLinkLocalUnicast() || address.Is4In6() {
		return false
	}
	if address.IsLoopback() {
		return policy.AllowLoopback
	}
	if address.IsPrivate() {
		return policy.AllowPrivate
	}
	documentation := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range documentation {
		if prefix.Contains(address) {
			return policy.AllowDocumentation
		}
	}
	return address.IsGlobalUnicast()
}

func validCountry(country string) bool {
	return len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z'
}

func shuffleCandidates(random io.Reader, candidates []Candidate) error {
	for i := len(candidates) - 1; i > 0; i-- {
		value, err := rand.Int(random, big.NewInt(int64(i+1)))
		if err != nil {
			return err
		}
		j := int(value.Int64())
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
	return nil
}

func cloneRegistration(value Registration) Registration {
	value.Descriptor = cloneDescriptor(value.Descriptor)
	value.SignedReceipt = cloneSignedReceipt(value.SignedReceipt)
	value.Route = cloneOwnerRoute(value.Route)
	return value
}

func cloneDescriptor(value Descriptor) Descriptor {
	value.SignedEnvelope = append([]byte(nil), value.SignedEnvelope...)
	value.RawPayload = append([]byte(nil), value.RawPayload...)
	if value.Announcement != nil {
		value.Announcement = proto.Clone(value.Announcement).(*irviov1.NodeAnnouncement)
	}
	return value
}

func cloneSignedReceipt(value SignedReceipt) SignedReceipt {
	value.Receipt.ServerPublicProof = append([]byte(nil), value.Receipt.ServerPublicProof...)
	value.SignedEnvelope = append([]byte(nil), value.SignedEnvelope...)
	value.RawPayload = append([]byte(nil), value.RawPayload...)
	return value
}

func cloneOwnerRoute(value OwnerRoute) OwnerRoute {
	value.ControlAddresses = append([]string(nil), value.ControlAddresses...)
	return value
}
