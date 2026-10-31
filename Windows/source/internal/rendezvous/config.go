// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"fmt"
	"time"
)

const (
	DefaultMaxRegistrations       = 10_000
	DefaultMaxPendingSignaling    = 256
	DefaultMaxOutgoingSample      = 32
	DefaultMaxConcurrentDHTLookup = 8
	DefaultClientGETConcurrency   = 2
)

type Config struct {
	MaxRegistrations       int
	MaxPendingSignaling    int
	MaxOutgoingSample      int
	MaxConcurrentDHTLookup int
	ClientGETConcurrency   int
	Admission              AdmissionPolicy

	HeartbeatInterval  time.Duration
	RegistrationStale  time.Duration
	RegistrationExpiry time.Duration
	DescriptorTTL      time.Duration
	ReceiptTTL         time.Duration
	ReceiptRenewLead   time.Duration
	PrepareTimeout     time.Duration
	PendingTimeout     time.Duration
	ActivationTimeout  time.Duration
	ClockSkew          time.Duration
	ObservedIPPolicy   ObservedIPPolicy

	Now func() time.Time
}

type AdmissionPolicy struct {
	MaxKeys               int
	SourceBurstMultiplier int
	RegisterInterval      time.Duration
	RegisterBurst         int
	HeartbeatInterval     time.Duration
	HeartbeatBurst        int
	GETInterval           time.Duration
	GETBurst              int
	SampleInterval        time.Duration
	SampleBurst           int
	SignalInterval        time.Duration
	SignalBurst           int
}

func DefaultAdmissionPolicy() AdmissionPolicy {
	return AdmissionPolicy{
		MaxKeys: 40_000, SourceBurstMultiplier: 64,
		RegisterInterval: 10 * time.Second, RegisterBurst: 2,
		HeartbeatInterval: 5 * time.Second, HeartbeatBurst: 3,
		GETInterval: time.Second, GETBurst: 3,
		SampleInterval: time.Second, SampleBurst: 3,
		SignalInterval: 250 * time.Millisecond, SignalBurst: 8,
	}
}

type ObservedIPPolicy struct {
	AllowLoopback      bool
	AllowPrivate       bool
	AllowDocumentation bool
}

func DefaultConfig() Config {
	return Config{
		MaxRegistrations:       DefaultMaxRegistrations,
		MaxPendingSignaling:    DefaultMaxPendingSignaling,
		MaxOutgoingSample:      DefaultMaxOutgoingSample,
		MaxConcurrentDHTLookup: DefaultMaxConcurrentDHTLookup,
		ClientGETConcurrency:   DefaultClientGETConcurrency,
		Admission:              DefaultAdmissionPolicy(),
		HeartbeatInterval:      30 * time.Second,
		RegistrationStale:      90 * time.Second,
		RegistrationExpiry:     120 * time.Second,
		DescriptorTTL:          10 * time.Minute,
		ReceiptTTL:             10 * time.Minute,
		ReceiptRenewLead:       5 * time.Minute,
		PrepareTimeout:         1500 * time.Millisecond,
		PendingTimeout:         20 * time.Second,
		ActivationTimeout:      30 * time.Second,
		ClockSkew:              5 * time.Minute,
		Now:                    time.Now,
	}
}

func (c Config) descriptorRefreshCheckInterval() time.Duration {
	interval := c.DescriptorTTL / 8
	return interval
}

func (c Config) effectiveAdmissionPolicy() AdmissionPolicy {
	policy := c.Admission
	// Permit descriptor UPDATE before expiry for every configured lifetime.
	if interval := c.descriptorRefreshCheckInterval(); policy.RegisterInterval > interval {
		policy.RegisterInterval = interval
	}
	return policy
}

func (c Config) validate() error {
	if c.MaxRegistrations < 1 || c.MaxRegistrations > 1_000_000 ||
		c.MaxPendingSignaling < 1 || c.MaxPendingSignaling > 65_536 ||
		c.MaxOutgoingSample < 1 || c.MaxOutgoingSample > 256 ||
		c.MaxConcurrentDHTLookup < 1 || c.MaxConcurrentDHTLookup > 64 ||
		c.ClientGETConcurrency < 1 || c.ClientGETConcurrency > 2 {
		return fmt.Errorf("%w: capacity", ErrInvalid)
	}
	if err := c.Admission.validate(); err != nil {
		return err
	}
	if c.HeartbeatInterval <= 0 || c.RegistrationStale <= c.HeartbeatInterval ||
		c.RegistrationExpiry <= c.RegistrationStale || c.DescriptorTTL < time.Second ||
		c.ReceiptTTL <= 0 || c.ReceiptTTL > c.DescriptorTTL ||
		c.ReceiptRenewLead <= 0 || c.ReceiptRenewLead >= c.ReceiptTTL ||
		c.PrepareTimeout <= 0 || c.PendingTimeout < c.PrepareTimeout ||
		c.ActivationTimeout <= 0 || c.ClockSkew < 0 || c.Now == nil {
		return fmt.Errorf("%w: timing policy", ErrInvalid)
	}
	return nil
}

func (p AdmissionPolicy) validate() error {
	if p.MaxKeys < 2 || p.MaxKeys > 2_000_000 || p.SourceBurstMultiplier < 1 || p.SourceBurstMultiplier > 1024 ||
		p.RegisterInterval <= 0 || p.RegisterBurst < 1 || p.RegisterBurst > 100 ||
		p.HeartbeatInterval <= 0 || p.HeartbeatBurst < 1 || p.HeartbeatBurst > 100 ||
		p.GETInterval <= 0 || p.GETBurst < 1 || p.GETBurst > 100 ||
		p.SampleInterval <= 0 || p.SampleBurst < 1 || p.SampleBurst > 100 ||
		p.SignalInterval <= 0 || p.SignalBurst < 1 || p.SignalBurst > 100 {
		return fmt.Errorf("%w: admission policy", ErrInvalid)
	}
	return nil
}
