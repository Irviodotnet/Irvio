// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

// Package trafficstats contains local-only, best-effort traffic accounting.
package trafficstats

import (
	"errors"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const CounterName = "irvio_user_upload"
const DownloadCounterName = "irvio_user_download"

// Ensure stages a missing counter in the caller's atomic batch without resetting existing values.
func Ensure(conn *nftables.Conn, table *nftables.Table, fresh bool, names ...string) error {
	name := CounterName
	if len(names) > 0 {
		name = names[0]
	}
	if !fresh {
		_, err := Read(table, name)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	conn.AddObject(&nftables.CounterObj{Table: table, Name: name})
	return nil
}

func Reference() expr.Any { return &expr.Objref{Type: unix.NFT_OBJECT_COUNTER, Name: CounterName} }
func DownloadReference() expr.Any {
	return &expr.Objref{Type: unix.NFT_OBJECT_COUNTER, Name: DownloadCounterName}
}

// Read opens no lasting socket and bounds netlink I/O independently of Core.
func Read(table *nftables.Table, names ...string) (uint64, error) {
	name := CounterName
	if len(names) > 0 {
		name = names[0]
	}
	conn, err := nftables.New(nftables.WithSockOptions(func(c *netlink.Conn) error {
		return c.SetDeadline(time.Now().Add(250 * time.Millisecond))
	}))
	if err != nil {
		return 0, err
	}
	object, err := conn.GetObject(&nftables.CounterObj{Table: table, Name: name})
	if err != nil {
		return 0, err
	}
	counter, ok := object.(*nftables.CounterObj)
	if !ok {
		return 0, errors.New("unexpected traffic counter type")
	}
	return counter.Bytes, nil
}
