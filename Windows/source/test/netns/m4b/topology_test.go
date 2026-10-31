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
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	stunPrimaryPort   = 3478
	stunAlternatePort = 3479
	clientMuxPort     = 41000
	externalPortOne   = 51001
	externalPortTwo   = 51002
	externalPortThree = 51003
)

type directTopology struct {
	ClientNamespace string     `json:"client_namespace"`
	ServerNamespace string     `json:"server_namespace"`
	ClientInterface string     `json:"client_interface"`
	ClientIPv4      netip.Addr `json:"client_ipv4"`
	ServerIPv4A1    netip.Addr `json:"server_ipv4_a1"`
	ServerIPv4A2    netip.Addr `json:"server_ipv4_a2"`
	ClientIPv6      netip.Addr `json:"client_ipv6"`
	ServerIPv6A1    netip.Addr `json:"server_ipv6_a1"`
	ServerIPv6A2    netip.Addr `json:"server_ipv6_a2"`
}

type discoveryTopology struct {
	ANamespace      string     `json:"a_namespace"`
	BNamespace      string     `json:"b_namespace"`
	CNamespace      string     `json:"c_namespace"`
	SwitchNamespace string     `json:"switch_namespace"`
	AIPv4           netip.Addr `json:"a_ipv4"`
	BIPv4           netip.Addr `json:"b_ipv4"`
	CIPv4           netip.Addr `json:"c_ipv4"`
	AIPv6           netip.Addr `json:"a_ipv6"`
	BIPv6           netip.Addr `json:"b_ipv6"`
	CIPv6           netip.Addr `json:"c_ipv6"`
}

func (l *lab) buildDiscoverySegment() discoveryTopology {
	l.t.Helper()
	topology := discoveryTopology{
		ANamespace: l.namespace("a"), BNamespace: l.namespace("b"),
		CNamespace: l.namespace("c"), SwitchNamespace: l.namespace("switch"),
		AIPv4: netip.MustParseAddr("198.18.50.1"), BIPv4: netip.MustParseAddr("198.18.50.2"),
		CIPv4: netip.MustParseAddr("198.18.50.3"), AIPv6: netip.MustParseAddr("2001:db8:50::1"),
		BIPv6: netip.MustParseAddr("2001:db8:50::2"), CIPv6: netip.MustParseAddr("2001:db8:50::3"),
	}
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", "lo", "up")
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "add", "br0", "type", "bridge")
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", "br0", "up")
	nodes := []struct {
		namespace string
		ipv4      netip.Addr
		ipv6      netip.Addr
		role      byte
		peerRole  byte
	}{
		{topology.ANamespace, topology.AIPv4, topology.AIPv6, 'a', '1'},
		{topology.BNamespace, topology.BIPv4, topology.BIPv6, 'b', '2'},
		{topology.CNamespace, topology.CIPv4, topology.CIPv6, 'c', '3'},
	}
	for _, node := range nodes {
		nodeInterface := l.interfaceName(node.role)
		switchInterface := l.interfaceName(node.peerRole)
		l.createVeth(node.namespace, nodeInterface, topology.SwitchNamespace, switchInterface)
		l.inNamespace(node.namespace, "ip", "link", "set", "lo", "up")
		l.inNamespace(node.namespace, "ip", "link", "set", nodeInterface, "up")
		l.inNamespace(node.namespace, "ip", "addr", "add", node.ipv4.String()+"/24", "dev", nodeInterface)
		l.inNamespace(node.namespace, "ip", "-6", "addr", "add", node.ipv6.String()+"/64", "dev", nodeInterface, "nodad")
		l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", switchInterface, "master", "br0")
		l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", switchInterface, "up")
	}
	l.recordTopology(topology)
	return topology
}

func (l *lab) buildDirectDualStack() directTopology {
	l.t.Helper()
	clientNamespace := l.namespace("client")
	serverNamespace := l.namespace("server")
	clientInterface := l.interfaceName('c')
	serverInterface := l.interfaceName('s')
	l.createVeth(clientNamespace, clientInterface, serverNamespace, serverInterface)
	l.inNamespace(clientNamespace, "ip", "link", "set", "lo", "up")
	l.inNamespace(serverNamespace, "ip", "link", "set", "lo", "up")
	l.inNamespace(clientNamespace, "ip", "link", "set", clientInterface, "up")
	l.inNamespace(serverNamespace, "ip", "link", "set", serverInterface, "up")

	topology := directTopology{
		ClientNamespace: clientNamespace, ServerNamespace: serverNamespace, ClientInterface: clientInterface,
		ClientIPv4:   netip.MustParseAddr("198.18.10.1"),
		ServerIPv4A1: netip.MustParseAddr("198.18.10.2"),
		ServerIPv4A2: netip.MustParseAddr("198.18.10.3"),
		ClientIPv6:   netip.MustParseAddr("2001:db8:10::1"),
		ServerIPv6A1: netip.MustParseAddr("2001:db8:10::2"),
		ServerIPv6A2: netip.MustParseAddr("2001:db8:10::3"),
	}
	l.inNamespace(clientNamespace, "ip", "addr", "add", topology.ClientIPv4.String()+"/24", "dev", clientInterface)
	l.inNamespace(serverNamespace, "ip", "addr", "add", topology.ServerIPv4A1.String()+"/24", "dev", serverInterface)
	l.inNamespace(serverNamespace, "ip", "addr", "add", topology.ServerIPv4A2.String()+"/24", "dev", serverInterface)
	l.inNamespace(clientNamespace, "ip", "-6", "addr", "add", topology.ClientIPv6.String()+"/64", "dev", clientInterface, "nodad")
	l.inNamespace(serverNamespace, "ip", "-6", "addr", "add", topology.ServerIPv6A1.String()+"/64", "dev", serverInterface, "nodad")
	l.inNamespace(serverNamespace, "ip", "-6", "addr", "add", topology.ServerIPv6A2.String()+"/64", "dev", serverInterface, "nodad")
	l.recordTopology(topology)
	return topology
}

type nat44Topology struct {
	ClientNamespace  string     `json:"client_namespace"`
	RouterNamespace  string     `json:"router_namespace"`
	ServerNamespace  string     `json:"server_namespace"`
	ClientIPv4       netip.Addr `json:"client_ipv4"`
	RouterInside     netip.Addr `json:"router_inside"`
	RouterOutside    netip.Addr `json:"router_outside"`
	ServerA1         netip.Addr `json:"server_a1"`
	ServerA2         netip.Addr `json:"server_a2"`
	InsideInterface  string     `json:"inside_interface"`
	OutsideInterface string     `json:"outside_interface"`
}

type doubleNAT44Topology struct {
	ClientNamespace  string     `json:"client_namespace"`
	RouterOne        string     `json:"router_one_namespace"`
	RouterTwo        string     `json:"router_two_namespace"`
	ServerNamespace  string     `json:"server_namespace"`
	ClientIPv4       netip.Addr `json:"client_ipv4"`
	RouterOneInside  netip.Addr `json:"router_one_inside"`
	RouterOneMiddle  netip.Addr `json:"router_one_middle"`
	RouterTwoMiddle  netip.Addr `json:"router_two_middle"`
	RouterTwoOutside netip.Addr `json:"router_two_outside"`
	ServerA1         netip.Addr `json:"server_a1"`
	ServerA2         netip.Addr `json:"server_a2"`
	R1InsideIf       string     `json:"router_one_inside_interface"`
	R1OutsideIf      string     `json:"router_one_outside_interface"`
	R2InsideIf       string     `json:"router_two_inside_interface"`
	R2OutsideIf      string     `json:"router_two_outside_interface"`
}

func (l *lab) buildDoubleNAT44() doubleNAT44Topology {
	l.t.Helper()
	topology := doubleNAT44Topology{
		ClientNamespace: l.namespace("client"), RouterOne: l.namespace("router1"),
		RouterTwo: l.namespace("router2"), ServerNamespace: l.namespace("server"),
		ClientIPv4: netip.MustParseAddr("10.242.10.2"), RouterOneInside: netip.MustParseAddr("10.242.10.1"),
		RouterOneMiddle: netip.MustParseAddr("100.64.10.1"), RouterTwoMiddle: netip.MustParseAddr("100.64.10.2"),
		RouterTwoOutside: netip.MustParseAddr("198.20.10.1"), ServerA1: netip.MustParseAddr("198.20.10.2"),
		ServerA2: netip.MustParseAddr("198.20.10.3"), R1InsideIf: l.interfaceName('1'),
		R1OutsideIf: l.interfaceName('2'), R2InsideIf: l.interfaceName('3'), R2OutsideIf: l.interfaceName('4'),
	}
	clientInterface := l.interfaceName('c')
	serverInterface := l.interfaceName('s')
	l.createVeth(topology.ClientNamespace, clientInterface, topology.RouterOne, topology.R1InsideIf)
	l.createVeth(topology.RouterOne, topology.R1OutsideIf, topology.RouterTwo, topology.R2InsideIf)
	l.createVeth(topology.RouterTwo, topology.R2OutsideIf, topology.ServerNamespace, serverInterface)
	for _, namespace := range []string{topology.ClientNamespace, topology.RouterOne, topology.RouterTwo, topology.ServerNamespace} {
		l.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for namespace, name := range map[string]string{
		topology.ClientNamespace: clientInterface, topology.RouterOne: topology.R1InsideIf,
		topology.RouterTwo: topology.R2InsideIf, topology.ServerNamespace: serverInterface,
	} {
		l.inNamespace(namespace, "ip", "link", "set", name, "up")
	}
	l.inNamespace(topology.RouterOne, "ip", "link", "set", topology.R1OutsideIf, "up")
	l.inNamespace(topology.RouterTwo, "ip", "link", "set", topology.R2OutsideIf, "up")
	l.inNamespace(topology.ClientNamespace, "ip", "addr", "add", topology.ClientIPv4.String()+"/24", "dev", clientInterface)
	l.inNamespace(topology.RouterOne, "ip", "addr", "add", topology.RouterOneInside.String()+"/24", "dev", topology.R1InsideIf)
	l.inNamespace(topology.RouterOne, "ip", "addr", "add", topology.RouterOneMiddle.String()+"/24", "dev", topology.R1OutsideIf)
	l.inNamespace(topology.RouterTwo, "ip", "addr", "add", topology.RouterTwoMiddle.String()+"/24", "dev", topology.R2InsideIf)
	l.inNamespace(topology.RouterTwo, "ip", "addr", "add", topology.RouterTwoOutside.String()+"/24", "dev", topology.R2OutsideIf)
	l.inNamespace(topology.ServerNamespace, "ip", "addr", "add", topology.ServerA1.String()+"/24", "dev", serverInterface)
	l.inNamespace(topology.ServerNamespace, "ip", "addr", "add", topology.ServerA2.String()+"/24", "dev", serverInterface)
	l.inNamespace(topology.ClientNamespace, "ip", "route", "add", "default", "via", topology.RouterOneInside.String())
	l.inNamespace(topology.RouterOne, "ip", "route", "add", "default", "via", topology.RouterTwoMiddle.String())
	for _, router := range []string{topology.RouterOne, topology.RouterTwo} {
		l.inNamespace(router, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
		l.inNamespace(router, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=0")
	}
	l.recordTopology(topology)
	return topology
}

func (l *lab) buildNAT44() nat44Topology {
	l.t.Helper()
	clientNamespace := l.namespace("client")
	routerNamespace := l.namespace("router")
	serverNamespace := l.namespace("server")
	clientInterface := l.interfaceName('c')
	routerInsideInterface := l.interfaceName('i')
	routerOutsideInterface := l.interfaceName('o')
	serverInterface := l.interfaceName('s')
	l.createVeth(clientNamespace, clientInterface, routerNamespace, routerInsideInterface)
	l.createVeth(routerNamespace, routerOutsideInterface, serverNamespace, serverInterface)
	for _, namespace := range []string{clientNamespace, routerNamespace, serverNamespace} {
		l.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for namespace, name := range map[string]string{
		clientNamespace: clientInterface, routerNamespace: routerInsideInterface,
		serverNamespace: serverInterface,
	} {
		l.inNamespace(namespace, "ip", "link", "set", name, "up")
	}
	l.inNamespace(routerNamespace, "ip", "link", "set", routerOutsideInterface, "up")

	topology := nat44Topology{
		ClientNamespace: clientNamespace, RouterNamespace: routerNamespace, ServerNamespace: serverNamespace,
		ClientIPv4: netip.MustParseAddr("10.241.10.2"), RouterInside: netip.MustParseAddr("10.241.10.1"),
		RouterOutside: netip.MustParseAddr("198.19.10.1"), ServerA1: netip.MustParseAddr("198.19.10.2"),
		ServerA2: netip.MustParseAddr("198.19.10.3"), InsideInterface: routerInsideInterface,
		OutsideInterface: routerOutsideInterface,
	}
	l.inNamespace(clientNamespace, "ip", "addr", "add", topology.ClientIPv4.String()+"/24", "dev", clientInterface)
	l.inNamespace(routerNamespace, "ip", "addr", "add", topology.RouterInside.String()+"/24", "dev", routerInsideInterface)
	l.inNamespace(routerNamespace, "ip", "addr", "add", topology.RouterOutside.String()+"/24", "dev", routerOutsideInterface)
	l.inNamespace(serverNamespace, "ip", "addr", "add", topology.ServerA1.String()+"/24", "dev", serverInterface)
	l.inNamespace(serverNamespace, "ip", "addr", "add", topology.ServerA2.String()+"/24", "dev", serverInterface)
	l.inNamespace(clientNamespace, "ip", "route", "add", "default", "via", topology.RouterInside.String())
	l.inNamespace(routerNamespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	l.inNamespace(routerNamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=0")
	l.inNamespace(clientNamespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=0")
	l.recordTopology(topology)
	return topology
}

type routedIPv6Topology struct {
	ClientNamespace  string     `json:"client_namespace"`
	RouterNamespace  string     `json:"router_namespace"`
	ServerNamespace  string     `json:"server_namespace"`
	ClientIPv6       netip.Addr `json:"client_ipv6"`
	RouterInside     netip.Addr `json:"router_inside"`
	RouterOutside    netip.Addr `json:"router_outside"`
	ServerA1         netip.Addr `json:"server_a1"`
	ServerA2         netip.Addr `json:"server_a2"`
	ClientInterface  string     `json:"client_interface"`
	InsideInterface  string     `json:"inside_interface"`
	OutsideInterface string     `json:"outside_interface"`
	ServerInterface  string     `json:"server_interface"`
}

func (l *lab) buildRoutedIPv6() routedIPv6Topology {
	l.t.Helper()
	clientNamespace := l.namespace("client")
	routerNamespace := l.namespace("router")
	serverNamespace := l.namespace("server")
	clientInterface := l.interfaceName('c')
	routerInsideInterface := l.interfaceName('i')
	routerOutsideInterface := l.interfaceName('o')
	serverInterface := l.interfaceName('s')
	l.createVeth(clientNamespace, clientInterface, routerNamespace, routerInsideInterface)
	l.createVeth(routerNamespace, routerOutsideInterface, serverNamespace, serverInterface)
	for _, namespace := range []string{clientNamespace, routerNamespace, serverNamespace} {
		l.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	l.inNamespace(clientNamespace, "ip", "link", "set", clientInterface, "up")
	l.inNamespace(routerNamespace, "ip", "link", "set", routerInsideInterface, "up")
	l.inNamespace(routerNamespace, "ip", "link", "set", routerOutsideInterface, "up")
	l.inNamespace(serverNamespace, "ip", "link", "set", serverInterface, "up")

	topology := routedIPv6Topology{
		ClientNamespace: clientNamespace, RouterNamespace: routerNamespace, ServerNamespace: serverNamespace,
		ClientIPv6: netip.MustParseAddr("2001:db8:41::2"), RouterInside: netip.MustParseAddr("2001:db8:41::1"),
		RouterOutside: netip.MustParseAddr("2001:db8:42::1"), ServerA1: netip.MustParseAddr("2001:db8:42::2"),
		ServerA2: netip.MustParseAddr("2001:db8:42::3"), ClientInterface: clientInterface,
		InsideInterface: routerInsideInterface, OutsideInterface: routerOutsideInterface, ServerInterface: serverInterface,
	}
	l.inNamespace(clientNamespace, "ip", "-6", "addr", "add", topology.ClientIPv6.String()+"/64", "dev", clientInterface, "nodad")
	l.inNamespace(routerNamespace, "ip", "-6", "addr", "add", topology.RouterInside.String()+"/64", "dev", routerInsideInterface, "nodad")
	l.inNamespace(routerNamespace, "ip", "-6", "addr", "add", topology.RouterOutside.String()+"/64", "dev", routerOutsideInterface, "nodad")
	l.inNamespace(serverNamespace, "ip", "-6", "addr", "add", topology.ServerA1.String()+"/64", "dev", serverInterface, "nodad")
	l.inNamespace(serverNamespace, "ip", "-6", "addr", "add", topology.ServerA2.String()+"/64", "dev", serverInterface, "nodad")
	l.inNamespace(clientNamespace, "ip", "-6", "route", "add", "default", "via", topology.RouterInside.String())
	l.inNamespace(serverNamespace, "ip", "-6", "route", "add", "2001:db8:41::/64", "via", topology.RouterOutside.String())
	l.inNamespace(routerNamespace, "sysctl", "-q", "-w", "net.ipv6.conf.all.forwarding=1")
	l.installStaticIPv6Neighbors(topology)
	l.recordTopology(topology)
	return topology
}

func (l *lab) installStaticIPv6Neighbors(topology routedIPv6Topology) {
	l.t.Helper()
	clientMAC := l.interfaceMAC(topology.ClientNamespace, topology.ClientInterface)
	routerInsideMAC := l.interfaceMAC(topology.RouterNamespace, topology.InsideInterface)
	routerOutsideMAC := l.interfaceMAC(topology.RouterNamespace, topology.OutsideInterface)
	serverMAC := l.interfaceMAC(topology.ServerNamespace, topology.ServerInterface)

	l.inNamespace(topology.ClientNamespace, "ip", "-6", "neigh", "replace", topology.RouterInside.String(), "lladdr", routerInsideMAC, "nud", "permanent", "dev", topology.ClientInterface)
	l.inNamespace(topology.RouterNamespace, "ip", "-6", "neigh", "replace", topology.ClientIPv6.String(), "lladdr", clientMAC, "nud", "permanent", "dev", topology.InsideInterface)
	l.inNamespace(topology.RouterNamespace, "ip", "-6", "neigh", "replace", topology.ServerA1.String(), "lladdr", serverMAC, "nud", "permanent", "dev", topology.OutsideInterface)
	l.inNamespace(topology.RouterNamespace, "ip", "-6", "neigh", "replace", topology.ServerA2.String(), "lladdr", serverMAC, "nud", "permanent", "dev", topology.OutsideInterface)
	l.inNamespace(topology.ServerNamespace, "ip", "-6", "neigh", "replace", topology.RouterOutside.String(), "lladdr", routerOutsideMAC, "nud", "permanent", "dev", topology.ServerInterface)
}

func (l *lab) interfaceMAC(namespace, interfaceName string) string {
	l.t.Helper()
	var links []struct {
		Address string `json:"address"`
	}
	output := l.inNamespace(namespace, "ip", "-j", "link", "show", "dev", interfaceName)
	if err := json.Unmarshal([]byte(output), &links); err != nil {
		l.t.Fatalf("decode interface %s MAC address: %v", interfaceName, err)
	}
	if len(links) != 1 || links[0].Address == "" {
		l.t.Fatalf("interface %s returned invalid MAC address data: %q", interfaceName, output)
	}
	return links[0].Address
}

func (l *lab) recordTopology(value any) {
	l.t.Helper()
	path := filepath.Join(l.artifactDir, "topology.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		l.t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		l.t.Fatalf("write topology: %v %v", encodeErr, closeErr)
	}
}

func (l *lab) installNFT(namespace, name, rules string) {
	l.t.Helper()
	path := filepath.Join(l.artifactDir, name+".nft")
	if err := os.WriteFile(path, []byte(rules), 0o600); err != nil {
		l.t.Fatal(err)
	}
	l.inNamespace(namespace, "nft", "-f", path)
	output := l.inNamespace(namespace, "nft", "-j", "list", "ruleset")
	if err := os.WriteFile(filepath.Join(l.artifactDir, name+"-ruleset.json"), []byte(output), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

type mappingPolicy uint8

const (
	mappingEIM mappingPolicy = iota + 1
	mappingADM
	mappingAPDM
)

type filteringPolicy uint8

const (
	filteringEIF filteringPolicy = iota + 1
	filteringADF
	filteringAPDF
)

func controlledNATRules(topology nat44Topology, mapping mappingPolicy, filtering filteringPolicy, muxPort uint16) string {
	e1, e2, e3 := externalPortOne, externalPortOne, externalPortOne
	if mapping == mappingADM || mapping == mappingAPDM {
		e2 = externalPortTwo
		e3 = externalPortTwo
	}
	if mapping == mappingAPDM {
		e3 = externalPortThree
	}
	portSet := uniquePortSet(e1, e2, e3)
	a2Verdict := "accept"
	a1p2Verdict := "accept"
	if filtering == filteringADF || filtering == filteringAPDF {
		a2Verdict = "drop"
	}
	if filtering == filteringAPDF {
		a1p2Verdict = "drop"
	}
	return fmt.Sprintf(`table ip hydra_m4b_raw {
 chain prerouting {
  type filter hook prerouting priority raw; policy accept;
  ip daddr %s udp dport %d ip saddr { %s, %s } udp sport %d notrack
  ip daddr %s udp dport %d ip saddr { %s, %s } udp sport %d counter ip daddr set %s udp dport set %d comment "alternate-stateless-forward"
 }
}
table ip hydra_m4b {
 chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
  ip daddr %s ip saddr { %s, %s } udp sport { %d, %d } udp dport %s counter dnat to %s:%d comment "dnat-test"
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s udp sport %d ip daddr %s udp dport %d counter snat to %s:%d comment "snat-a1p1"
  ip saddr %s udp sport %d ip daddr %s udp dport %d counter snat to %s:%d comment "snat-a2p1"
  ip saddr %s udp sport %d ip daddr %s udp dport %d counter snat to %s:%d comment "snat-a2p2"
 }
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip saddr %s ip daddr { %s, %s } udp sport != %d udp dport { %d, %d } counter drop comment "internal-bad-port"
  iifname "%s" ip saddr %s ip daddr { %s, %s } udp sport %d udp dport { %d, %d } counter accept comment "internal-mux-port"
  ct state established,related counter accept comment "established"
  iifname "%s" ip saddr %s udp sport %d ip daddr %s udp dport %d counter %s comment "filter-a2p2-%s"
  iifname "%s" ip saddr %s udp sport %d ip daddr %s udp dport %d counter %s comment "filter-a1p2-%s"
  iifname "%s" ip saddr %s udp sport %d ip daddr %s udp dport %d counter accept comment "filter-a1p1"
 }
}
`, topology.RouterOutside, e1, topology.ServerA1, topology.ServerA2, stunAlternatePort,
		topology.RouterOutside, e1, topology.ServerA1, topology.ServerA2, stunAlternatePort, topology.ClientIPv4, muxPort,
		topology.RouterOutside, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		portSet, topology.ClientIPv4, muxPort,
		topology.ClientIPv4, muxPort, topology.ServerA1, stunPrimaryPort, topology.RouterOutside, e1,
		topology.ClientIPv4, muxPort, topology.ServerA2, stunPrimaryPort, topology.RouterOutside, e2,
		topology.ClientIPv4, muxPort, topology.ServerA2, stunAlternatePort, topology.RouterOutside, e3,
		topology.InsideInterface, topology.ClientIPv4, topology.ServerA1, topology.ServerA2, muxPort, stunPrimaryPort, stunAlternatePort,
		topology.InsideInterface, topology.ClientIPv4, topology.ServerA1, topology.ServerA2, muxPort, stunPrimaryPort, stunAlternatePort,
		topology.OutsideInterface, topology.ServerA2, stunAlternatePort, topology.ClientIPv4, muxPort, a2Verdict, a2Verdict,
		topology.OutsideInterface, topology.ServerA1, stunAlternatePort, topology.ClientIPv4, muxPort, a1p2Verdict, a1p2Verdict,
		topology.OutsideInterface, topology.ServerA1, stunPrimaryPort, topology.ClientIPv4, muxPort)
}

func uniquePortSet(ports ...int) string {
	seen := make(map[int]bool, len(ports))
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		if !seen[port] {
			seen[port] = true
			values = append(values, fmt.Sprintf("%d", port))
		}
	}
	if len(values) == 1 {
		return values[0]
	}
	return "{ " + strings.Join(values, ", ") + " }"
}

func doubleNATRules(topology doubleNAT44Topology, muxPort uint16) (string, string) {
	const middlePort = 50001
	routerOne := fmt.Sprintf(`table ip hydra_m4b {
 chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
  ip daddr %s ip saddr { %s, %s } udp sport { %d, %d } udp dport %d counter dnat to %s:%d comment "r1-dnat"
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s udp sport %d ip daddr { %s, %s } udp dport { %d, %d } counter snat to %s:%d comment "r1-snat"
 }
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip saddr %s udp sport != %d counter drop comment "internal-bad-port"
  iifname "%s" ip saddr %s udp sport %d counter accept comment "internal-mux-port"
  ct state established,related accept
  iifname "%s" ip daddr %s udp dport %d counter accept comment "r1-alternate-forward"
 }
}
`, topology.RouterOneMiddle, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		middlePort, topology.ClientIPv4, muxPort,
		topology.ClientIPv4, muxPort, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		topology.RouterOneMiddle, middlePort,
		topology.R1InsideIf, topology.ClientIPv4, muxPort,
		topology.R1InsideIf, topology.ClientIPv4, muxPort,
		topology.R1OutsideIf, topology.ClientIPv4, muxPort)
	routerTwo := fmt.Sprintf(`table ip hydra_m4b {
 chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
  ip daddr %s ip saddr { %s, %s } udp sport { %d, %d } udp dport %d counter dnat to %s:%d comment "r2-dnat"
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s udp sport %d ip daddr { %s, %s } udp dport { %d, %d } counter snat to %s:%d comment "r2-snat"
 }
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip saddr %s udp sport %d counter accept comment "middle-port"
  ct state established,related accept
  iifname "%s" ip daddr %s udp dport %d counter accept comment "r2-alternate-forward"
 }
}
`, topology.RouterTwoOutside, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		externalPortOne, topology.RouterOneMiddle, middlePort,
		topology.RouterOneMiddle, middlePort, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		topology.RouterTwoOutside, externalPortOne,
		topology.R2InsideIf, topology.RouterOneMiddle, middlePort,
		topology.R2OutsideIf, topology.RouterOneMiddle, middlePort)
	return routerOne, routerTwo
}

func nativeNATRules(topology nat44Topology, muxPort uint16) string {
	return fmt.Sprintf(`table ip hydra_m4b {
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s udp sport %d ip daddr { %s, %s } udp dport { %d, %d } counter masquerade comment "native-masquerade"
 }
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip saddr %s udp sport != %d counter drop comment "internal-bad-port"
  iifname "%s" ip saddr %s udp sport %d counter accept comment "internal-mux-port"
  ct state established,related counter accept comment "established"
 }
}
`, topology.ClientIPv4, muxPort, topology.ServerA1, topology.ServerA2, stunPrimaryPort, stunAlternatePort,
		topology.InsideInterface, topology.ClientIPv4, muxPort,
		topology.InsideInterface, topology.ClientIPv4, muxPort)
}

func ipv6FilterRules(topology routedIPv6Topology, filtering filteringPolicy, muxPort uint16) string {
	a2Verdict := "accept"
	a1p2Verdict := "accept"
	if filtering == filteringADF || filtering == filteringAPDF {
		a2Verdict = "drop"
	}
	if filtering == filteringAPDF {
		a1p2Verdict = "drop"
	}
	return fmt.Sprintf(`table ip6 hydra_m4b {
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip6 saddr %s ip6 daddr { %s, %s } udp sport != %d counter drop comment "internal-bad-port"
  iifname "%s" ip6 saddr %s ip6 daddr { %s, %s } udp sport %d counter accept comment "internal-mux-port"
  ct state established,related counter accept comment "established"
  iifname "%s" ip6 saddr %s udp sport %d ip6 daddr %s udp dport %d counter %s comment "filter-a2p2-%s"
  iifname "%s" ip6 saddr %s udp sport %d ip6 daddr %s udp dport %d counter %s comment "filter-a1p2-%s"
  iifname "%s" ip6 saddr %s udp sport %d ip6 daddr %s udp dport %d counter accept comment "filter-a1p1"
 }
}
`, topology.InsideInterface, topology.ClientIPv6, topology.ServerA1, topology.ServerA2, muxPort,
		topology.InsideInterface, topology.ClientIPv6, topology.ServerA1, topology.ServerA2, muxPort,
		topology.OutsideInterface, topology.ServerA2, stunAlternatePort, topology.ClientIPv6, muxPort, a2Verdict, a2Verdict,
		topology.OutsideInterface, topology.ServerA1, stunAlternatePort, topology.ClientIPv6, muxPort, a1p2Verdict, a1p2Verdict,
		topology.OutsideInterface, topology.ServerA1, stunPrimaryPort, topology.ClientIPv6, muxPort)
}

func (l *lab) captureNamespace(namespace, label string, router bool) {
	l.t.Helper()
	commands := map[string][]string{
		"ip-address.json":  {"ip", "-j", "addr", "show"},
		"ip-route-v4.json": {"ip", "-j", "route", "show", "table", "all"},
		"ip-route-v6.json": {"ip", "-j", "-6", "route", "show", "table", "all"},
	}
	if router {
		commands["nft-ruleset.json"] = []string{"nft", "-j", "list", "ruleset"}
	}
	for name, command := range commands {
		output := l.inNamespace(namespace, command[0], command[1:]...)
		path := filepath.Join(l.artifactDir, label+"-"+name)
		if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
			l.t.Fatal(err)
		}
	}
	if router {
		if path, err := exec.LookPath("conntrack"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			output, commandErr := suite.command(ctx, "ip", "netns", "exec", namespace, path, "-L")
			cancel()
			if commandErr != nil {
				output += "\noptional conntrack capture failed: " + commandErr.Error() + "\n"
			}
			if err := os.WriteFile(filepath.Join(l.artifactDir, label+"-conntrack.txt"), []byte(output), 0o600); err != nil {
				l.t.Fatal(err)
			}
		}
	}
}

func counterForComment(raw string, comment string) (uint64, bool) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return 0, false
	}
	return findCounter(value, comment)
}

func findCounter(value any, comment string) (uint64, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if typedComment, ok := typed["comment"].(string); ok && typedComment == comment {
			if expressions, ok := typed["expr"].([]any); ok {
				for _, expression := range expressions {
					if object, ok := expression.(map[string]any); ok {
						if counter, ok := object["counter"].(map[string]any); ok {
							if packets, ok := counter["packets"].(float64); ok {
								return uint64(packets), true
							}
						}
					}
				}
			}
		}
		for _, child := range typed {
			if packets, ok := findCounter(child, comment); ok {
				return packets, true
			}
		}
	case []any:
		for _, child := range typed {
			if packets, ok := findCounter(child, comment); ok {
				return packets, true
			}
		}
	}
	return 0, false
}

func endpoint(address netip.Addr, port uint16) string {
	return netip.AddrPortFrom(address, port).String()
}

func policyName(mapping mappingPolicy, filtering filteringPolicy) string {
	mapName := map[mappingPolicy]string{mappingEIM: "eim", mappingADM: "adm", mappingAPDM: "apdm"}[mapping]
	filterName := map[filteringPolicy]string{filteringEIF: "eif", filteringADF: "adf", filteringAPDF: "apdf"}[filtering]
	return strings.Join([]string{mapName, filterName}, "-")
}
