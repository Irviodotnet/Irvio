// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitbridge

import "sync/atomic"

// Traffic survives bridge generations and counts accepted IP bytes, not encrypted transport overhead.
type Traffic struct {
	myUpload, myDownload, sharedUpload atomic.Uint64
}

type TrafficSnapshot struct {
	MyUpload, MyDownload, SharedUpload uint64
}

func (t *Traffic) Snapshot() TrafficSnapshot {
	if t == nil {
		return TrafficSnapshot{}
	}
	return TrafficSnapshot{t.myUpload.Load(), t.myDownload.Load(), t.sharedUpload.Load()}
}

func (p *Peer) isControl(m packetFrame, outbound bool) bool {
	if !m.tcp || !p.control.IsValid() {
		return false
	}
	if outbound {
		return m.dst == p.control.Addr() && m.dport == p.control.Port()
	}
	return m.src == p.control.Addr() && m.sport == p.control.Port()
}
