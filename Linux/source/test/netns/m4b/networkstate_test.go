// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

func TestM5C3_RealNetlinkNetworkGenerations(t *testing.T) {
	requirePrivileged(t)
	helper := assertNormalUserExecutable(t, "HYDRA_M5C3_NETWORKSTATE")
	lab := newLab(t, "m5c3-networkstate")
	client := lab.namespace("client")
	routerOld := lab.namespace("router1")
	routerNew := lab.namespace("router2")
	oldIf, oldPeer := lab.interfaceName('a'), lab.interfaceName('b')
	newIf, newPeer := lab.interfaceName('c'), lab.interfaceName('d')
	lab.createVeth(client, oldIf, routerOld, oldPeer)
	lab.createVeth(client, newIf, routerNew, newPeer)
	for _, namespace := range []string{client, routerOld, routerNew} {
		lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, link := range []struct{ namespace, name string }{
		{client, oldIf}, {routerOld, oldPeer}, {client, newIf}, {routerNew, newPeer},
	} {
		lab.inNamespace(link.namespace, "ip", "link", "set", link.name, "up")
	}

	old4, old4New := netip.MustParseAddr("10.81.1.2"), netip.MustParseAddr("10.81.1.3")
	new4 := netip.MustParseAddr("10.81.2.2")
	oldGW4, newGW4 := netip.MustParseAddr("10.81.1.1"), netip.MustParseAddr("10.81.2.1")
	old6, old6New := netip.MustParseAddr("2001:db8:81:1::2"), netip.MustParseAddr("2001:db8:81:1::3")
	new6 := netip.MustParseAddr("2001:db8:81:2::2")
	oldGW6, newGW6 := netip.MustParseAddr("2001:db8:81:1::1"), netip.MustParseAddr("2001:db8:81:2::1")
	for _, address := range []struct{ namespace, family, value, device string }{
		{client, "-4", old4.String() + "/24", oldIf}, {routerOld, "-4", oldGW4.String() + "/24", oldPeer},
		{client, "-4", new4.String() + "/24", newIf}, {routerNew, "-4", newGW4.String() + "/24", newPeer},
		{client, "-6", old6.String() + "/64", oldIf}, {routerOld, "-6", oldGW6.String() + "/64", oldPeer},
		{client, "-6", new6.String() + "/64", newIf}, {routerNew, "-6", newGW6.String() + "/64", newPeer},
	} {
		args := []string{address.family, "addr", "add", address.value, "dev", address.device}
		if address.family == "-6" {
			args = append(args, "nodad")
		}
		lab.inNamespace(address.namespace, "ip", args...)
	}
	lab.inNamespace(client, "ip", "route", "add", "default", "via", oldGW4.String(), "dev", oldIf, "metric", "100")
	lab.inNamespace(client, "ip", "route", "add", "default", "via", newGW4.String(), "dev", newIf, "metric", "200")
	lab.inNamespace(client, "ip", "-6", "route", "add", "default", "via", oldGW6.String(), "dev", oldIf, "metric", "100")
	lab.inNamespace(client, "ip", "-6", "route", "add", "default", "via", newGW6.String(), "dev", newIf, "metric", "200")
	lab.inNamespace(client, "ip", "link", "add", "unrelated0", "type", "dummy")
	lab.inNamespace(client, "ip", "link", "set", "unrelated0", "up")
	lab.inNamespace(client, "ip", "addr", "add", "192.0.2.2/24", "dev", "unrelated0")

	runtimeDir := lab.tempDir("networkstate")
	stateDir := filepath.Join(runtimeDir, "states")
	stopFile := filepath.Join(runtimeDir, "stop")
	errorFile := filepath.Join(runtimeDir, "error.json")
	child := lab.startChild("m5c3-networkstate", client, helper,
		"--state-dir", stateDir, "--stop-file", stopFile, "--error-file", errorFile,
		"--debounce", "100ms", "--timeout", "30s")

	state := waitNetworkGeneration(t, child, stateDir, 1)
	assertNetworkState(t, state, 4, 2, oldIf, old4, old6, 1500, true)

	// Unrelated event bursts must not multiply the generation for one relevant mutation.
	lab.inNamespace(client, "ip", "link", "set", "unrelated0", "mtu", "1300")
	lab.inNamespace(client, "ip", "addr", "replace", "192.0.2.3/24", "dev", "unrelated0")
	lab.inNamespace(client, "ip", "link", "set", oldIf, "mtu", "1400")
	state = waitNetworkGeneration(t, child, stateDir, 2)
	assertNetworkState(t, state, 4, 2, oldIf, old4, old6, 1400, true)

	runIPBatch(t, lab, client, "m5c3-ipv4-address", fmt.Sprintf(
		"address del %s/24 dev %s\naddress add %s/24 dev %s\nroute replace default via %s dev %s metric 100 onlink\n",
		old4, oldIf, old4New, oldIf, oldGW4, oldIf))
	state = waitNetworkGeneration(t, child, stateDir, 3)
	assertNetworkState(t, state, 4, 2, oldIf, old4New, old6, 1400, true)

	runIP6Batch(t, lab, client, "m5c3-ipv6-address", fmt.Sprintf(
		"address add %s/64 dev %s nodad\naddress del %s/64 dev %s\nroute replace default via %s dev %s metric 100 onlink\n",
		old6New, oldIf, old6, oldIf, oldGW6, oldIf))
	state = waitNetworkGeneration(t, child, stateDir, 4)
	assertNetworkState(t, state, 4, 2, oldIf, old4New, old6New, 1400, true)

	runIPBatch(t, lab, client, "m5c3-default-route-v4", fmt.Sprintf(
		"route del default via %s dev %s metric 100\nroute del default via %s dev %s metric 200\nroute add default via %s dev %s metric 100\n",
		oldGW4, oldIf, newGW4, newIf, newGW4, newIf))
	runIP6Batch(t, lab, client, "m5c3-default-route-v6", fmt.Sprintf(
		"route del default via %s dev %s metric 100\nroute del default via %s dev %s metric 200\nroute add default via %s dev %s metric 100\n",
		oldGW6, oldIf, newGW6, newIf, newGW6, newIf))
	state = waitNetworkGeneration(t, child, stateDir, 5)
	assertNetworkState(t, state, 2, 1, newIf, new4, new6, 1500, true)

	lab.inNamespace(client, "ip", "link", "set", newIf, "mtu", "1450")
	state = waitNetworkGeneration(t, child, stateDir, 6)
	assertNetworkState(t, state, 2, 1, newIf, new4, new6, 1450, true)
	lab.inNamespace(client, "ip", "link", "set", newIf, "down")
	state = waitNetworkGeneration(t, child, stateDir, 7)
	if len(state.Snapshot.Routes) != 0 || len(state.Snapshot.Links) != 0 {
		t.Fatalf("down egress remained usable: %+v", state.Snapshot)
	}
	lab.inNamespace(client, "ip", "link", "set", newIf, "up")
	lab.inNamespace(client, "ip", "-6", "addr", "replace", new6.String()+"/64", "dev", newIf, "nodad")
	lab.inNamespace(client, "ip", "route", "replace", "default", "via", newGW4.String(), "dev", newIf, "metric", "100")
	lab.inNamespace(client, "ip", "-6", "route", "replace", "default", "via", newGW6.String(), "dev", newIf, "metric", "100", "onlink")
	state = waitNetworkGeneration(t, child, stateDir, 8)
	assertNetworkState(t, state, 2, 1, newIf, new4, new6, 1450, true)

	if err := touch(stopFile); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := child.wait(ctx); err != nil {
		t.Fatalf("network-state helper shutdown: %v", err)
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) != 8 || !strings.Contains(names[len(names)-1], "00000000000000000008") {
		t.Fatalf("netlink event storm produced unexpected semantic generations: %v", names)
	}
}

func runIPBatch(t *testing.T, lab *lab, namespace, label, commands string) {
	t.Helper()
	path := filepath.Join(lab.artifactDir, label+".batch")
	if err := os.WriteFile(path, []byte(commands), 0o600); err != nil {
		t.Fatal(err)
	}
	lab.inNamespace(namespace, "ip", "-batch", path)
}

func runIP6Batch(t *testing.T, lab *lab, namespace, label, commands string) {
	t.Helper()
	path := filepath.Join(lab.artifactDir, label+".batch")
	if err := os.WriteFile(path, []byte(commands), 0o600); err != nil {
		t.Fatal(err)
	}
	lab.inNamespace(namespace, "ip", "-6", "-batch", path)
}

func waitNetworkGeneration(t *testing.T, child *childProcess, directory string, generation networkstate.Generation) networkstate.State {
	t.Helper()
	path := filepath.Join(directory, fmt.Sprintf("generation-%020d.json", generation))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, path); err != nil {
		t.Fatal(err)
	}
	var state networkstate.State
	if err := readJSON(path, &state); err != nil {
		t.Fatal(err)
	}
	if state.Generation != generation {
		t.Fatalf("generation file contains %d, want %d", state.Generation, generation)
	}
	return state
}

func assertNetworkState(t *testing.T, state networkstate.State, routes, links int, name string, ipv4, ipv6 netip.Addr, mtu int, up bool) {
	t.Helper()
	if len(state.Snapshot.Routes) != routes || len(state.Snapshot.Links) != links {
		t.Fatalf("unexpected normalized snapshot size: %+v", state)
	}
	for _, link := range state.Snapshot.Links {
		if link.Name != name {
			continue
		}
		if link.MTU != mtu || link.Up != up || !containsPrefixAddress(link.Addresses, ipv4) || !containsPrefixAddress(link.Addresses, ipv6) {
			t.Fatalf("unexpected active link state: %+v", link)
		}
		return
	}
	t.Fatalf("snapshot lacks active link %s: %+v", name, state.Snapshot)
}

func containsPrefixAddress(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Addr() == address {
			return true
		}
	}
	return false
}
