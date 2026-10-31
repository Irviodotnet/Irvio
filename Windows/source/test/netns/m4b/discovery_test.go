// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	irvioprotocol "github.com/Irviodotnet/Irvio/internal/protocol"

	_ "modernc.org/sqlite"
)

type irviodNodeInfo struct {
	PeerID      string   `json:"peer_id"`
	ListenAddrs []string `json:"listen_addrs"`
}

type irviodDiscovery struct {
	Country   string   `json:"country"`
	Providers []string `json:"providers"`
}

func TestM4B_DiscoveryAcrossNamespaces(t *testing.T) {
	requirePrivileged(t)
	for _, family := range []string{"ip4", "ip6"} {
		family := family
		t.Run(family, func(t *testing.T) {
			lab := newLab(t, "discovery-"+family)
			topology := lab.buildDiscoverySegment()
			addresses := map[string]netip.Addr{"a": topology.AIPv4, "b": topology.BIPv4, "c": topology.CIPv4}
			if family == "ip6" {
				addresses = map[string]netip.Addr{"a": topology.AIPv6, "b": topology.BIPv6, "c": topology.CIPv6}
			}
			irviod := os.Getenv("HYDRA_M4B_HYDRAD")
			runtimeDir := lab.tempDir("discovery-" + family)
			bDir := filepath.Join(runtimeDir, "data-b")
			cDir := filepath.Join(runtimeDir, "data-c")
			aDir := filepath.Join(runtimeDir, "data-a")
			for _, directory := range []string{aDir, bDir, cDir} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			b := lab.startChild("hydrad-b", topology.BNamespace, irviod,
				"--data-dir", bDir, "--listen-port", "42002", "--print-node-info")
			bInfo := waitNodeInfo(t, lab, b, "hydrad-b.stdout.log", 1)
			bootstrap := selectMultiaddr(t, bInfo, family, addresses["b"])
			c := lab.startChild("hydrad-c", topology.CNamespace, irviod,
				"--data-dir", cDir, "--listen-port", "42003", "--bootstrap", bootstrap,
				"--exit", "--country", "DE", "--print-node-info")
			cInfo := waitNodeInfo(t, lab, c, "hydrad-c.stdout.log", 1)
			_ = selectMultiaddr(t, cInfo, family, addresses["c"])

			a := lab.startChild("hydrad-a", topology.ANamespace, irviod,
				"--data-dir", aDir, "--listen-port", "42001", "--bootstrap", bootstrap,
				"--find-exits", "DE", "--find-timeout", "12s", "--print-node-info")
			aObjects := waitJSONObjects(t, lab, a, "hydrad-a.stdout.log", 2, 18*time.Second)
			var aInfo irviodNodeInfo
			if err := json.Unmarshal(aObjects[0], &aInfo); err != nil {
				t.Fatal(err)
			}
			var discovery irviodDiscovery
			if err := json.Unmarshal(aObjects[1], &discovery); err != nil {
				t.Fatal(err)
			}
			if discovery.Country != "DE" || len(discovery.Providers) != 1 || discovery.Providers[0] != cInfo.PeerID {
				t.Fatalf("unexpected discovery result: %+v; C=%s", discovery, cInfo.PeerID)
			}
			verifyStoredAnnouncement(t, filepath.Join(aDir, "hydra.db"), cInfo.PeerID)

			if err := b.stop(); err != nil {
				t.Fatalf("stop bootstrap B: %v", err)
			}
			cAddress := selectMultiaddr(t, cInfo, family, addresses["c"])
			if err := a.stop(); err != nil {
				t.Fatalf("stop A: %v", err)
			}
			restarted := lab.startChild("hydrad-a-restart", topology.ANamespace, irviod,
				"--data-dir", aDir, "--listen-port", "42001", "--bootstrap", cAddress,
				"--find-exits", "DE", "--find-timeout", "12s", "--print-node-info")
			restartedObjects := waitJSONObjects(t, lab, restarted, "hydrad-a-restart.stdout.log", 2, 18*time.Second)
			var restartedInfo irviodNodeInfo
			if err := json.Unmarshal(restartedObjects[0], &restartedInfo); err != nil {
				t.Fatal(err)
			}
			var directDiscovery irviodDiscovery
			if err := json.Unmarshal(restartedObjects[1], &directDiscovery); err != nil {
				t.Fatal(err)
			}
			if len(directDiscovery.Providers) != 1 || directDiscovery.Providers[0] != cInfo.PeerID {
				t.Fatalf("A did not fetch C directly after B removal: %+v", directDiscovery)
			}
			if restartedInfo.PeerID != aInfo.PeerID {
				t.Fatalf("A PeerID changed across restart: %s -> %s", aInfo.PeerID, restartedInfo.PeerID)
			}
			verifyStoredAnnouncement(t, filepath.Join(aDir, "hydra.db"), cInfo.PeerID)
			lab.captureNamespace(topology.ANamespace, "a", false)
			lab.captureNamespace(topology.BNamespace, "b", false)
			lab.captureNamespace(topology.CNamespace, "c", false)
		})
	}
}

func TestM4B_StaleProviderIsNotPersisted(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "stale-provider")
	topology := lab.buildDiscoverySegment()
	irviod := os.Getenv("HYDRA_M4B_HYDRAD")
	runtimeDir := lab.tempDir("stale-provider")
	bDir := filepath.Join(runtimeDir, "data-b")
	cDir := filepath.Join(runtimeDir, "data-c")
	aDir := filepath.Join(runtimeDir, "data-a")
	for _, directory := range []string{aDir, bDir, cDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	b := lab.startChild("stale-b", topology.BNamespace, irviod,
		"--data-dir", bDir, "--listen-port", "42102", "--print-node-info")
	bInfo := waitNodeInfo(t, lab, b, "stale-b.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bInfo, "ip4", topology.BIPv4)
	c := lab.startChild("stale-c", topology.CNamespace, irviod,
		"--data-dir", cDir, "--listen-port", "42103", "--bootstrap", bootstrap,
		"--exit", "--country", "DE", "--print-node-info")
	cInfo := waitNodeInfo(t, lab, c, "stale-c.stdout.log", 1)
	if err := c.stop(); err != nil {
		t.Fatal(err)
	}
	a := lab.startChild("stale-a", topology.ANamespace, irviod,
		"--data-dir", aDir, "--listen-port", "42101", "--bootstrap", bootstrap,
		"--find-exits", "DE", "--find-once", "--find-timeout", "3s", "--print-node-info")
	waitContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := a.wait(waitContext); err == nil {
		t.Fatal("stale provider discovery unexpectedly succeeded")
	}
	db, err := sql.Open("sqlite", filepath.Join(aDir, "hydra.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM peers WHERE node_id = ?", cInfo.PeerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale provider %s was persisted without a fresh verified announcement", cInfo.PeerID)
	}
	if !structuredLogContains(filepath.Join(lab.artifactDir, "stale-a.stderr.log"), "NODE_ANNOUNCEMENT_REJECTED", cInfo.PeerID) {
		t.Fatal("machine-readable log did not show rejection of the stale provider hint")
	}
}

func waitNodeInfo(t *testing.T, lab *lab, child *childProcess, file string, count int) irviodNodeInfo {
	t.Helper()
	objects := waitJSONObjects(t, lab, child, file, count, 12*time.Second)
	var info irviodNodeInfo
	if err := json.Unmarshal(objects[0], &info); err != nil {
		t.Fatal(err)
	}
	if info.PeerID == "" || len(info.ListenAddrs) == 0 {
		t.Fatalf("invalid hydrad node info: %+v", info)
	}
	return info
}

func waitJSONObjects(t *testing.T, lab *lab, child *childProcess, file string, count int, maximum time.Duration) []json.RawMessage {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		objects := make([]json.RawMessage, 0, count)
		for len(objects) < count {
			var object json.RawMessage
			if err := decoder.Decode(&object); err != nil {
				break
			}
			objects = append(objects, object)
		}
		if len(objects) >= count {
			return objects
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d JSON objects in %s: %v", count, path, ctx.Err())
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("hydrad exited before readiness: %v", err)
		case <-ticker.C:
		}
	}
}

func selectMultiaddr(t *testing.T, info irviodNodeInfo, family string, address netip.Addr) string {
	t.Helper()
	prefix := "/" + family + "/" + address.String() + "/tcp/"
	for _, candidate := range info.ListenAddrs {
		if strings.HasPrefix(candidate, prefix) {
			return candidate
		}
	}
	t.Fatalf("node %s has no %s listen address for %s: %v", info.PeerID, family, address, info.ListenAddrs)
	return ""
}

func verifyStoredAnnouncement(t *testing.T, databasePath, expectedPeerID string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var envelope []byte
	if err := db.QueryRow("SELECT signed_envelope FROM announcements WHERE node_id = ?", expectedPeerID).Scan(&envelope); err != nil {
		t.Fatalf("read verified announcement for %s: %v", expectedPeerID, err)
	}
	provider, err := peer.Decode(expectedPeerID)
	if err != nil {
		t.Fatal(err)
	}
	announcement, err := irvioprotocol.VerifySignedAnnouncement(envelope, provider, time.Now())
	if err != nil {
		t.Fatalf("verify stored signed announcement: %v", err)
	}
	if string(announcement.NodeId) != string(provider) {
		t.Fatalf("provider/signer/NodeID invariant failed for %s", provider)
	}
}

func structuredLogContains(path, event, peerID string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			return false
		}
		if object["msg"] == event && object["remote_peer_id"] == peerID {
			return true
		}
	}
}
