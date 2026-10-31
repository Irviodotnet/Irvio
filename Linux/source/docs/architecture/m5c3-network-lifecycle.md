# M5c3 network and ICE generation lifecycle

M5c3 is the lifecycle boundary between the proven ICE control plane and the
future encrypted data plane. It does not carry application payload through
Pion ICE and is not VPN runtime integration.

## Ownership

```text
Hydra identity + authenticated libp2p host
                    |
             NetworkGeneration N
                    |
          TransportGeneration N
          /        |           \
 HydraSocketMux  PhaseManager  generation-bound STUN
                    |
                 ICE lease
                    |
                 ICEEngine
          /          |          \
 one Registry   one Adapter   one Control reader
                    |
          +---------+---------+
          |         |         |
       Agent A   Agent B   Agent C
```

`transportgen.Generation` owns the mux, phase manager, STUN observations and
shared engine. `iceengine.Engine` owns its one adapter, registry and ICE lease.
A PreparedSession owns one Pion Agent, credentials, immutable local description
and its bounded engine slot. It only borrows the adapter. A ConnectedSession
closes that PreparedSession; it cannot close the engine or mux. The libp2p host,
identity and PeerDirectory remain outside transport generations.

The shared engine is bounded by its configured session and adapter registry
limits. It creates no physical socket. Three simultaneous clients have distinct
ufrags and Agents but share one adapter and one physical Hydra socket; replacing
one client generation leaves the other two Agents and selected pairs intact.

## Semantic network state

`networkstate.Snapshot` contains only transport-relevant default-route state:

- family, main-table route priority, egress ifindex and gateway;
- relevant link ifindex, name, UP state and MTU;
- sorted usable unicast prefixes on those egress links.

Loopback, unspecified, multicast and link-local addresses are excluded. Links
not referenced by a current default route are excluded, so unrelated container
or veth churn does not restart Hydra. Route, address and link ordering and
duplicates are normalized.

Linux route, address and link subscriptions are only rescan triggers:

```text
netlink notification burst
        -> bounded notification channel
        -> debounce/coalesce
        -> authoritative route/link/address rescan
        -> normalize and compare
        -> publish Generation+1 only if different
```

The change channel retains the latest state rather than blocking the netlink
reader. A closed subscription, callback error or failed rescan is a visible
terminal monitor error. The owner may replace the monitor; there is no silent
background retry loop. NetworkGeneration is monotonic and process-local, and
is intentionally not persisted.

The privileged netns test drives real kernel changes without sleeps: IPv4 and
IPv6 address replacement, default-route switch, MTU change, link down/up, and
an unrelated-interface burst. File-based readiness records provide explicit
barriers. The final semantic states appear exactly once even when the kernel
emits several notifications for one operation.

## Session generations and reconnect

Hydra restart means a replacement session, not in-place Pion mutation:

```text
logical peer/purpose intent
  SessionGeneration 1: SessionID A, Agent A, ufrag/pwd A
  SessionGeneration 2: SessionID B, Agent B, ufrag/pwd B
  SessionGeneration 3: SessionID C, Agent C, ufrag/pwd C
```

Pion v4.4.1 `Agent.Restart` clears internal state, cancels gathering and needs a
new gather. M5c3 does not call it. A fresh Agent keeps the one-shot M5c1
contract, immutable M5b descriptions and hard stale-completion fencing. It also
allows a replacement Agent to connect through the same shared engine while the
old selected pair remains alive.

For a same-network failure the supervisor retains NetworkGeneration, mux,
physical socket/inode, mux port, engine and generation-bound STUN observations.
It creates a new SessionID, Agent, credentials, complete candidate description,
authenticated Offer/Answer, candidate load and Dial/Accept. The new session is
promoted only if both its NetworkGeneration and SessionGeneration still equal
the desired values. Older late results are closed. The prior active session is
closed only after successful promotion.

Disconnected alone does not immediately replace a session. Pion may recover
before its Failed timeout; that keeps the SessionID, credentials and Agent.
Failed or an expired configurable disconnected grace starts a fresh generation.
Failed attempts use bounded exponential backoff. A newer network generation
cancels a stale attempt or backoff immediately. A shared limiter bounds
simultaneous reconnect work across supervisors. After a configurable bounded
number of consecutive failures in one network generation, retries stop and the
supervisor publishes `EventTransportRefreshRequested`; the transport owner can
then close ICE and perform a fresh phase-exclusive STUN/transport build.

## Transport migration

A semantic network change makes the old transport stale. Stale transports deny
new PreparedSessions and cancel generation-bound preparation. The manager
coalesces to the newest requested generation and bounds concurrent builds.

```text
Generation N connected
        -> semantic network change
        -> mark N stale
        -> build mux N+1
        -> fresh M3b STUN N+1
        -> shared ICEEngine N+1
        -> fresh M5b/M5c session N+1
        -> fenced atomic activation
        -> close N
```

Where the old path remains usable, it stays connected until the new migration
finishes. A delayed build or signaling completion for N is discarded after
N+1 becomes desired. A transport-generation STUN record contains the local
NetworkGeneration, Mux.Port, family, local endpoint, mapped endpoint and
observation time; it cannot be reused by N+1.

The full-chain integration test uses two concrete local transport addresses,
a real M3b responder, two independent secure libp2p peers, new Pion credentials
and real Dial/Accept on each generation. It verifies both simultaneous sessions
share one local engine, are re-established before promotion, use only the
second address and STUN endpoint, replace their Agents/SessionIDs/credentials,
and close the old transport after promotion.

## Physical and privileged evidence

The namespace lab proves persistent direct IPv4 and IPv6 loss reaches Pion
Disconnected then Failed, restores the path, and connects generation two with
fresh credentials over the same `/proc/net/udp` or `/proc/net/udp6` inode. A
separate controlled EIM+APDF NAT case repeats real M3b STUN and authenticated
signaling, blocks the established path with scoped namespace nftables counters,
and reconnects with the same Hydra socket and mapped endpoint. Pion creates no
listener and application packet/byte counters remain zero.

The direct dual-stack lifecycle case places authenticated signaling on a
separate IPv4 control veth, removes the connected route for the selected UDP
family on both peers, and waits on Pion state events. If the existing Agent
recovers on the other nominated pair, the SessionID remains unchanged; if it
reaches Failed, the supervisor creates a fresh SessionGeneration. Either valid
outcome must select the surviving family, retain both original Hydra UDP socket
inodes, and send zero application bytes. With pinned Pion v4.4.1 the observed
path is Failed followed by a fresh Agent on the other family.

A routed IPv4 address-renumbering case keeps the same interface and default
route, replaces only its active unicast address, and obtains a fresh real M3b
observation before signaling and selecting the replacement HOST path. The new
transport has a different socket inode and its description contains neither
the old host address nor stale STUN state.

A routed IPv6 migration test removes the active global client address and adds
a different address on the same default-route interface. Real netlink state
advances once to generation two; a new UDP6 mux, Agent, SessionID, credentials,
authenticated description exchange, and HOST-to-HOST selected pair use only
the replacement address. The old candidate and socket inode are absent after
activation. Its libp2p stream uses a separate test-only IPv4 control veth.

A second EIM+APDF migration topology gives the initiator two private interfaces
and two namespace-local NAT gateways. Replacing the kernel default route is
observed by the real netlink monitor. The manager keeps generation one alive
while generation two binds the new private address, obtains a distinct mapped
endpoint from the same real responder, completes a fresh authenticated M5b
exchange and selects the new NAT path. Only after activation are the old ICE
session, engine and physical socket closed. A separate hard-break variant first
puts the old data link down, then proves the same fresh-generation chain reaches
the new path without relying on the old connection remaining usable.

All Go binaries are built as the normal user. Only the prebuilt runner executes
through sudo. Network mutations, forwarding, NAT and firewall state stay in
uniquely named disposable namespaces. Host state is normalized before/after,
and cleanup is transactional and idempotent. Bounded artifacts omit passwords,
private keys and full secret-bearing signaling messages.

## Commands

```bash
make test-m5c3
make test-m5c3-netns
make test-m5c3-soak
make clean-m4b
```

The soak target runs 100 fresh same-network reconnect lifecycles, 25
three-client isolation/reconnect iterations, 25 full transport migrations, 25
real netlink switch sequences, and 10 iterations each of direct IPv4, direct
IPv6, dual-stack selected-family loss, controlled NAT reconnect,
OLD-NAT-to-NEW-NAT migration, IPv4 address migration, and IPv6 address
migration.

## Boundaries before M6

Control remains phase-exclusive. While an ICEEngine owns Control, Hydra cannot
refresh STUN concurrently; repeated same-generation failures must escalate to a
whole transport refresh. M5c3 provides the generation boundary but no general
ControlRouter.

The privileged NAT tests retain a separate test-only authenticated libp2p
control network. Production signaling mobility or relay between NATed peers is
not solved. Network selection remains one explicit address per family; there
is no continual gathering, automatic renomination, TURN, relay, TCP ICE,
trickle ICE, mobile API, or persisted active state.

M6 owns HydraBind, AmneziaWG and encrypted application data. M7 owns TUN,
routing, SNAT, DNS, kill switch and Internet exit.
