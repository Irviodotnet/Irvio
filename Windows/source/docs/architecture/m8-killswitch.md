# M8 Fail-Closed Protected Mode

M8 adds a Linux kernel boundary for locally-originated traffic. `ProtectedMode`
is user intent, not a synonym for ICE, AWG, TUN, or Exit health. Once protection
has committed, connecting, connected, recovery, migration, and no-Exit states
all retain the same fail-closed rule:

```text
ordinary traffic -> hydra0 or drop
Hydra-owned traffic marked 0x48594452 -> physical WAN
```

M8 does not select or reselect an Exit. It supplies the state on which a later
selector can safely wait.

## Ownership and state

`hydrad --protected-mode` constructs `killswitch.Controller` before it starts
the discovery/libp2p node. The controller owns the logical lifecycle, one
persistent `hydra_protect` nftables table, and IPv4 `src_valid_mark`. Its
monotonically increasing lifecycle
generation fences late tunnel, route, reconnect, and migration completions.
The states are VPN off, protecting, protected-connecting, connected,
same-peer-recovery, network-migration, no-Exit, unprotecting, and closed.
Every committed state except VPN off and closed is protected.

Activation order is:

```text
atomic nftables install and verification
-> commit ProtectedMode
-> marked control/outer connectivity
-> ICE and AWG
-> protected user routing
```

Explicit Disconnect and clean service shutdown use the reverse ownership
order. Tunnel and user-routing resources close while protection remains
installed; the nftables table is removed last. A teardown or policy-removal
failure is returned and leaves a restrictive state instead of reporting VPN
off.

The controller's resource builder runs only after kernel protection commits.
A stale asynchronous builder result is closed and cannot resurrect routing
after Disconnect or a newer lifecycle generation.

## Crash persistence and reconciliation

The kernel table deliberately has no process-lifetime cleanup hook. SIGKILL
therefore leaves its fail-closed OUTPUT chain active. On restart, `Ensure`
recognizes the exact `hydra-protected-mode-v1` ownership marker and bounded
rule set. It atomically reapplies the canonical expressions before reporting
adoption, so matching comments cannot disguise a changed mark or interface
condition. An owned but incomplete same-version policy is likewise atomically
reconciled. A different version or an unrelated administrator
table with the same name is rejected and is never deleted.

The table records the exact pre-Hydra `src_valid_mark` value in an owned
metadata rule. A restarted daemon can therefore adopt a crash-surviving policy,
and explicit Disconnect can restore the previous setting without relying on
process memory. This API is intended to run during privileged service startup
before tunnel recovery. Explicit Disconnect of an adopted controller removes
only the verified Hydra-owned table and restores that recorded value.

## nftables policy

The dedicated inet OUTPUT chain is interface-independent. A newly appearing
Ethernet, Wi-Fi, LTE, veth, or other non-Hydra interface is protected without a
rule rewrite. In order, it permits:

* the exact `HYDRA_BYPASS_MARK` (`0x48594452`);
* loopback;
* the configured long-lived `hydra0`;
* DHCPv4 client 68 to server 67;
* DHCPv6 client 546 to server 547;
* IPv6 router solicitation/advertisement and neighbor solicitation/
  advertisement.

The base chain itself has a drop policy, and a terminal drop rule provides an
independent packet counter. There is no root-process exception,
destination allowlist, LAN allowlist, generic UDP/ICMP allowance, or DNS
exception. The M7 `hydra_out` and `hydra_wan` policy tables remain defense in
depth; disappearance of `hydra0`, a route, or a policy rule cannot bypass the
OUTPUT drop. M7 Exit forwarding remains a separate FORWARD/NAT policy and is
not modified by the client kill switch.

Policy creation and reconciliation use one nftables netlink batch, so a policy
replacement does not delete the old restrictive state before the replacement
is committed.

The same table has a mangle-priority PREROUTING chain. It copies the mark from
an inbound packet's already-selected socket only when that socket carries one
of two exact Hydra transport intents. `HYDRA_BYPASS_MARK` makes Linux
reverse-path validation check replies to Hydra-owned physical sockets through
`hydra_wan`, not the fail-closed unmarked `hydra_out` table.
`HYDRA_TUNNEL_CONTROL_MARK` makes validation of an authenticated session-
control reply use table 51822, which contains only the current remote lease
`/32` or `/128` through `hydra0` and an unreachable default. With
`src_valid_mark=1`, loose or strict `rp_filter` therefore cannot discard outer
ICE/AWG packets or valid inner control packets after protected routes are
installed. No ordinary unmarked socket or destination receives either
privilege.

## Hydra bypass

HydraSocketMux retains real `SO_MARK` on its sole UDP4/UDP6 descriptors.
Consequently STUN, ICE, and AWG outer packets continue through `hydra_wan`
without a second socket.

Pinned go-libp2p v0.49.0 exposes `tcptransport.WithDialerForAddr` but no
listener socket-control option. Hydra uses that official dialer hook and a
`net.Dialer.Control` callback to set `SO_MARK` before every outbound TCP
connect. This is the recovery-critical direction for the protected Client:
bootstrap, Kademlia, signaling, and future Rendezvous connections are
initiated toward literal-IP multiaddresses. Protected client recovery does not
depend on accepting a new inbound TCP connection. If a future protected role
requires inbound-initiated libp2p recovery, it must add a dedicated service
UID/cgroup marking policy rather than granting a root-wide bypass or copying
the upstream TCP transport.

Setting `SO_MARK` requires privilege. Hydra does not grant ordinary users
`CAP_NET_ADMIN` or `CAP_NET_RAW`; the namespace acceptance test removes all
capabilities from an ordinary process and proves that it cannot obtain the
Hydra mark.

## IP-native recovery and DNS

Recovery uses only literal `/ip4` and `/ip6` libp2p multiaddresses plus PeerID.
M8 adds no `/dns`, `/dns4`, `/dns6`, FQDN, or port-53 exception. Ordinary UDP
and TCP DNS either traverse Hydra when a tunnel is healthy or are dropped.
A marked authenticated libp2p exchange and a marked HydraSocketMux UDP control
operation remain usable with no VPN Data Plane.

The same-destination test proves ownership rather than destination identity:
a marked Hydra connection reaches a test endpoint while an ordinary unmarked
connection to the identical IP is dropped.

## Failure and migration behavior

Exit death, AWG failure, `hydra0` loss, `hydra_out` loss, same-peer reconnect,
and NetworkGeneration migration never transition user intent to VPN off.
Independent namespace sinks and nftables counters check both IPv4 and IPv6.
Barrier-driven UDP probes run across an Exit fault and network migration; any
packet observed directly on either physical path is a failure. Marked Hydra
control remains usable.

Graceful shutdown is an explicit Disconnect and restores ordinary WAN only
after protected resources are gone. Unexpected death leaves protection for a
new service instance to adopt.

## Boundaries

M8 is the fail-closed client boundary. It does not implement Exit selection,
Power-of-K, ACTIVE/STANDBY/COLD, production Rendezvous, GUI ownership, public
server verification, telemetry, or token economics. M10 will prove the full
simultaneous Client+Exit composition; the OUTPUT-only design intentionally
preserves that possibility.
