// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"sort"
	"sync"
	"time"
)

type metrics struct {
	mu             sync.Mutex
	stats          Stats
	probeActive    uint64
	probeSamples   []time.Duration
	connectSamples []time.Duration
	capacity       int
}

func newMetrics(capacity int) *metrics { return &metrics{capacity: capacity} }

func (m *metrics) providerRequest() { m.mu.Lock(); m.stats.ProviderRequests++; m.mu.Unlock() }
func (m *metrics) providerFailure() { m.mu.Lock(); m.stats.ProviderFailures++; m.mu.Unlock() }
func (m *metrics) batch(size int) {
	m.mu.Lock()
	m.stats.BatchesReceived++
	m.stats.CandidatesReceived += uint64(size)
	m.mu.Unlock()
}
func (m *metrics) probeStart() {
	m.mu.Lock()
	m.stats.ProbesStarted++
	m.probeActive++
	if m.probeActive > m.stats.MaximumConcurrentProbes {
		m.stats.MaximumConcurrentProbes = m.probeActive
	}
	m.mu.Unlock()
}
func (m *metrics) probeDone(latency time.Duration) {
	m.mu.Lock()
	m.stats.ProbesCompleted++
	if m.probeActive > 0 {
		m.probeActive--
	}
	m.probeSamples = appendBounded(m.probeSamples, latency, m.capacity)
	m.mu.Unlock()
}
func (m *metrics) connectStart() { m.mu.Lock(); m.stats.ConnectAttempts++; m.mu.Unlock() }
func (m *metrics) connectDone(outcome ConnectOutcome, latency time.Duration) {
	m.mu.Lock()
	switch outcome {
	case OutcomeConnected:
		m.stats.Connected++
		m.connectSamples = appendBounded(m.connectSamples, latency, m.capacity)
	case OutcomeBusy:
		m.stats.Busy++
	case OutcomeUnavailable, OutcomeRouteFailed:
		m.stats.Unavailable++
	case OutcomeLocalPromotionFailed:
		m.stats.LocalPromotionFailures++
	default:
		m.stats.Failed++
	}
	m.mu.Unlock()
}
func (m *metrics) mutate(apply func(*Stats)) { m.mu.Lock(); apply(&m.stats); m.mu.Unlock() }

func appendBounded(values []time.Duration, value time.Duration, capacity int) []time.Duration {
	if value < 0 {
		return values
	}
	if len(values) == capacity {
		copy(values, values[1:])
		values[len(values)-1] = value
		return values
	}
	return append(values, value)
}

func (m *metrics) snapshot() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := m.stats
	result.ProbeLatencyP50 = percentile(m.probeSamples, 50)
	result.ProbeLatencyP95 = percentile(m.probeSamples, 95)
	result.ConnectLatencyP50 = percentile(m.connectSamples, 50)
	result.ConnectLatencyP95 = percentile(m.connectSamples, 95)
	return result
}

func percentile(values []time.Duration, percent int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := (len(copyValues)*percent + 99) / 100
	if index < 1 {
		index = 1
	}
	return copyValues[index-1]
}
