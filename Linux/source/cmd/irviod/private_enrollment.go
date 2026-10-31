// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"errors"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ResumePrivateEnrollment performs bounded control-plane work without requesting a user tunnel.
func (d *desktopController) ResumePrivateEnrollment(ctx context.Context, host peer.ID) (status irviov1.PrivateEnrollmentResponse_Status, resultErr error) {
	if err := d.lockPrivateManagement(ctx); err != nil {
		return status, err
	}
	defer d.mu.Unlock()
	state, err := d.privateStore.Snapshot()
	if err != nil {
		return status, err
	}
	entries, err := state.EnrollmentSummaries()
	if err != nil {
		return status, err
	}
	found := false
	for _, entry := range entries {
		found = found || entry.HostPeerID == host.String()
	}
	if !found {
		return status, irvioruntime.ErrInvalidConfig
	}
	ctx, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	daemon := d.daemon
	if daemon == nil {
		if d.desiredPrivate.active() || d.desiredCountry != "" || d.recoveryPending {
			return status, irvioruntime.ErrInvalidState
		}
		addresses, err := d.bootstrap.StartupAddresses()
		if err != nil {
			return status, err
		}
		if len(addresses) == 0 {
			return status, irvioruntime.ErrInvalidConfig
		}
		daemon, err = irvioruntime.NewDaemon(irvioruntime.DaemonConfig{Platform: productDaemonPlatform(d.platform), DataDir: d.dataDir, Bootstrap: addresses,
			DHTMode: "auto", Logger: d.logger, M12: irvioruntime.M12Config{Enabled: true, Policy: rendezvous.DefaultConfig()}})
		if err != nil {
			return status, err
		}
		runCtx, cancel := context.WithCancel(d.parent)
		stopRequest := context.AfterFunc(ctx, cancel)
		defer func() {
			stopRequest()
			cancel()
			cleanup, stopCleanup := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopCleanup()
			if err := daemon.Close(cleanup); err != nil {
				d.daemon, d.cancel, d.teardownPending = daemon, cancel, true
				resultErr = errors.Join(resultErr, err)
			}
		}()
		if err := daemon.Start(runCtx); err != nil {
			return status, err
		}
	}
	node := daemon.Node()
	if node == nil {
		return status, irvioruntime.ErrNotStarted
	}
	client, err := rendezvous.NewClient(node.Host(), 2*time.Second)
	if err != nil {
		return status, err
	}
	dial, err := client.PrivateEnrollmentDialer(node)
	if err != nil {
		return status, err
	}
	return d.privateStore.ResumeEnrollment(ctx, host, node.IdentityPrivateKey(), dial)
}
