// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"fmt"
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"google.golang.org/protobuf/proto"
)

// SetPrivateExitReady publishes only a private descriptor; its caller owns ACL and data-plane readiness.
func (n *Node) SetPrivateExitReady(ready bool) error {
	if n == nil {
		return fmt.Errorf("invalid private Exit")
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	if n.closed || !n.exitConfigured {
		return fmt.Errorf("private Exit unavailable")
	}
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if ready && (n.providerStarted || n.announcement.Capabilities&protocol.CapExit != 0) {
		return fmt.Errorf("public Exit must be withdrawn before private hosting")
	}
	if n.privateExitActive == ready {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	next := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	next.SessionFeatures = removeFeature(next.SessionFeatures, protocol.PrivateExitFeature)
	next.Capabilities &^= protocol.CapPrivateExit
	next.AcceptingNewSessions = false
	next.Country = n.country
	if ready {
		if !containsFeature(next.SessionFeatures, "session-establish-v1") || !containsFeature(next.SessionFeatures, "session-control-v1") {
			return fmt.Errorf("private Exit requires session establishment and control")
		}
		next.Capabilities |= protocol.CapPrivateExit
		next.SessionFeatures = append(next.SessionFeatures, protocol.PrivateExitFeature)
		next.AcceptingNewSessions = true
		next.Country = ""
	}
	now := time.Now()
	next.Sequence++
	next.CreatedAtUnix, next.ExpiresAtUnix = now.Unix(), now.Add(n.announcementTTL).Unix()
	signed, err := protocol.SealAnnouncement(n.privateKey, next, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement, n.signedEnvelope = next, append([]byte(nil), signed...)
	n.privateExitActive = ready
	return nil
}

func (n *Node) PrivateExitActive() bool {
	if n == nil {
		return false
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	return !n.closed && n.privateExitActive
}
