// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

// Oracle combines network state and marked probes with concurrent-safe bounded evidence.
type Oracle struct {
	config Config

	mu       sync.Mutex
	state    State
	sequence uint64
	closed   bool
}

func New(config Config) (*Oracle, error) {
	if config.Targets == nil || config.Prober == nil || config.Now == nil ||
		config.ProbeTimeout <= 0 || config.TargetLimit < 1 || config.TargetLimit > 3 ||
		config.Parallelism < 1 || config.Parallelism > config.TargetLimit ||
		config.FailureThreshold == 0 {
		return nil, ErrInvalidConfig
	}
	return &Oracle{config: config}, nil
}

func (o *Oracle) Current() State {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

// Evaluate checks both families: absent routes are unreachable, untrusted target sets remain unknown.
func (o *Oracle) Evaluate(ctx context.Context, network networkstate.State) (State, error) {
	if ctx == nil || network.Generation == 0 {
		return State{}, ErrInvalidConfig
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return State{}, ErrClosed
	}
	o.sequence++
	sequence := o.sequence
	previous := o.state
	if previous.NetworkGeneration != network.Generation {
		previous = State{}
	}
	o.mu.Unlock()

	configured4, configured6 := configuredFamilies(network.Snapshot)
	ipv4, err4 := o.evaluateFamily(ctx, networkstate.FamilyIPv4, configured4, previous.IPv4)
	ipv6, err6 := o.evaluateFamily(ctx, networkstate.FamilyIPv6, configured6, previous.IPv6)
	result := State{NetworkGeneration: network.Generation, IPv4: ipv4, IPv6: ipv6}
	o.mu.Lock()
	if !o.closed && o.sequence == sequence {
		o.state = result
	}
	o.mu.Unlock()
	if err4 != nil || err6 != nil {
		return result, fmt.Errorf("underlay observation: %w", errorsJoin(err4, err6))
	}
	return result, nil
}

func (o *Oracle) evaluateFamily(ctx context.Context, family networkstate.Family, configured bool, previous FamilyEvidence) (FamilyEvidence, error) {
	now := o.config.Now().UTC()
	if !configured {
		return FamilyEvidence{State: Unreachable, ObservedAt: now}, nil
	}
	targets, err := o.config.Targets.Targets(ctx, family, o.config.TargetLimit)
	if err != nil {
		return FamilyEvidence{State: Unknown, ObservedAt: now, Failures: previous.Failures}, err
	}
	targets = validTargets(targets, family, o.config.TargetLimit)
	if len(targets) == 0 {
		return FamilyEvidence{State: Unknown, ObservedAt: now, Failures: previous.Failures}, nil
	}
	probeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		target Target
		err    error
	}
	jobs := make(chan Target)
	results := make(chan result, len(targets))
	workers := o.config.Parallelism
	if workers > len(targets) {
		workers = len(targets)
	}
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for target := range jobs {
				attempt, stop := context.WithTimeout(probeContext, o.config.ProbeTimeout)
				err := o.config.Prober.Probe(attempt, target)
				stop()
				select {
				case results <- result{target: target, err: err}:
				case <-probeContext.Done():
					return
				}
				if err == nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, target := range targets {
			select {
			case jobs <- target:
			case <-probeContext.Done():
				return
			}
		}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()
	for value := range results {
		if value.err == nil {
			return FamilyEvidence{State: Reachable, ObservedAt: now, Target: value.target}, nil
		}
	}
	failures := previous.Failures + 1
	state := Unknown
	if failures >= o.config.FailureThreshold {
		state = Unreachable
	}
	return FamilyEvidence{State: state, ObservedAt: now, Failures: failures}, nil
}

func (o *Oracle) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	return nil
}

func configuredFamilies(snapshot networkstate.Snapshot) (bool, bool) {
	links := make(map[int]networkstate.Link, len(snapshot.Links))
	for _, link := range snapshot.Links {
		links[link.Index] = link
	}
	var ipv4, ipv6 bool
	for _, route := range snapshot.Routes {
		link, ok := links[route.LinkIndex]
		if !ok || !link.Up {
			continue
		}
		for _, prefix := range link.Addresses {
			address := prefix.Addr()
			if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() ||
				address.IsLinkLocalUnicast() || address.Is4In6() {
				continue
			}
			if route.Family == networkstate.FamilyIPv4 && address.Is4() {
				ipv4 = true
			}
			if route.Family == networkstate.FamilyIPv6 && address.Is6() {
				ipv6 = true
			}
		}
	}
	return ipv4, ipv6
}

func validTargets(input []Target, family networkstate.Family, limit int) []Target {
	result := make([]Target, 0, limit)
	seen := make(map[netip.AddrPort]struct{}, limit)
	for _, target := range input {
		address := target.Endpoint.Addr()
		if !target.Endpoint.IsValid() || target.Endpoint.Port() == 0 || address.IsUnspecified() ||
			address.IsMulticast() || address.IsLinkLocalUnicast() || address.Is4In6() ||
			(family == networkstate.FamilyIPv4) != address.Is4() {
			continue
		}
		if _, exists := seen[target.Endpoint]; exists {
			continue
		}
		seen[target.Endpoint] = struct{}{}
		result = append(result, target)
		if len(result) == limit {
			break
		}
	}
	return result
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return fmt.Errorf("%v; %w", left, right)
}
