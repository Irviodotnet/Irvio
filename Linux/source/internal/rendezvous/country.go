// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

type CountryResolver interface {
	Resolve(context.Context, netip.Addr) (CountryObservation, error)
	Snapshot() CountryResolverSnapshot
}

type CountryResolverSnapshot struct {
	DatabaseType  string
	DatabaseBuild time.Time
	Degraded      bool
	LastError     string
}

type MMDBResolver struct {
	mu     sync.RWMutex
	path   string
	reader *maxminddb.Reader
	state  CountryResolverSnapshot
	now    func() time.Time
}

type mmdbCountryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	RegisteredCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"registered_country"`
	RepresentedCountry struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"represented_country"`
}

func OpenMMDB(path string) (*MMDBResolver, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty database path", ErrCountryDegraded)
	}
	resolver := &MMDBResolver{path: path, now: time.Now}
	if err := resolver.Reload(path); err != nil {
		return nil, err
	}
	return resolver, nil
}

func (r *MMDBResolver) Reload(path string) error {
	if r == nil || path == "" {
		return fmt.Errorf("%w: empty database path", ErrCountryDegraded)
	}
	next, err := maxminddb.Open(path)
	if err != nil {
		r.mu.Lock()
		r.state.Degraded = true
		r.state.LastError = err.Error()
		r.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrCountryDegraded, err)
	}
	state := CountryResolverSnapshot{
		DatabaseType:  next.Metadata.DatabaseType,
		DatabaseBuild: next.Metadata.BuildTime().UTC(),
	}
	r.mu.Lock()
	previous := r.reader
	r.reader = next
	r.path = path
	r.state = state
	r.mu.Unlock()
	if previous != nil {
		if err := previous.Close(); err != nil {
			return fmt.Errorf("close previous GeoIP snapshot: %w", err)
		}
	}
	return nil
}

func (r *MMDBResolver) Resolve(ctx context.Context, address netip.Addr) (CountryObservation, error) {
	if r == nil || ctx == nil || !address.IsValid() || address.IsUnspecified() ||
		address.IsLoopback() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return CountryObservation{}, fmt.Errorf("%w: invalid observed address", ErrCountryDegraded)
	}
	if err := ctx.Err(); err != nil {
		return CountryObservation{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Failed GeoIP reloads retain the last validated snapshot until replacement succeeds.
	if r.reader == nil {
		return CountryObservation{State: CountryUnknown, ObservedIP: address}, ErrCountryDegraded
	}
	var record mmdbCountryRecord
	if err := r.reader.Lookup(address).Decode(&record); err != nil {
		return CountryObservation{State: CountryUnknown, ObservedIP: address}, fmt.Errorf("%w: %v", ErrCountryDegraded, err)
	}
	now := r.now().UTC()
	observation := CountryObservation{
		State: CountryUnknown, ObservedIP: address, Family: addressFamily(address),
		DatabaseType: r.state.DatabaseType, DatabaseBuild: r.state.DatabaseBuild, ObservedAt: now,
	}
	if len(record.Country.ISOCode) == 2 {
		country := record.Country.ISOCode
		if country[0] >= 'a' && country[0] <= 'z' {
			country = string([]byte{country[0] - 32, country[1] - 32})
		}
		if country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z' {
			observation.State = CountryObserved
			observation.Country = country
		}
	}
	return observation, nil
}

func (r *MMDBResolver) Snapshot() CountryResolverSnapshot {
	if r == nil {
		return CountryResolverSnapshot{Degraded: true, LastError: ErrCountryDegraded.Error()}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *MMDBResolver) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	previous := r.reader
	r.reader = nil
	r.state.Degraded = true
	r.state.LastError = "closed"
	r.mu.Unlock()
	if previous == nil {
		return nil
	}
	return previous.Close()
}

type UnknownCountryResolver struct {
	Reason error
}

func (r UnknownCountryResolver) Resolve(_ context.Context, address netip.Addr) (CountryObservation, error) {
	err := r.Reason
	if err == nil {
		err = ErrCountryDegraded
	}
	return CountryObservation{State: CountryUnknown, ObservedIP: address, Family: addressFamily(address), ObservedAt: time.Now().UTC()}, err
}

func (r UnknownCountryResolver) Snapshot() CountryResolverSnapshot {
	err := r.Reason
	if err == nil {
		err = ErrCountryDegraded
	}
	return CountryResolverSnapshot{Degraded: true, LastError: err.Error()}
}
