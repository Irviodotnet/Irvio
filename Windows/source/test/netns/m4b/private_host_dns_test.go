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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
)

func TestPrivateRuntimeActualHostDNS(t *testing.T) {
	if os.Getenv("IRVIO_PRIVATE_HOST_DNS") != "1" {
		t.Skip("requires explicit disposable host namespace DNS acceptance")
	}
	testPrivateRuntime(t, privateRuntimeCase{hostDNS: true})
}

func attachPrivateDNSHost(t *testing.T, lab *lab, namespaces []string, ips []netip.Addr) {
	t.Helper()
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Readlink("/proc/1/ns/net")
	if err != nil || self != actual {
		t.Fatal("private host DNS test must run in PID1 network namespace")
	}
	if strings.TrimSpace(lab.run("systemctl", "show", "irviod.service", "--property=MainPID", "--value")) != "0" {
		t.Fatal("refuse to replace an active installed daemon")
	}
	for _, path := range []string{"/run/irvio/session-state", "/sys/class/net/hydra0"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("host already has session/protection state: %s err=%v", path, err)
		}
	}
	if rules := lab.run("nft", "list", "tables"); strings.Contains(rules, "hydra_") || strings.Contains(rules, "private_oracle") {
		t.Fatal("host has existing IRVIO/lab firewall state")
	}
	baseline := privateHostDNSState(t, lab)
	socketActive := strings.TrimSpace(lab.run("systemctl", "show", "irvio.socket", "--property=ActiveState", "--value")) == "active"
	if socketActive {
		lab.run("systemctl", "stop", "irvio.socket")
	}
	t.Cleanup(func() {
		// Stop only this lab's children before restoring host socket activation.
		if err := lab.Close(); err != nil {
			t.Errorf("host DNS lab cleanup: %v", err)
			return
		}
		if _, err := os.Lstat("/run/irvio/session-state"); !os.IsNotExist(err) {
			t.Error("runtime intent retained; socket activation left stopped")
			return
		}
		if rules := lab.run("nft", "list", "tables"); strings.Contains(rules, "hydra_") || strings.Contains(rules, "private_oracle") {
			t.Error("host firewall residue retained; socket activation left stopped")
			return
		}
		for attempt := 0; attempt < 20; attempt++ {
			if reflect.DeepEqual(baseline, privateHostDNSState(t, lab)) {
				if socketActive {
					lab.run("systemctl", "start", "irvio.socket")
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Error("actual host DNS did not return to original baseline; socket left stopped")
	})
	iface := lab.interfaceName('a' + 6)
	alias := lab.name + "-host-dns"
	lab.run("ip", "netns", "attach", alias, "1")
	lab.namespaces = append(lab.namespaces, alias)
	lab.inNamespace(namespaces[6], "ip", "link", "set", iface, "netns", "1")
	lab.run("ip", "link", "set", iface, "up")
	lab.run("ip", "addr", "add", ips[6].String()+"/24", "dev", iface)
	// The real host's test transport uses this disposable underlay; deleting its veth restores the original defaults.
	lab.run("ip", "route", "add", "default", "via", "198.18.57.254", "dev", iface, "src", ips[6].String(), "metric", "1")
	for index, ip := range ips {
		if index != 6 {
			lab.run("ip", "route", "add", ip.String()+"/32", "via", "198.18.57.254", "dev", iface, "src", ips[6].String())
		}
	}
	namespaces[6] = alias
	router := lab.name + "-router"
	lab.inNamespace(router, "nft", "add", "table", "ip", "private_dns_fixture")
	lab.inNamespace(router, "nft", "add", "chain", "ip", "private_dns_fixture", "prerouting", "{ type nat hook prerouting priority dstnat; policy accept; }")
	for _, protocol := range []string{"udp", "tcp"} {
		lab.inNamespace(router, "nft", "add", "rule", "ip", "private_dns_fixture", "prerouting",
			"ip", "daddr", "1.1.1.1", protocol, "dport", "53", "dnat", "to", ips[7].String())
	}
}

func privateHostDNSState(t *testing.T, lab *lab) map[string]string {
	t.Helper()
	state := make(map[string]string)
	for _, operation := range []string{"dns", "domain", "default-route"} {
		state[operation] = lab.run("env", "LC_ALL=C", "SYSTEMD_COLORS=0", "resolvectl", "--no-pager", operation)
	}
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	state["resolv.conf"] = string(data)
	info, err := os.Lstat("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	state["mode"] = info.Mode().String()
	if info.Mode()&os.ModeSymlink != 0 {
		state["link"], err = os.Readlink("/etc/resolv.conf")
		if err != nil {
			t.Fatal(err)
		}
	}
	return state
}

func checkPrivateHostDNS(t *testing.T, lab *lab, server netip.Addr) {
	t.Helper()
	for operation, suffix := range map[string]string{"dns": ": " + server.String(), "domain": ": ~.", "default-route": ": yes"} {
		value := lab.run("env", "LC_ALL=C", "SYSTEMD_COLORS=0", "resolvectl", "--no-pager", operation, "hydra0")
		if !strings.HasSuffix(strings.TrimSpace(value), suffix) {
			t.Fatalf("real tunnel DNS %s mismatch: %s", operation, value)
		}
	}
	value := lab.run("env", "LC_ALL=C", "SYSTEMD_COLORS=0", "resolvectl", "--no-pager", "--cache=no", "--type=A", "query", fmt.Sprintf("%s.private.test", lab.name))
	if !strings.Contains(value, "198.51.100.77") || !strings.Contains(value, "hydra0") {
		t.Fatalf("system resolver did not use private tunnel DNS: %s", value)
	}
}

func registerPrivateHostDNSCleanup(t *testing.T, lab *lab, client *localapi.Client, socket string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Shutdown(ctx); err != nil {
			fresh, dialErr := localapi.Dial(ctx, socket)
			if dialErr != nil {
				t.Errorf("host DNS cleanup cannot reach daemon: %v / %v", err, dialErr)
				return
			}
			defer fresh.Close()
			if err := fresh.Shutdown(ctx); err != nil {
				t.Errorf("host DNS cleanup Shutdown: %v", err)
			}
		}
		lab.inNamespace(lab.name+"-host-dns", "nft", "delete", "table", "inet", "private_oracle")
		if _, err := os.Stat(filepath.Join("/run/irvio", "session-state")); !os.IsNotExist(err) {
			t.Errorf("host DNS cleanup retained session-state: %v", err)
		}
	})
}
