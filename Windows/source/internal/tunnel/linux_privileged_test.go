// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package tunnel

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

const privilegedTunnelLifecycleEnvironment = "HYDRA_TUNNEL_PRIVILEGED"

type processFDSnapshot struct {
	total   int
	netlink int
	pipes   int
	tun     int
}

func TestLinuxNativeTunLifecycleResources(t *testing.T) {
	if os.Getenv(privilegedTunnelLifecycleEnvironment) != "1" || os.Geteuid() != 0 {
		t.Skip("set HYDRA_TUNNEL_PRIVILEGED=1 and execute the prebuilt test binary as root")
	}
	name := fmt.Sprintf("hytun%x", os.Getpid()&0xffff)
	if _, err := netlink.LinkByName(name); err == nil {
		t.Fatalf("refusing to use existing interface %q", name)
	}

	createCloseRealTUN(t, name, true)
	baseline := snapshotProcessFDs(t)
	for cycle := 1; cycle <= 500; cycle++ {
		createCloseRealTUN(t, name, false)
		if cycle%100 != 0 {
			continue
		}
		current := snapshotProcessFDs(t)
		if current.netlink != baseline.netlink || current.pipes != baseline.pipes || current.tun != baseline.tun || current.total > baseline.total+2 {
			t.Fatalf("cycle %d leaked NativeTun resources: baseline=%+v current=%+v", cycle, baseline, current)
		}
	}
	final := snapshotProcessFDs(t)
	if final.netlink != baseline.netlink || final.pipes != baseline.pipes || final.tun != baseline.tun || final.total > baseline.total+2 {
		t.Fatalf("500 lifecycle cycles leaked NativeTun resources: baseline=%+v final=%+v", baseline, final)
	}
	t.Logf("500 lifecycle cycles: baseline=%+v final=%+v", baseline, final)
}

func createCloseRealTUN(t *testing.T, name string, burst bool) {
	t.Helper()
	owner, err := NewLinux(Config{
		Name: name, MTU: 1280,
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.18.255.1/32")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if burst {
		link, err := netlink.LinkByName(name)
		if err != nil {
			t.Fatal(err)
		}
		for range 32 {
			if err := netlink.LinkSetDown(link); err != nil {
				t.Fatal(err)
			}
			if err := netlink.LinkSetUp(link); err != nil {
				t.Fatal(err)
			}
		}
	}
	done := make(chan error, 1)
	go func() { done <- owner.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner NativeTun close did not complete")
	}
	if _, err := netlink.LinkByName(name); err == nil {
		t.Fatalf("closed owner left interface %q", name)
	}
}

func snapshotProcessFDs(t *testing.T) processFDSnapshot {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	sockets := make(map[uint64]struct{})
	snapshot := processFDSnapshot{total: len(entries)}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(target, "pipe:["):
			snapshot.pipes++
		case target == "/dev/net/tun":
			snapshot.tun++
		case strings.HasPrefix(target, "socket:["):
			inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
			if err == nil {
				sockets[inode] = struct{}{}
			}
		}
	}
	file, err := os.Open("/proc/self/net/netlink")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 0 {
				continue
			}
			inode, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
			if err == nil {
				if _, owned := sockets[inode]; owned {
					snapshot.netlink++
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
