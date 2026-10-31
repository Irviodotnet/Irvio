// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
)

func TestPrivateRuntimePrepareRealReboot(t *testing.T) {
	if os.Getenv("IRVIO_PRIVATE_REAL_REBOOT") != "1" || os.Getenv("IRVIO_PRIVATE_HOST_DNS") != "1" {
		t.Skip("requires explicit disposable host reboot acceptance")
	}
	testPrivateRuntime(t, privateRuntimeCase{hostDNS: true, realReboot: true})
}

func holdPrivateForReboot(t *testing.T, ctx context.Context, lab *lab, api *localapi.Client, base, hostID string) {
	t.Helper()
	path := os.Getenv("IRVIO_PRIVATE_REBOOT_EVIDENCE")
	if filepath.Dir(path) != "/var/tmp" || !strings.HasPrefix(filepath.Base(path), "irvio-private-reboot-") {
		t.Fatal("reboot evidence requires a fresh /var/tmp/irvio-private-reboot-* directory")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	intent, present, err := (productintent.Store{Path: "/run/irvio/session-state"}).Read()
	if err != nil || !present || !intent.ConnectIntent || intent.PrivateTarget != hostID {
		t.Fatalf("missing actual-host private ConnectIntent: %v", err)
	}
	status, err := api.GetStatus(ctx)
	if err != nil || status.GetState().String() != "CONNECTION_STATE_CONNECTED" || status.GetPrivateTargetPeerId() != hostID {
		t.Fatal("reboot preparation lost connected target:", err)
	}
	if err := os.Mkdir(filepath.Join(path, "profile"), 0700); err != nil {
		t.Fatal(err)
	}
	// Preserve atomic identity/trust files, never copy a live SQLite database or API socket.
	for _, name := range []string{".hydra-profile-v1", "identity.key", "awg-identity", "private-access-v1.json", "bootstrap-v1.pb"} {
		source := filepath.Join(base, "private-client", name)
		info, err := os.Lstat(source)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("cannot preserve required profile file %s: %v", name, err)
		}
		lab.run("cp", "-p", source, filepath.Join(path, "profile", name))
	}
	proof := map[string]any{
		"boot_id": strings.TrimSpace(string(boot)), "prepared_at": time.Now().UTC(),
		"private_target": hostID, "state": status.GetState().String(),
		"profile_source": filepath.Join(base, "private-client"), "artifact_source": lab.artifactDir,
		"dns": privateHostDNSState(t, lab), "protection": lab.run("nft", "list", "table", "inet", "hydra_protect"),
		"intent_present": true, "real_http_verified": true,
	}
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "PRE_REBOOT.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	lab.run("sync", "-f", path)
	t.Logf("REAL_REBOOT_READY evidence=%s; awaiting operator reboot, this test does not report PASS", path)
	<-ctx.Done()
	t.Fatal("real reboot was not performed within preparation window")
}
