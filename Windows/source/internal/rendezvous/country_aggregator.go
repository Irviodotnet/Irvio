// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	MaxAvailableCountryServers      = 10
	DefaultAvailableCountryParallel = 5
	DefaultAvailableCountryTTL      = 10 * time.Second
	DefaultAvailableCountryTimeout  = 5 * time.Second
	DefaultAvailableCountryPeerTime = 1500 * time.Millisecond
)

type AvailableCountriesStatus uint8

const (
	AvailableCountriesUnavailable AvailableCountriesStatus = iota
	AvailableCountriesReady
	AvailableCountriesPartial
	AvailableCountriesEmpty
)

type AvailableCountries struct {
	Status      AvailableCountriesStatus
	Codes       []string
	GeneratedAt time.Time
}

type AvailableCountryLister interface {
	ListLocalAvailableCountries(context.Context, peer.ID) ([]string, error)
}

type LocalCountrySource func() ([]string, bool)

type CountryAggregatorOptions struct {
	Local       peer.ID
	Client      AvailableCountryLister
	Provider    RendezvousServerProvider
	LocalSource LocalCountrySource
	Parallelism int
	CacheTTL    time.Duration
	Timeout     time.Duration
	PeerTimeout time.Duration
	Now         func() time.Time
}

type countryAggregationCall struct {
	done   chan struct{}
	result AvailableCountries
}

type CountryAggregator struct {
	options CountryAggregatorOptions

	mu       sync.Mutex
	cached   AvailableCountries
	expires  time.Time
	inflight *countryAggregationCall
}

func NewCountryAggregator(options CountryAggregatorOptions) (*CountryAggregator, error) {
	if options.Local == "" || options.Client == nil || options.Provider == nil {
		return nil, ErrInvalid
	}
	if options.Parallelism == 0 {
		options.Parallelism = DefaultAvailableCountryParallel
	}
	if options.CacheTTL == 0 {
		options.CacheTTL = DefaultAvailableCountryTTL
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultAvailableCountryTimeout
	}
	if options.PeerTimeout == 0 {
		options.PeerTimeout = DefaultAvailableCountryPeerTime
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Parallelism < 1 || options.Parallelism > DefaultAvailableCountryParallel ||
		options.CacheTTL <= 0 || options.Timeout <= 0 || options.PeerTimeout <= 0 ||
		options.PeerTimeout > options.Timeout {
		return nil, ErrInvalid
	}
	return &CountryAggregator{options: options}, nil
}

func (a *CountryAggregator) Get(ctx context.Context, refresh bool) (AvailableCountries, error) {
	if ctx == nil {
		return AvailableCountries{}, ErrInvalid
	}
	now := a.options.Now()
	a.mu.Lock()
	if !refresh && !a.expires.IsZero() && now.Before(a.expires) {
		result := cloneAvailableCountries(a.cached)
		a.mu.Unlock()
		return result, nil
	}
	if call := a.inflight; call != nil {
		a.mu.Unlock()
		select {
		case <-call.done:
			return cloneAvailableCountries(call.result), nil
		case <-ctx.Done():
			return AvailableCountries{}, ctx.Err()
		}
	}
	call := &countryAggregationCall{done: make(chan struct{})}
	a.inflight = call
	a.mu.Unlock()

	result := a.collect(ctx)
	a.mu.Lock()
	a.cached = cloneAvailableCountries(result)
	a.expires = a.options.Now().Add(a.options.CacheTTL)
	call.result = cloneAvailableCountries(result)
	a.inflight = nil
	close(call.done)
	a.mu.Unlock()
	return result, nil
}

func (a *CountryAggregator) collect(ctx context.Context) AvailableCountries {
	ctx, cancel := context.WithTimeout(ctx, a.options.Timeout)
	defer cancel()
	countries := make(map[string]struct{})
	successes, failures := 0, 0
	if a.options.LocalSource != nil {
		if local, available := a.options.LocalSource(); available {
			if addCountries(countries, local) {
				successes++
			} else {
				failures++
			}
		}
	}

	discoveryTimeout := a.options.Timeout - a.options.PeerTimeout
	if discoveryTimeout <= 0 {
		discoveryTimeout = a.options.Timeout / 2
	}
	discoveryContext, stopDiscovery := context.WithTimeout(ctx, discoveryTimeout)
	servers, err := a.options.Provider.Servers(discoveryContext, "", MaxAvailableCountryServers)
	stopDiscovery()
	if err != nil {
		failures++
	}
	seen := make(map[peer.ID]struct{}, len(servers))
	selected := make([]peer.ID, 0, min(len(servers), MaxAvailableCountryServers))
	for _, server := range servers {
		if server == "" || server == a.options.Local {
			continue
		}
		if _, duplicate := seen[server]; duplicate {
			continue
		}
		seen[server] = struct{}{}
		selected = append(selected, server)
		if len(selected) == MaxAvailableCountryServers {
			break
		}
	}
	type response struct {
		codes []string
		err   error
	}
	responses := make(chan response, len(selected))
	slots := make(chan struct{}, a.options.Parallelism)
	var workers sync.WaitGroup
	for _, server := range selected {
		workers.Add(1)
		go func(server peer.ID) {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				responses <- response{err: ctx.Err()}
				return
			}
			peerContext, stop := context.WithTimeout(ctx, a.options.PeerTimeout)
			codes, err := a.options.Client.ListLocalAvailableCountries(peerContext, server)
			stop()
			reportServerFailure(a.options.Provider, server, err)
			responses <- response{codes: codes, err: err}
		}(server)
	}
	workers.Wait()
	close(responses)
	for response := range responses {
		if response.err != nil || !addCountries(countries, response.codes) {
			failures++
			continue
		}
		successes++
	}
	codes := make([]string, 0, len(countries))
	for country := range countries {
		codes = append(codes, country)
	}
	sort.Strings(codes)
	status := AvailableCountriesUnavailable
	if successes > 0 {
		switch {
		case failures > 0:
			status = AvailableCountriesPartial
		case len(codes) == 0:
			status = AvailableCountriesEmpty
		default:
			status = AvailableCountriesReady
		}
	}
	return AvailableCountries{Status: status, Codes: codes, GeneratedAt: a.options.Now().UTC()}
}

func addCountries(target map[string]struct{}, values []string) bool {
	if len(values) > MaxAvailableCountries {
		return false
	}
	for _, country := range values {
		if !validCountry(country) {
			return false
		}
	}
	for _, country := range values {
		target[country] = struct{}{}
	}
	return true
}

func cloneAvailableCountries(value AvailableCountries) AvailableCountries {
	value.Codes = append([]string(nil), value.Codes...)
	return value
}
