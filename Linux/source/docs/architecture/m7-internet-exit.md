# M7 Linux Internet Exit

M7 composes the encrypted M6 Data Plane with a real Linux TUN, policy routing,
Exit forwarding, nftables NAT44/NAT66, and deterministic HTTP, UDP, and DNS
servers. It is an Internet-path milestone, not the final failure-time kill
switch or Exit-selection runtime.

## Ownership

The logical `tunnelsession.Session` owns the long-lived client tunnel identity,
authenticated `TunnelLease`, `hydra0`, DNS policy, and installed user-routing
state. A replaceable TransportGeneration owns its HydraSocketMux, STUN
observations, shared ICEEngine, selected endpoint, and generation-scoped AWG
transport. Replacing an outer generation does not intrinsically replace the
logical TUN or lease.

The Exit-side `tunnelsession.ExitService` similarly owns one shared TUN,
forwarding owner, nftables installation, and bounded lease allocator for all
downstream clients. Closing one client lease or AWG peer does not close those
shared owners.

## Borrowed TUN lifecycle

The pinned AmneziaWG implementation
`github.com/amnezia-vpn/amneziawg-go/v3 v3.1.20260828` closes the supplied
`tun.Device` from `device.Device.Close()`. Hydra therefore keeps the true TUN
descriptor in `tunnel.Linux` and lends AWG a `F_DUPFD_CLOEXEC` duplicate wrapped
with `tun.CreateTUNFromFile`. Closing a generation-local AWG device closes the
duplicate and releases the borrow; only the logical owner closes the real TUN.
Only one generation may borrow a TUN at a time because concurrent readers would
race for the same inner packets.

## Lease and cryptokey routing

`/hydra/tunnel/1.0.0` is a bounded length-prefixed protobuf exchange over the
existing authenticated libp2p host. A 128-bit random lease ID binds the Exit
PeerID, authenticated Client PeerID, optional IPv4 `/32`, optional IPv6 `/128`,
and expiry. Allocation is Exit-local, bounded, replay-quarantined, and
memory-only. Pools are caller configuration rather than wire constants.

The client has exactly one upstream AWG peer with `0.0.0.0/0` and/or `::/0`.
The Exit gives each downstream AWG peer only its allocated `/32` and `/128`.
Peer keys remain independent of Hydra Ed25519 identity, and private keys and
ICE passwords are never logged.

## Physical UDP and routing

HydraSocketMux remains the sole physical UDP owner. `HydraBind.SetMark`
delegates to `Mux.SetMark`, which transactionally applies real `SO_MARK` to
each enabled UDP family. A TransportGeneration can request the mark before its
first STUN probe, so STUN, ICE, and AWG outer packets all use the physical WAN
policy rather than the protected default route.

The centrally allocated Linux policy is:

```text
SO_MARK 0x48594452 -> table hydra_wan (51821) -> physical WAN
unmarked protected user packet -> table hydra_out (51820) -> hydra0
explicit control/LAN bypass prefixes -> main
```

Unsupported protected families receive an explicit unreachable default in
`hydra_out`; they are not deliberately allowed to fall back to the client ISP.
All route and rule additions are netlink-native, collision-checked, recorded,
and rolled back in reverse order. Core production code does not execute `ip`.

## Exit firewall and NAT

`exitfirewall.NFTBackend` atomically creates one dedicated inet table through
the nftables netlink API. It owns only that table. The policy:

* drops traffic from `hydra0` to Exit-local input;
* drops `hydra0 -> hydra0` forwarding;
* blocks loopback, private, link-local, CGNAT, ULA, and configured management
  destinations before WAN forwarding;
* accepts only Hydra-to-physical-WAN forwarding and established/related
  replies in the reverse direction;
* applies source-scoped IPv4 and IPv6 masquerade on the configured WAN.

`ipforward.Manager` changes only requested namespace forwarding sysctls and
restores their exact previous values. Partial setup failures roll back every
completed owner.

## DNS and independent validation

M7 records tunnel-scoped DNS endpoints but does not itself mutate a host
resolver. A later production Linux runtime adapter can optionally apply
literal-IP resolvers to `hydra0`; that adapter remains outside the M7 transport
and lease protocol. Privileged M7 tests use ordinary socket clients and
deterministic DNS UDP/TCP servers. The same deterministic Internet namespace
also provides HTTP and UDP echo. Remote observations must see the Exit WAN
address after NAT44 or NAT66 and the client socket must have its assigned inner
source.

Namespace-local nftables counters independently prove marked outer traffic,
zero direct protected-flow bypass, NAT44/NAT66 use, Exit destination blocking,
and an exercised `hydra0 -> hydra0` drop. Socket inode snapshots prove AWG did
not replace the Hydra transport.

The three-client scenario uses one Exit PeerID, Mux, ICEEngine, real TUN, and
AWG transport with three non-overlapping cryptokey routes. All clients reach
the deterministic Internet; after one is closed, the other two establish new
flows without rebuilding the Exit transport.

## Reconnect and transport migration

A same-network ICE restart retains the logical lease, kernel TUN, routing
owner, Mux, socket inode, and AWG device. A fresh SessionID, Pion Agent, and
selected pair may update only that peer's generation-fenced AWG endpoint.
Other Exit peers remain untouched.

A NetworkGeneration change replaces the Mux, ICEEngine, routing owner, and AWG
device while retaining the logical lease and the original kernel TUN ifindex.
The old generation cannot install a later endpoint or routing result. Because
the pinned AWG device closes its supplied TUN and concurrent TUN readers would
race, the current implementation deliberately releases the old duplicated TUN
borrow before lending a new duplicate to the next AWG device. This is a
bounded break-before-make at the AWG-device layer; it does not recreate
`hydra0` or its logical ownership. Production make-before-break routing and
failure-time packet fencing remain later work.

The privileged migration and reconnect tests establish a fresh deterministic
Internet flow after the swap and independently require the same TUN ifindex.
The NAT composition test additionally preserves the M3b STUN to ICE to AWG
socket and port continuity before applying Exit NAT44.

## Explicit boundaries

M7 proves healthy-path routing and no intentional unsupported-family fallback.
It does not prove zero leakage after abrupt Exit failure; M8 owns that kill
switch. Production rendezvous reachability, Exit selection/reselection,
ACTIVE/STANDBY/COLD policy, Power-of-K, GUI/runtime commands, TURN/relay,
multi-hop, and abuse policy beyond the explicit destination boundary remain
deferred.
