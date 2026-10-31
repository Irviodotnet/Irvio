// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type privateResourceSample struct {
	Cycle  int                     `json:"cycle"`
	Host   privateProcessResources `json:"host"`
	Client privateProcessResources `json:"client"`
}

type privateProcessResources struct {
	FD, Pipes, Threads, Sockets, Netlink int
	RSSKiB                               int
}

func runPrivateLifecycleCycles(t *testing.T, ctx context.Context, lab *lab, host, client *localapi.Client,
	desktops map[int]*childProcess, namespaces []string, ips []netip.Addr, hostID string, cycles int, baselineNetwork map[int]map[string]string) {
	t.Helper()
	disconnect := func() {
		t.Helper()
		for _, api := range []*localapi.Client{client, host} {
			if err := api.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
			status := waitPrivateRuntimeState(t, ctx, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
			if status.PrivateHostingWanted || status.PrivateTargetPeerId != "" || status.ContributionEnabled {
				t.Fatal("IDLE retains private intent")
			}
			stats, err := api.GetTrafficStats(ctx)
			if err != nil || stats.GetClients() != 0 || stats.GetActive() {
				t.Fatalf("IDLE retains active traffic/downstream clients: stats=%v err=%v", stats, err)
			}
			if api == client {
				waitPrivateDownstreamReleased(t, ctx, host)
			}
		}
	}
	connect := func() {
		t.Helper()
		// Respect the production ten-second registration token refill without weakening admission.
		select {
		case <-time.After(10200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		path := filepath.Join(lab.artifactDir, "private-host.stderr.log")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true}); err != nil {
			t.Fatal(err)
		}
		waitPrivateRuntimeState(t, ctx, host, localv1.ConnectionState_CONNECTION_STATE_HOSTING)
		waitPrivateRegistrationAfter(t, ctx, path, info.Size())
		if _, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: hostID}); err != nil {
			t.Fatal(err)
		}
		status := waitPrivateRuntimeState(t, ctx, client, localv1.ConnectionState_CONNECTION_STATE_CONNECTED)
		if status.PrivateTargetPeerId != hostID {
			t.Fatal("lifecycle changed private target")
		}
	}
	disconnect()
	sample := func(cycle int) privateResourceSample {
		return privateResourceSample{Cycle: cycle,
			Host:   privateResources(t, desktops[5].command.Process.Pid),
			Client: privateResources(t, desktops[6].command.Process.Pid)}
	}
	baseline := sample(0)
	samples := []privateResourceSample{baseline}
	for cycle := 1; cycle <= cycles; cycle++ {
		connect()
		probe := runM7Probe(t, lab, namespaces[6], "http", netip.AddrPortFrom(ips[7], 18080))
		source, err := netip.ParseAddrPort(probe.ObservedSource)
		if err != nil || !probe.Success || source.Addr() != ips[5] {
			t.Fatalf("cycle %d private user-plane failure: %+v parse=%v", cycle, probe, err)
		}
		disconnect()
		current := sample(cycle)
		samples = append(samples, current)
		data, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lab.artifactDir, "private-lifecycle-resources.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, index := range []int{5, 6} {
			actual := privateIdleNetwork(t, lab, namespaces[index])
			if !reflect.DeepEqual(baselineNetwork[index], actual) {
				before, _ := json.MarshalIndent(baselineNetwork[index], "", "  ")
				after, _ := json.MarshalIndent(actual, "", "  ")
				_ = os.WriteFile(filepath.Join(lab.artifactDir, fmt.Sprintf("private-residue-%d-before.json", index)), before, 0o600)
				_ = os.WriteFile(filepath.Join(lab.artifactDir, fmt.Sprintf("private-residue-%d-after.json", index)), after, 0o600)
				t.Fatalf("cycle %d namespace %d retained network residue", cycle, index)
			}
		}
		for name, pair := range map[string][2]privateProcessResources{"host": {baseline.Host, current.Host}, "client": {baseline.Client, current.Client}} {
			before, after := pair[0], pair[1]
			if after.FD > before.FD+8 || after.Pipes > before.Pipes+4 || after.Sockets > before.Sockets+4 || after.Netlink > before.Netlink || after.Threads > before.Threads+32 {
				t.Fatalf("cycle %d %s resource bound exceeded: baseline=%+v current=%+v", cycle, name, before, after)
			}
			// This RSS ceiling protects the lab; leak classification requires the recorded GC/RSS trend.
			if after.RSSKiB <= 0 || after.RSSKiB > 512*1024 {
				t.Fatalf("cycle %d %s RSS diagnostic safety ceiling exceeded: %+v", cycle, name, after)
			}
		}
		if cycle%10 == 0 {
			t.Logf("private lifecycle cycle=%d/%d host=%+v client=%+v", cycle, cycles, current.Host, current.Client)
		}
	}
	log, err := os.ReadFile(filepath.Join(lab.artifactDir, "private-client.stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "TUNNEL_RELEASE_UNCONFIRMED") {
		t.Fatal("normal lifecycle disconnect lost RELEASE acknowledgment")
	}
	connect()
}

func waitPrivateDownstreamReleased(t *testing.T, parent context.Context, host *localapi.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	for {
		status, err := host.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_HOSTING {
			t.Fatalf("lease cleanup oracle requires a still-hosting Exit: status=%v err=%v", status, err)
		}
		stats, err := host.GetTrafficStats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if stats.GetClients() == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("client DISCONNECT left downstream leases on the still-hosting Exit")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func privateIdleNetwork(t *testing.T, lab *lab, namespace string) map[string]string {
	t.Helper()
	commands := map[string][]string{
		"links": {"ip", "-j", "link", "show"}, "addresses": {"ip", "-j", "addr", "show"},
		"routes4": {"ip", "-j", "-4", "route", "show", "table", "all"}, "routes6": {"ip", "-j", "-6", "route", "show", "table", "all"},
		"rules4": {"ip", "-j", "rule", "show"}, "rules6": {"ip", "-j", "-6", "rule", "show"},
		"nft": {"nft", "-j", "list", "ruleset"},
	}
	result := make(map[string]string)
	for name, args := range commands {
		output := lab.inNamespace(namespace, args[0], args[1:]...)
		normalized, err := normalizeJSON([]byte(output))
		if err != nil {
			t.Fatal(err)
		}
		result[name] = normalized
	}
	return result
}

func waitPrivateAddressReady(t *testing.T, parent context.Context, lab *lab, namespace string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		var links []struct {
			Addresses []struct {
				Tentative bool `json:"tentative"`
				Failed    bool `json:"dadfailed"`
			} `json:"addr_info"`
		}
		data := lab.inNamespace(namespace, "ip", "-j", "-6", "addr", "show")
		if err := json.Unmarshal([]byte(data), &links); err != nil {
			t.Fatal(err)
		}
		ready := true
		for _, link := range links {
			for _, address := range link.Addresses {
				if address.Failed {
					t.Fatal("lab IPv6 duplicate address detection failed")
				}
				ready = ready && !address.Tentative
			}
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("lab IPv6 addresses remained tentative")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func privateResources(t *testing.T, pid int) privateProcessResources {
	t.Helper()
	base := productProcessResources(t, pid)
	result := privateProcessResources{FD: base.FD, Pipes: base.Pipes, Threads: base.Threads}
	root := filepath.Join("/proc", strconv.Itoa(pid))
	netlink, err := os.ReadFile(filepath.Join(root, "net", "netlink"))
	if err != nil {
		t.Fatal(err)
	}
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(netlink), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			inodes[fields[len(fields)-1]] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "fd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(root, "fd", entry.Name()))
		if err == nil && strings.HasPrefix(link, "socket:[") {
			result.Sockets++
			if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				result.Netlink++
			}
		}
	}
	status, err := os.ReadFile(filepath.Join(root, "status"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmRSS:" {
			result.RSSKiB, err = strconv.Atoi(fields[1])
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return result
}

func waitPrivateRegistrationAfter(t *testing.T, parent context.Context, path string, offset int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	for {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Seek(offset, 0)
		if err != nil {
			file.Close()
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		ready := false
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event["msg"] == "RENDEZVOUS_REGISTRATIONS" && event["active"] == float64(2) {
				ready = true
				break
			}
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("new hosting generation registration: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
