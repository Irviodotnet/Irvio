// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"fmt"
	"net/netip"
)

const (
	m5c2AStunPort = 51011
	m5c2APeerPort = 51012
	m5c2BStunPort = 52011
	m5c2BPeerPort = 52012
)

type iceNATTopology struct {
	ANamespace      string     `json:"a_namespace"`
	BNamespace      string     `json:"b_namespace"`
	ARouter         string     `json:"a_router,omitempty"`
	BRouter         string     `json:"b_router,omitempty"`
	ServerNamespace string     `json:"server_namespace"`
	SwitchNamespace string     `json:"switch_namespace"`
	AData           netip.Addr `json:"a_data"`
	BData           netip.Addr `json:"b_data"`
	AControl        netip.Addr `json:"a_control"`
	BControl        netip.Addr `json:"b_control"`
	AExternal       netip.Addr `json:"a_external"`
	BExternal       netip.Addr `json:"b_external"`
	ServerA1        netip.Addr `json:"server_a1"`
	ServerA2        netip.Addr `json:"server_a2"`
	ADataIf         string     `json:"a_data_interface"`
	AInsideIf       string     `json:"a_inside_interface,omitempty"`
	AOutsideIf      string     `json:"a_outside_interface,omitempty"`
	BInsideIf       string     `json:"b_inside_interface,omitempty"`
	BOutsideIf      string     `json:"b_outside_interface,omitempty"`
	ANAT            bool       `json:"a_nat"`
	BNAT            bool       `json:"b_nat"`
}

func (l *lab) buildICENATTopology(aNAT, bNAT bool) iceNATTopology {
	l.t.Helper()
	topology := iceNATTopology{
		ANamespace: l.namespace("a"), BNamespace: l.namespace("b"),
		ServerNamespace: l.namespace("server"), SwitchNamespace: l.namespace("switch"),
		AControl: netip.MustParseAddr("192.0.2.1"), BControl: netip.MustParseAddr("192.0.2.2"),
		AExternal: netip.MustParseAddr("198.18.90.10"), BExternal: netip.MustParseAddr("198.18.90.20"),
		ServerA1: netip.MustParseAddr("198.18.90.100"), ServerA2: netip.MustParseAddr("198.18.90.101"),
		ANAT: aNAT, BNAT: bNAT,
	}
	if aNAT {
		topology.ARouter = l.namespace("router1")
		topology.AData = netip.MustParseAddr("10.51.1.2")
		topology.ADataIf = l.interfaceName('a')
		topology.AInsideIf = l.interfaceName('1')
		topology.AOutsideIf = l.interfaceName('2')
	} else {
		topology.AData = netip.MustParseAddr("198.18.90.30")
		topology.ADataIf = l.interfaceName('a')
	}
	if bNAT {
		topology.BRouter = l.namespace("router2")
		topology.BData = netip.MustParseAddr("10.51.2.2")
		topology.BInsideIf = l.interfaceName('4')
		topology.BOutsideIf = l.interfaceName('5')
	} else {
		topology.BData = netip.MustParseAddr("198.18.90.40")
	}

	for _, namespace := range []string{topology.ANamespace, topology.BNamespace, topology.ServerNamespace, topology.SwitchNamespace, topology.ARouter, topology.BRouter} {
		if namespace != "" {
			l.inNamespace(namespace, "ip", "link", "set", "lo", "up")
		}
	}
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "add", "bd0", "type", "bridge")
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "add", "bc0", "type", "bridge")
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", "bd0", "up")
	l.inNamespace(topology.SwitchNamespace, "ip", "link", "set", "bc0", "up")

	l.attachICEControl(topology.ANamespace, l.interfaceName('c'), topology.AControl, topology.SwitchNamespace, l.interfaceName('7'))
	l.attachICEControl(topology.BNamespace, l.interfaceName('d'), topology.BControl, topology.SwitchNamespace, l.interfaceName('8'))
	l.attachICEExternal(topology.ServerNamespace, l.interfaceName('s'), []netip.Addr{topology.ServerA1, topology.ServerA2}, topology.SwitchNamespace, l.interfaceName('9'))
	if aNAT {
		l.attachICEPrivate(topology.ANamespace, topology.ADataIf, topology.AData, topology.ARouter, topology.AInsideIf, netip.MustParseAddr("10.51.1.1"))
		l.attachICEExternal(topology.ARouter, topology.AOutsideIf, []netip.Addr{topology.AExternal}, topology.SwitchNamespace, l.interfaceName('3'))
		l.inNamespace(topology.ANamespace, "ip", "route", "add", "default", "via", "10.51.1.1")
		l.enableICERouter(topology.ARouter)
	} else {
		l.attachICEExternal(topology.ANamespace, topology.ADataIf, []netip.Addr{topology.AData}, topology.SwitchNamespace, l.interfaceName('3'))
	}
	if bNAT {
		l.attachICEPrivate(topology.BNamespace, l.interfaceName('b'), topology.BData, topology.BRouter, topology.BInsideIf, netip.MustParseAddr("10.51.2.1"))
		l.attachICEExternal(topology.BRouter, topology.BOutsideIf, []netip.Addr{topology.BExternal}, topology.SwitchNamespace, l.interfaceName('6'))
		l.inNamespace(topology.BNamespace, "ip", "route", "add", "default", "via", "10.51.2.1")
		l.enableICERouter(topology.BRouter)
	} else {
		l.attachICEExternal(topology.BNamespace, l.interfaceName('b'), []netip.Addr{topology.BData}, topology.SwitchNamespace, l.interfaceName('6'))
	}
	for _, namespace := range []string{topology.ANamespace, topology.BNamespace} {
		l.inNamespace(namespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=0")
	}
	l.recordTopology(topology)
	return topology
}

func (l *lab) attachICEControl(namespace, localIf string, address netip.Addr, switchNamespace, switchIf string) {
	l.t.Helper()
	l.createVeth(namespace, localIf, switchNamespace, switchIf)
	l.inNamespace(namespace, "ip", "link", "set", localIf, "up")
	l.inNamespace(namespace, "ip", "addr", "add", address.String()+"/24", "dev", localIf)
	l.inNamespace(switchNamespace, "ip", "link", "set", switchIf, "master", "bc0")
	l.inNamespace(switchNamespace, "ip", "link", "set", switchIf, "up")
}

func (l *lab) attachICEExternal(namespace, localIf string, addresses []netip.Addr, switchNamespace, switchIf string) {
	l.t.Helper()
	l.createVeth(namespace, localIf, switchNamespace, switchIf)
	l.inNamespace(namespace, "ip", "link", "set", localIf, "up")
	for _, address := range addresses {
		l.inNamespace(namespace, "ip", "addr", "add", address.String()+"/24", "dev", localIf)
	}
	l.inNamespace(switchNamespace, "ip", "link", "set", switchIf, "master", "bd0")
	l.inNamespace(switchNamespace, "ip", "link", "set", switchIf, "up")
}

func (l *lab) attachICEPrivate(peerNamespace, peerIf string, peerAddress netip.Addr, routerNamespace, routerIf string, routerAddress netip.Addr) {
	l.t.Helper()
	l.createVeth(peerNamespace, peerIf, routerNamespace, routerIf)
	l.inNamespace(peerNamespace, "ip", "link", "set", peerIf, "up")
	l.inNamespace(routerNamespace, "ip", "link", "set", routerIf, "up")
	l.inNamespace(peerNamespace, "ip", "addr", "add", peerAddress.String()+"/24", "dev", peerIf)
	l.inNamespace(routerNamespace, "ip", "addr", "add", routerAddress.String()+"/24", "dev", routerIf)
}

func (l *lab) enableICERouter(namespace string) {
	l.t.Helper()
	l.inNamespace(namespace, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1")
	l.inNamespace(namespace, "sysctl", "-q", "-w", "net.ipv4.conf.all.rp_filter=0")
}

func iceNATRules(private, external, stunServer, remoteExternal netip.Addr, insideIf, outsideIf string,
	stunExternalPort, peerExternalPort, remoteAdvertisedPort, remotePeerPort int, mapping mappingPolicy, filtering filteringPolicy,
) string {
	if mapping == mappingEIM {
		peerExternalPort = stunExternalPort
	}
	localPorts := uniquePortSet(stunExternalPort, peerExternalPort)
	remotePorts := uniquePortSet(remoteAdvertisedPort, remotePeerPort)
	inboundRule := fmt.Sprintf("iifname \"%s\" ip daddr %s udp dport %d counter drop comment \"m5c2-inbound-apdf-drop\"",
		outsideIf, private, clientMuxPort)
	if filtering == filteringEIF {
		inboundRule = fmt.Sprintf("iifname \"%s\" ip daddr %s udp dport %d counter accept comment \"m5c2-inbound-eif\"",
			outsideIf, private, clientMuxPort)
	}
	return fmt.Sprintf(`table ip hydra_m5c2 {
 chain prerouting {
  type nat hook prerouting priority dstnat; policy accept;
  iifname "%s" ip daddr %s udp dport %s counter dnat to %s:%d comment "m5c2-dnat"
 }
 chain postrouting {
  type nat hook postrouting priority srcnat; policy accept;
  ip saddr %s udp sport %d ip daddr %s udp dport %d counter snat to %s:%d comment "m5c2-stun-snat"
  ip saddr %s udp sport %d ip daddr %s udp dport %s counter snat to %s:%d comment "m5c2-peer-snat"
 }
 chain forward {
  type filter hook forward priority filter; policy drop;
  iifname "%s" ip saddr %s udp sport != %d counter drop comment "m5c2-internal-bad-port"
  iifname "%s" ip saddr %s udp sport %d ip daddr %s udp dport %d counter accept comment "m5c2-internal-stun"
  iifname "%s" ip saddr %s udp sport %d ip daddr %s udp dport %s counter accept comment "m5c2-internal-peer"
  ct state established,related counter accept comment "m5c2-established"
  %s
 }
}
`, outsideIf, external, localPorts, private, clientMuxPort,
		private, clientMuxPort, stunServer, stunPrimaryPort, external, stunExternalPort,
		private, clientMuxPort, remoteExternal, remotePorts, external, peerExternalPort,
		insideIf, private, clientMuxPort,
		insideIf, private, clientMuxPort, stunServer, stunPrimaryPort,
		insideIf, private, clientMuxPort, remoteExternal, remotePorts,
		inboundRule)
}
