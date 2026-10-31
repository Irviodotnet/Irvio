// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import "sync/atomic"

// Stats is a race-safe snapshot of one Attempt's activity.
type Stats struct {
	AttemptsCreated           uint64
	RemoteCandidateSetsLoaded uint64
	RemoteCandidatesAdded     uint64
	ConnectAttemptsStarted    uint64
	DialAttempts              uint64
	AcceptAttempts            uint64
	Connected                 uint64
	Failed                    uint64
	Canceled                  uint64
	Timeouts                  uint64
	SelectedUDP4              uint64
	SelectedUDP6              uint64
	SelectedHostHost          uint64
	SelectedRemoteHost        uint64
	SelectedRemoteSrflx       uint64
	SelectedRemotePrflx       uint64
	PeerReflexiveObserved     uint64
	CandidateLoadFailures     uint64
	StateMisuseErrors         uint64
}

type counters struct {
	attemptsCreated           atomic.Uint64
	remoteCandidateSetsLoaded atomic.Uint64
	remoteCandidatesAdded     atomic.Uint64
	connectAttemptsStarted    atomic.Uint64
	dialAttempts              atomic.Uint64
	acceptAttempts            atomic.Uint64
	connected                 atomic.Uint64
	failed                    atomic.Uint64
	canceled                  atomic.Uint64
	timeouts                  atomic.Uint64
	selectedUDP4              atomic.Uint64
	selectedUDP6              atomic.Uint64
	selectedHostHost          atomic.Uint64
	selectedRemoteHost        atomic.Uint64
	selectedRemoteSrflx       atomic.Uint64
	selectedRemotePrflx       atomic.Uint64
	peerReflexiveObserved     atomic.Uint64
	candidateLoadFailures     atomic.Uint64
	stateMisuseErrors         atomic.Uint64
}

func (c *counters) snapshot() Stats {
	return Stats{
		AttemptsCreated: c.attemptsCreated.Load(), RemoteCandidateSetsLoaded: c.remoteCandidateSetsLoaded.Load(),
		RemoteCandidatesAdded: c.remoteCandidatesAdded.Load(), ConnectAttemptsStarted: c.connectAttemptsStarted.Load(),
		DialAttempts: c.dialAttempts.Load(), AcceptAttempts: c.acceptAttempts.Load(), Connected: c.connected.Load(),
		Failed: c.failed.Load(), Canceled: c.canceled.Load(), Timeouts: c.timeouts.Load(),
		SelectedUDP4: c.selectedUDP4.Load(), SelectedUDP6: c.selectedUDP6.Load(),
		SelectedHostHost: c.selectedHostHost.Load(), CandidateLoadFailures: c.candidateLoadFailures.Load(),
		SelectedRemoteHost: c.selectedRemoteHost.Load(), SelectedRemoteSrflx: c.selectedRemoteSrflx.Load(),
		SelectedRemotePrflx: c.selectedRemotePrflx.Load(), PeerReflexiveObserved: c.peerReflexiveObserved.Load(),
		StateMisuseErrors: c.stateMisuseErrors.Load(),
	}
}
