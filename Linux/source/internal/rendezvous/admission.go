// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"net/netip"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type admissionOperation uint8

const (
	admitRegister admissionOperation = iota + 1
	admitHeartbeat
	admitGET
	admitSample
	admitSignal
)

type admissionKey struct {
	identity string
	peer     bool
	op       admissionOperation
}

type admissionBucket struct {
	tokens int
	last   time.Time
}

type requestAdmission struct {
	policy AdmissionPolicy
	now    func() time.Time

	mu      sync.Mutex
	buckets map[admissionKey]admissionBucket
}

func newRequestAdmission(policy AdmissionPolicy, now func() time.Time) (*requestAdmission, error) {
	if err := policy.validate(); err != nil || now == nil {
		return nil, ErrInvalid
	}
	return &requestAdmission{policy: policy, now: now, buckets: make(map[admissionKey]admissionBucket)}, nil
}

func (a *requestAdmission) Allow(remote peer.ID, address ma.Multiaddr, operation admissionOperation) bool {
	if a == nil || remote == "" {
		return false
	}
	source := sourceIdentity(address)
	if source == "" {
		return false
	}
	now := a.now()
	interval, burst := a.limit(operation)
	keys := [...]admissionKey{{identity: remote.String(), peer: true, op: operation}, {identity: source, op: operation}}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, key := range keys {
		keyBurst := burst
		if !key.peer {
			keyBurst *= a.policy.SourceBurstMultiplier
		}
		bucket := a.refillLocked(key, now, interval, keyBurst)
		if bucket.tokens == 0 {
			return false
		}
	}
	for _, key := range keys {
		keyBurst := burst
		if !key.peer {
			keyBurst *= a.policy.SourceBurstMultiplier
		}
		bucket := a.refillLocked(key, now, interval, keyBurst)
		bucket.tokens--
		bucket.last = now
		a.buckets[key] = bucket
	}
	return true
}

func (a *requestAdmission) refillLocked(key admissionKey, now time.Time, interval time.Duration, burst int) admissionBucket {
	bucket, exists := a.buckets[key]
	if !exists {
		if len(a.buckets) >= a.policy.MaxKeys {
			a.evictExpiredLocked(now)
		}
		if len(a.buckets) >= a.policy.MaxKeys {
			return admissionBucket{last: now}
		}
		return admissionBucket{tokens: burst, last: now}
	}
	if now.After(bucket.last) {
		refill := int(now.Sub(bucket.last) / interval)
		if refill > 0 {
			bucket.tokens = min(burst, bucket.tokens+refill)
			bucket.last = bucket.last.Add(time.Duration(refill) * interval)
		}
	}
	return bucket
}

func (a *requestAdmission) evictExpiredLocked(now time.Time) {
	for key, bucket := range a.buckets {
		interval, burst := a.limit(key.op)
		if !key.peer {
			burst *= a.policy.SourceBurstMultiplier
		}
		if now.Sub(bucket.last) > time.Duration(burst+1)*interval {
			delete(a.buckets, key)
		}
	}
}

func (a *requestAdmission) limit(operation admissionOperation) (time.Duration, int) {
	switch operation {
	case admitRegister:
		return a.policy.RegisterInterval, a.policy.RegisterBurst
	case admitHeartbeat:
		return a.policy.HeartbeatInterval, a.policy.HeartbeatBurst
	case admitGET:
		return a.policy.GETInterval, a.policy.GETBurst
	case admitSample:
		return a.policy.SampleInterval, a.policy.SampleBurst
	default:
		return a.policy.SignalInterval, a.policy.SignalBurst
	}
}

func sourceIdentity(address ma.Multiaddr) string {
	for _, code := range []int{ma.P_IP4, ma.P_IP6} {
		value, err := address.ValueForProtocol(code)
		if err != nil {
			continue
		}
		parsed, err := netip.ParseAddr(value)
		if err != nil || parsed.Is4In6() {
			continue
		}
		bits := 64
		if parsed.Is4() {
			bits = 24
		}
		return netip.PrefixFrom(parsed, bits).Masked().String()
	}
	return ""
}
