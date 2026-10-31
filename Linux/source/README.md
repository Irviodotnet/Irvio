# HYDRA

HYDRA is a Linux-only distributed control and discovery core. M0-M2 provides
persistent Ed25519/libp2p identity, a local SQLite peer directory, signed node
announcements, and capability discovery over a libp2p Kademlia DHT. There is no
central control server.

HYDRA Core v1 is frozen at the completed M12 baseline. It includes bounded
Rendezvous v1 discovery, end-to-end signed brokered establishment, and direct
TCP+Noise lease maintenance inside AWG. A promoted session does not depend on
Rendezvous availability, and Rendezvous never carries application traffic.
See [`docs/architecture/m12-rendezvous.md`](docs/architecture/m12-rendezvous.md)
and
[`docs/architecture/m12-session-control.md`](docs/architecture/m12-session-control.md).

P1 adds a thin, versioned local product-control surface over a protected Unix
domain socket. It maps the existing production runtime to stable UI states and
accepts Connect/Disconnect without owning any VPN lifecycle. GUI disconnects
do not affect Core. See
[`docs/product/local-api-v1.md`](docs/product/local-api-v1.md).

P2 adds the first Linux desktop GUI as a thin Flutter client of that API. It
renders the stable product states, sends only Connect/Disconnect and supported
product commands, resynchronizes with `GET_STATUS`, and keeps Quit UI separate
from VPN Disconnect. Unsupported v1.0 contribution and diagnostic features are
shown honestly rather than fabricated. See
[`docs/product/linux-gui-p2.md`](docs/product/linux-gui-p2.md).

P2.1 adds field UX without changing Core: the central control is the sole
Connect/Disconnect action, logical uptime is daemon-authoritative, logs are
bounded and sanitized, and a fresh install can import or share versioned
literal-IP/PeerID bootstrap bundles. Local API v1.2 also lets Settings request
a guarded daemon-owned profile deletion and self-restart; Flutter never
deletes profile files. A bundle remains an untrusted introduction hint;
authenticated libp2p PeerID verification is the trust boundary. See
[`docs/product/linux-gui-p2.1.md`](docs/product/linux-gui-p2.1.md).

P2.2 replaces the static location selector with a bounded live country view.
Rendezvous classifies the server-observed source IP of fresh, accepting Exit
registrations with the existing Country MMDB resolver; local hydrad aggregates
only ISO country codes and exposes them through Local API v1.3. Flutter does
not perform GeoIP, and country metadata never controls an active VPN lifecycle.
See [`docs/product/linux-gui-p2.2.md`](docs/product/linux-gui-p2.2.md).

The in-progress Alpha product runtime profiles reduce normal startup intent to
`hydrad` on desktop or `hydrad --role server --bootstrap '<v1 bundle>'` on a
server. Low-level flags below are engineering examples, not the planned Alpha
deployment interface. Product-path privileged acceptance is still pending;
see [`docs/product/product-runtime-profiles-v1.md`](docs/product/product-runtime-profiles-v1.md)
and its linked validation status.

The frozen M12 baseline does not claim a global live database, globally uniform sampling,
geographic egress attestation, Sybil resistance, signaling confidentiality,
TURN/relay, DNS bootstrap, or central telemetry. The later Alpha server preset
adds a public STUN UDP/3478 responder as a separate infrastructure capability.
Privileged completion evidence is tracked explicitly in
[`docs/testing/m12-acceptance.md`](docs/testing/m12-acceptance.md); a blocked or
skipped namespace gate is not treated as a pass.

## M0-M2 architecture

A new node may start with an empty peer directory and import one or more
literal-IP/PeerID introductions through the product API. A bootstrap peer
introduces it to the DHT; synthetic provider
CIDs then expand knowledge from `0 -> 1 -> N`. A provider record is only a hint.
It may identify a valid PeerID without a dialable address. Discovery must use
bounded address resolution or a later usable locator, rather than discard that
PeerID merely because its first provider observation is addressless.
The discovering node connects directly to each provider, fetches
`/hydra/node/1.0.0`, verifies the libp2p signed envelope, enforces
`provider PeerID == signer PeerID == announcement NodeID`, and only then writes
the announcement to SQLite.

Signed announcements use domain `hydra/node-announcement`, private-use
multicodec `0x300001`, a 32 KiB payload limit, and a 64 KiB signed-envelope
limit.

`EXIT/DE` maps deterministically to the CIDv1/raw SHA-256 digest of
`hydra/v1/exit/DE`. Country input is normalized and validated before this key is
built.

## Environment and build

The initial workspace was built with Go 1.27.1, protoc 3.21.12, and
protoc-gen-go 1.36.12.

```bash
make proto
make build
make test
make test-race
```

The binary is written to `bin/hydrad`. Runtime identity and database files are
`<data-dir>/identity.key` and `<data-dir>/hydra.db`; every instance must use a
different data directory.

## Three nodes (legacy engineering example)

Start bootstrap B and request its machine-readable address:

```bash
bin/hydrad --data-dir /tmp/hydra-b --listen-port 41002 --print-node-info
```

The command prints one JSON object to stdout while structured logs go to
stderr. Use one of its `listen_addrs` values as `<B_MULTIADDR>`.

Start C and advertise `EXIT/DE`:

```bash
bin/hydrad \
  --data-dir /tmp/hydra-c \
  --listen-port 41003 \
  --bootstrap '<B_MULTIADDR>' \
  --exit \
  --country DE
```

Start A knowing only B and discover C:

```bash
bin/hydrad \
  --data-dir /tmp/hydra-a \
  --listen-port 41001 \
  --bootstrap '<B_MULTIADDR>' \
  --find-exits DE
```

For a one-shot machine-readable discovery result, add `--find-once`. Multiple
`--bootstrap` flags are accepted. `--dht-mode server` is the deterministic
M0-M2 default; `--dht-mode auto` is available for later deployment policy. Nodes try both IPv4 and IPv6 TCP
listeners;
failure to create the IPv6 listener falls back to IPv4 without preventing
startup. libp2p default transports and relay are disabled; M0-M2 enables only
the TCP transport.

The complete repeatable three-process demonstration is:

```bash
make demo
```

The integration test additionally closes B after discovery and proves A can
still fetch and verify C's announcement directly.

## M3a shared UDP transport foundation

`internal/socketmux` owns at most one physical UDP4 socket and one physical
UDP6 socket. When both families are active they bind the same numeric port, and
the IPv6 socket is explicitly IPv6-only. Logical Control and Data endpoints
receive from separate bounded queues but send through the same family-specific
physical socket, preserving one stable local source port and ownership domain.

Inbound packets are copied before delivery. Queue writes are non-blocking, so a
stalled Control consumer cannot block Data and vice versa; overflow and policy
drops are counted. Classification is injected, and the safe default sends
unknown packets to Data. Transaction-aware Basic STUN classification is added
by M3b; RFC 5780 profiling is added by the isolated M4a library, ICE
integration to M5, and
AmneziaWG/HydraBind integration to M6. M3a alone does not provide NAT traversal
and is not started by `hydrad`.

Run the focused unit and loopback integration tests with:

```bash
make test-m3a
```

## M3b Basic STUN Binding foundation

`internal/stunprobe` performs Basic STUN Binding through the existing
`HydraSocketMux` Control endpoint. A transaction registry is created before the
mux and is shared by the injected classifier and STUN client. Exactly one client
dispatcher reads Control; concurrent probes receive responses through bounded
per-transaction delivery slots instead of racing as Control consumers.

Pion STUN v3 performs request encoding, strict response decoding, and
XOR-MAPPED-ADDRESS decoding. A response is Control only when its Binding method,
response class, exact datagram framing, transaction ID, source endpoint, and
address family match an active probe. Malformed and unknown valid STUN packets
remain Data. Matching responses for recently completed or canceled transactions
are quarantined in a time- and count-bounded retired set and dropped.

IPv4 and IPv6 probes both send through the mux-owned physical sockets, so their
source port remains `Mux.Port()`. The client uses bounded exponential
retransmission and does not own or close the mux. It is not started by `hydrad`.
M3b reports only the mapped endpoint observed by a Binding server; it does not
infer NAT type or provide NAT traversal. RFC 5780 profiling is deferred to M4,
ICE to M5, and AmneziaWG/HydraBind to M6.

Run the focused unit, integration, and M3a regression tests with:

```bash
make test-m3b
```

## M4a RFC 5780 behavior profiling

`internal/natprobe` produces a bounded diagnostic snapshot with independent
mapping, filtering, and address-translation fields. It deliberately does not
collapse these observations into legacy cone/symmetric NAT labels. The profile
applies only to the current HydraSocketMux UDP socket and current network path;
behavior may change with route, interface, public-address, NAT-load, or port
conflict changes.

The profiler obtains a strict baseline topology from MAPPED-ADDRESS,
XOR-MAPPED-ADDRESS, RESPONSE-ORIGIN, and OTHER-ADDRESS. Filtering tests run
before any mapping request reaches an alternate endpoint. Changed-address
success responses use an explicit alternate-source transaction policy while
Binding errors remain allowed only from the request destination. A filtering
timeout is used as evidence only after a normal request confirms that the
primary endpoint is still live; timeout-derived conclusions are marked in the
result. Mapping timeouts remain inconclusive.

Pion continues to encode and decode STUN while the existing single M3b
dispatcher owns Control reads. The profiler creates no socket, and every real
IPv4 test request passes through the mux-owned socket with source port
`Mux.Port()`. Tests use a scripted four-socket IPv4 topology on
`127.0.0.1/127.0.0.2`. IPv6 algorithm and concurrency coverage use the
`BindingExchanger` boundary; the existing M3b tests retain real IPv6 socket and
source-port coverage. A real two-address IPv6 RFC 5780 topology is deferred to
M4b network-namespace testing because ordinary loopback environments generally
provide only `::1`.

This snapshot is not NAT traversal, does not discover binding lifetime or
hairpin behavior, and does not predict ICE success. It is not persisted and is
not started by `hydrad`; a future network-generation design must invalidate
cached observations after path changes. RFC 5780 netns validation is deferred
to M4b, ICE to M5, and AmneziaWG/HydraBind to M6.

Run the focused algorithm, physical-loopback, and M3a/M3b regression tests with:

```bash
make test-m4a
```

## M4b Linux network-namespace validation laboratory

M4b adds test infrastructure, not a runtime networking feature. Its privileged
runner creates uniquely named disposable Linux network namespaces and keeps all
veth links, forwarding sysctls, nftables NAT/filter rules, and conntrack state
inside those namespaces. It snapshots host links, addresses, routes, forwarding
sysctls, and normalized nftables structure before the run and compares them
after transactional cleanup. It never changes a host route, physical interface,
firewall rule, or forwarding setting.

The lab runs actual `hydrad` processes for isolated IPv4 and IPv6 `0 -> 1 -> N`
discovery and invokes the production `socketmux`, `stunprobe`, and `natprobe`
packages through machine-readable test-only helpers. Topologies cover direct
dual-stack paths, native Linux NAT44 observation, controlled mapping/filtering
policies, double NAT44, and routed two-address IPv6. The server and gateway
records form an independent oracle; expected categories are not generated by
the profiler under test.

The port invariant deliberately distinguishes the two sides of NAT:

- before NAT, Hydra packets leave the client namespace with source port
  `Mux.Port()`;
- after NAT, the responder may observe a different address and port;
- the STUN mapped endpoint and RFC 5780 observations must equal the responder's
  actual observation.

Go binaries and the test binary are built as the normal user. Only the already
built runner is executed with root/CAP_NET_ADMIN:

```bash
make test-regression
make test-m4b-build
make test-m4b
make test-m4b-soak
make clean-m4b
```

`make test-m4b` fails explicitly when passwordless `sudo`, network namespaces,
nftables, forwarding, or physical namespace IPv6 is unavailable; ordinary
`go test ./...` safely skips only the root-required cases. Each privileged run
retains bounded evidence under `/tmp/hydra-m4b-<runid>/`. Use
`scripts/netns/inspect_m4b.sh` for read-only inspection and `make clean-m4b` for
strictly prefix-limited recovery after an interrupted run. See
[`docs/testing/m4b-netns-lab.md`](docs/testing/m4b-netns-lab.md) for topology,
oracle, synchronization, and safety details. The production-boundary review is
recorded in [`docs/testing/m4b-regression-audit.md`](docs/testing/m4b-regression-audit.md).

M4b does not provide NAT traversal, ICE, hole punching, production routing, or
a production STUN service. Its verdicts remain transient observations of the
tested socket and path.

## M5a Pion ICE adapter foundation

`internal/iceadapter` implements Pion ICE v4's `ice.UDPMux` without exposing or
creating an operating-system socket. `HydraSocketMux` remains the sole owner of
one UDP4 and one IPv6-only UDP6 socket; Pion receives bounded logical
`net.PacketConn` views whose host candidates and outbound Binding Requests use
the existing `Mux.Port()`.

One ICE adapter dispatcher exclusively reads `Mux.Control()` during the ICE
phase. Binding Requests are routed by a strictly decoded USERNAME local ufrag,
including requests from previously unknown endpoints needed for future
peer-reflexive discovery. Responses require an active Transaction ID plus an
exact destination-source and address-family match. Active ufrags, outbound
transactions, retired response quarantine, and per-connection queues are all
bounded. Malformed or unknown STUN remains Data; matching retired responses are
dropped. Pion, not the classifier, remains responsible for MESSAGE-INTEGRITY
and the ICE state machine.

Hydra v1 uses phase-exclusive Control ownership: close the M3b/M4 STUN client,
then start the ICE adapter. A small immutable classifier chain supports this
handoff without two Control readers. Logical connection or Agent shutdown never
closes the mux. M6 may use `Mux.Data()` concurrently while the one ICE adapter
continues to own `Mux.Control()`.

M5a configures Pion for host candidates only, UDP4/UDP6 only, with mDNS
disabled, no STUN URLs, no Pion srflx gathering, no relay/TURN, and no TCP.
The privileged namespace test independently verifies that gathering leaves
exactly the original two physical Hydra UDP sockets and that an outbound
Pion-encoded ICE Binding Request is observed with source port `Mux.Port()`.

Run the focused acceptance and bounded soak tests with:

```bash
make test-m5a
make test-m5a-soak
```

M5a does not exchange credentials or candidates, call ICE Dial/Accept, select a
working peer pair, punch a NAT, or carry application traffic. Signaling and
two-peer negotiation are deferred to M5b/M5c; AmneziaWG/HydraBind remains
deferred to M6. See
[`docs/architecture/m5a-ice-adapter.md`](docs/architecture/m5a-ice-adapter.md).

## M5b authenticated non-trickle ICE signaling

M5b exchanges one complete ICE Offer and one complete ICE Answer over the
existing authenticated TCP-only libp2p host using
`/hydra/ice/1.0.0`. The bounded protobuf protocol carries a cryptographically
random 128-bit SessionID, authenticated initiator/responder PeerIDs, EXIT
purpose, future controlling/controlled roles, Pion-generated credentials, and
structured host/server-reflexive UDP candidates. ICE passwords are ephemeral:
they are never placed in NodeAnnouncement, Kademlia, SQLite, status output, or
normal logs.

`internal/icesession` gathers the proven M5a host candidates and converts
verified M3b XOR-MAPPED observations into detached Pion server-reflexive
candidates. Pion constructors supply foundation, priority, component, and
related-address metadata. A no-translation observation equal to its host
endpoint is suppressed rather than signaled as a duplicate. Candidate sets,
credential lengths, foundations, addresses, ports, frame sizes, concurrent
offers, per-peer sessions, accepted-session queues, and the replay quarantine
are all explicitly bounded.

The responder binds every Offer to `stream.Conn().RemotePeer()`, the local
host identity, and the addressed peer pair before authorizing or allocating a
PreparedSession. The initiator similarly binds the Answer to the authenticated
remote peer and original SessionID. Successful responder sessions are handed
off through a bounded queue; the caller then owns them. After the M5c3
ownership correction, a PreparedSession owns its Pion Agent and immutable
description, borrows the generation's shared M5a adapter, and never owns the
socket mux, ICE Control lease, or libp2p host.

Control ownership remains phase-exclusive:

```text
M3b/M4 STUN phase -> close client/release STUN lease
                   -> acquire ICE lease/gather complete description
                   -> one-shot authenticated signaling
                   -> close PreparedSession/release ICE lease
```

The privileged namespace tests exchange dual-stack descriptions over both IPv4
and IPv6 libp2p paths. A separate NAT44 test obtains a real Basic STUN mapping,
compares it with the responder oracle, and verifies that the exact mapped
endpoint is transported as srflx metadata. Neither signaling path changes the
M5a outbound ICE transaction counter.

Run:

```bash
make test-m5b
make test-m5b-netns
make test-m5b-soak
```

M5b is non-trickle and deliberately stops before
`AddRemoteCandidate`, Dial/Accept, connectivity checks, pair nomination, hole
punching, or application traffic. Those are deferred to M5c. AmneziaWG and
HydraBind remain deferred to M6. See
[`docs/architecture/m5b-ice-signaling.md`](docs/architecture/m5b-ice-signaling.md).

## M5c1 direct no-NAT ICE connectivity

M5c1 consumes the authenticated M5b handoff exactly once. A successful
Offer/Answer binds each live `PreparedSession` to its SessionID, peer pair,
EXIT purpose, and controlling/controlled roles. `internal/iceconnect` rejects
unbound, cross-session, wrong-peer, wrong-role, already-used, and closed
sessions before connectivity starts. It deep-copies and revalidates the remote
description, converts every structured HOST/SRFLX candidate with the existing
Pion codec, adds the complete non-trickle set before checks, and verifies the
Agent received the exact set.

The initiator uses blocking Pion `Dial`; the responder uses blocking `Accept`.
Both calls are context-bounded and one-shot. Success requires a non-nil,
nominated, succeeded HOST-to-HOST selected pair whose endpoints occur in the
immutable signaled descriptions. Pion pair statistics and Hydra ICE-adapter
counters must prove real STUN connectivity checks, while application packet
and byte counters remain zero. The returned `ConnectedSession` intentionally
hides `ice.Conn`: it retains its Agent only until explicit close. The
transport-generation ICE engine retains the shared adapter and ICE Control
lease. Closing one connection leaves that engine, `HydraSocketMux`, other ICE
sessions, and `Mux.Data()` alive.

Tests cover direct IPv4, direct IPv6, and dual-stack loopback plus the full
authenticated libp2p M5b chain. The privileged namespace lab independently
proves that each peer still owns only one socket per enabled family and uses
namespace nftables counters to verify that actual Binding checks leave from
`Mux.Port()`. A deterministic firewall test proves blocked UDP terminates
without a false selected pair.

Run:

```bash
make test-m5c1
make test-m5c1-netns
make test-m5c1-soak
```

M5c1 validates only direct no-NAT HOST-to-HOST ICE. NAT paths, selected srflx
and peer-reflexive candidates, and hole punching remain M5c2 work. Reconnect
and ICE restart remain deferred to M5c3. No application payload is sent over
Pion ICE; HydraBind and the AmneziaWG data plane remain deferred to M6. See
[`docs/architecture/m5c1-direct-ice.md`](docs/architecture/m5c1-direct-ice.md).

## M5c2 controlled IPv4 NAT ICE connectivity

M5c2 runs the complete real chain from M3b Basic STUN through M5b authenticated
non-trickle signaling and M5c candidate loading to blocking Pion Dial/Accept.
Controlled namespace policies prove NAT-to-public connectivity in both role
directions, simultaneous EIM+APDF NAT-to-NAT UDP hole punching, and Pion
peer-reflexive recovery when an APDM peer's STUN mapping differs from its
peer-specific mapping. A controlled APDM+APDF pair also proves bounded direct
failure with independent mapping and filter-drop evidence; Hydra does not
predict ports or fall back to relay.

The privileged lab uses separate networks. Authenticated libp2p TCP signaling
runs only on a test control bridge, while HydraSocketMux UDP, NAT, and STUN run
only on the data topology. Control addresses are rejected as ICE candidates,
the private data networks have no direct route between them, and IPv6 is
disabled for these NAT44 cases. Consequently a control or IPv6 path cannot
silently bypass the policy under test.

M3b and ICE retain the same mux and physical UDP socket across the exclusive
STUN-to-ICE phase transition. Process-owned socket-inode and socket-count
checks prove continuity and no Pion listener. Before-NAT nftables counters
require source port `Mux.Port()`; fixed SNAT counters separately prove the
external STUN and peer-specific ports. The STUN responder, gateway counters,
selected pair, and Pion remote-candidate list form independent evidence. M5b
still signals only HOST/SRFLX; PRFLX is created dynamically by Pion and retained
only in bounded M5c diagnostics.

Run:

```bash
make test-m5c2
make test-m5c2-netns
make test-m5c2-soak
```

M5c2 sends no application payload through `ice.Conn` and is not a VPN. It does
not show that all NATs are traversable. Production reachability of the libp2p
signaling channel between NATed peers is also not solved: the direct control
network is test-only. Reconnect/network-generation behavior remains M5c3;
HydraBind, AmneziaWG, routing, and the encrypted data plane remain M6. See
[`docs/architecture/m5c2-nat-ice.md`](docs/architecture/m5c2-nat-ice.md).

## M5c3 network and ICE generation lifecycle

M5c3 replaces per-session adapter ownership with one shared `iceengine.Engine`
per immutable transport generation. The engine owns exactly one ICEAdapter,
one bounded registry, one `Mux.Control()` dispatcher, and the generation's ICE
phase lease. Many Pion Agents with distinct ufrags may coexist over that one
HydraSocketMux. A PreparedSession owns only its Agent, credentials and local
description plus an engine registration; closing one peer session cannot close
the adapter, lease, mux, or another peer's Agent.

`networkstate.Monitor` uses Linux netlink route, address, and link
subscriptions as triggers, then performs a debounced authoritative rescan.
Generation numbers change only when the normalized default-route environment
changes semantically: egress interface, gateway/metric, usable egress address,
link state, or MTU. Unrelated interfaces are excluded, event bursts coalesce,
slow consumers receive the latest state, and subscription failure is surfaced.
The generation is process-local and begins at one.

Persistent ICE failure creates a new session generation with a new SessionID,
Pion Agent, ufrag, password, candidates, authenticated M5b exchange, and
connectivity checks. It reuses the same engine, mux, socket inode, port and
network generation when the network did not change. Hydra deliberately does
not call Pion `Agent.Restart`: replacement Agents preserve the existing
one-shot invariant and let old/new attempts coexist for make-before-break.
Transient Disconnected state may recover without changing any generation.
Monotonic session-generation fencing prevents an older late completion from
replacing a newer active session; retries are bounded and back off.
After a bounded configurable number of same-generation failures, the supervisor
stops retrying and emits an explicit transport-refresh request instead of
looping indefinitely while stale STUN state remains in use.

A semantic network change instead fences the old transport, builds a new mux,
runs fresh generation-bound M3b STUN, starts a new shared engine, performs a
fresh signaling/connectivity generation, promotes it atomically, and closes
the old transport. STUN observations carry their local NetworkGeneration and
mux port and cannot cross that boundary. Direct full-chain integration covers
two concrete transport addresses; the privileged lab independently validates
real IPv4/IPv6 route, address, default-interface, MTU, and link changes through
netlink, direct IPv4/IPv6 failure recovery, socket continuity, EIM+APDF NAT
reconnect, and a netlink-driven migration from an OLD NAT/STUN endpoint to a
distinct NEW NAT/STUN endpoint. Both make-before-break and old-link-down
hard-break migration are covered. A routed IPv6 test also replaces the active
global address and proves a fresh UDP6 transport and direct ICE session use the
new address; a routed IPv4 counterpart replaces the active address on the same
default-route interface and proves fresh STUN, Mux, signaling, and ICE state. A
direct dual-stack test removes the connected route for the
selected family while signaling remains on a separate control veth; Pion may
recover on another nominated pair or the supervisor creates a fresh Agent, and
the test requires the surviving family to be selected without replacing either
Hydra socket. The unprivileged full-chain migration moves two simultaneous peer
sessions together.

Run:

```bash
make test-m5c3
make test-m5c3-netns
make test-m5c3-soak
make clean-m4b
```

Go tooling remains unprivileged. Only the prebuilt namespace runner executes
with root/CAP_NET_ADMIN, and it preserves host links, addresses, routes,
nftables and forwarding sysctls. M5c3 still uses a test-only reachable libp2p
control network where required; production signaling mobility remains open.
The phase-exclusive design also means a fresh STUN observation requires
closing the generation's ICE engine, rather than probing concurrently.

M5c3 sends no application bytes through `ice.Conn` and adds no TURN, relay,
TCP ICE, trickle ICE, continual gathering, renomination, TUN, routing,
AmneziaWG, or hydrad connect command. See
[`docs/architecture/m5c3-network-lifecycle.md`](docs/architecture/m5c3-network-lifecycle.md).

## M6 AmneziaWG encrypted Data Plane

`internal/awg` embeds AmneziaWG and implements its `conn.Bind` interface over
the existing `HydraSocketMux.Data()` endpoint. `HydraBind.Open` creates no
socket, reports `Mux.Port()`, receives copied Data datagrams, and sends ordered
batches through the mux-owned UDP4/UDP6 sockets. Closing an AWG peer or device
does not close the mux or the shared ICE engine. M7 extends `SetMark` to the
actual mux-owned UDP descriptors; M6 itself does not install policy routing.

The AWG destination is copied from Pion's selected candidate pair and fenced by
remote PeerID, NetworkGeneration, and SessionGeneration. It is never inferred
from an announcement or stale signaled SRFLX record. The privileged PRFLX case
proves AWG uses the dynamically learned peer-specific NAT port when it differs
from the original STUN endpoint. Same-network reconnect updates only the
affected peer after a fresh M5b/M5c ICE generation; transport migration creates
a new generation-owned mux, STUN observation, ICE engine, and AWG data plane.

Real direct UDP4, UDP6, dual-stack, public-to-NAT, NAT-to-NAT hole-punch, and
PRFLX namespace tests perform an AWG Noise handshake and exchange inner IPv4
and IPv6 packets bidirectionally. `/proc/net/udp*` inode snapshots and scoped
nftables counters independently prove STUN, ICE, AWG handshake, and encrypted
transport retain the Hydra socket and NAT path. Three clients share one Exit
AWG device/bind/mux with distinct AllowedIPs; restarting or removing one peer
does not affect the other two. ICE remains active during at least one AWG proof,
while Pion `ice.Conn` application counters remain zero.

Run:

```bash
make test-m6
make test-m6-netns
make test-m6-soak
make clean-m4b
```

Go builds remain unprivileged; only the prebuilt namespace runner uses
root/CAP_NET_ADMIN. M6 is an encrypted peer Data Plane proof, not an Internet
VPN. See [`docs/architecture/m6-awg-dataplane.md`](docs/architecture/m6-awg-dataplane.md).

## M7 Linux Internet Exit

M7 adds a real long-lived Linux `hydra0`, authenticated bounded TunnelLease
exchange, netlink policy routing, real `SO_MARK` delegation through
HydraSocketMux, namespace-scoped forwarding, and an atomic nftables Exit policy
with NAT44/NAT66. AmneziaWG borrows a duplicate TUN descriptor so closing or
replacing a generation-local AWG device cannot destroy the logical tunnel.

Privileged tests carry real HTTP, UDP echo, DNS/UDP, and DNS/TCP traffic through
AWG to a deterministic Internet namespace. Remote servers independently see
the Exit WAN source after NAT; nftables counters prove protected packets did
not bypass `hydra0`, all Hydra outer UDP was marked, Exit-local destinations
were blocked, and an actual `hydra0 -> hydra0` forwarding attempt was dropped.
Outer UDP4 with inner IPv6 and outer UDP6 with inner IPv4 are covered. An
unsupported inner family gets an explicit unreachable route.

Three authenticated clients also share one Exit Mux, ICEEngine, real TUN, and
AWG transport with distinct inner routes. Releasing one client leaves new
Internet flows for the other clients operational.

Run:

```bash
make test-m7
make test-m7-netns
make test-m7-soak
```

M7 proves the healthy encrypted Internet path. Abrupt-failure zero-leakage and
the final kill switch remain M8. See
[`docs/architecture/m7-internet-exit.md`](docs/architecture/m7-internet-exit.md).

## M8 fail-closed Protected Mode

M8 adds an explicit generation-fenced `ProtectedMode` controller and a
persistent, atomically managed nftables OUTPUT boundary. Ordinary IPv4 and
IPv6 may leave only through `hydra0`; traffic using the exact Hydra bypass mark
retains physical-WAN access. There are no destination or DNS allowlists.
HydraSocketMux keeps its existing real `SO_MARK`, and outbound libp2p TCP uses
the pinned transport's custom-dialer hook to mark sockets before connect.

The policy survives an unexpected service death and is adopted or reconciled
on restart using a versioned ownership marker. Explicit Disconnect and clean
shutdown remove it only after tunnel/routing teardown. Privileged tests cover
healthy encrypted traffic, connecting/no-Exit state, IPv4/IPv6 TCP, UDP, ICMP
and DNS blocking, literal-IP libp2p and Mux recovery, same-peer reconnect,
NetworkGeneration migration, dual-WAN protection, continuous Exit-death leak
probes, unauthorized mark attempts, crash adoption, and WAN restoration.
The real `hydrad --protected-mode` startup path installs or adopts protection
before constructing its libp2p node and removes it on graceful service shutdown.

Run:

```bash
make test-m8
make test-m8-netns
make test-m8-soak
```

See [`docs/architecture/m8-killswitch.md`](docs/architecture/m8-killswitch.md).

## M9 Exit selection and failover

M9 adds a replaceable bounded `CandidateProvider`, provider-side random
Power-of-K sampling (`K <= 10`), a marked literal-IP `CandidateProbe`, and a
generation-fenced selector with one sticky ACTIVE plus metadata-only RESERVE
and COLD candidates. Probe fan-out is limited to five; bandwidth is a hard
threshold and latency uses tolerance buckets rather than a weighted score.

The selector derives physical availability from M5c3 `networkstate`. Loss of
the local WAN enters `WAITING_FOR_UNDERLAY`: it pauses the ACTIVE peer's
two-second usable-underlay recovery budget and consumes neither fallback peers
nor provider requests. On return it tries the same Exit first. Genuine
same-peer failure advances through fast RESERVE/COLD attempts and immediately
requests a fresh bounded batch when exhausted. A healthy ACTIVE is never
replaced by background refresh, and provider failure is independent from a
working Data Plane. M8 ProtectedMode must already be active and remains the
zero-fallback authority in every M9 state.

Privileged acceptance composes the selector with real authenticated
signaling, ICE, AWG, `hydra0`, TunnelLease, Exit NAT and deterministic IPv4/
IPv6 Internet servers. It proves sticky transient recovery, A-to-B failover,
dead-RESERVE-to-COLD fallback, immediate fresh-batch recovery, provider
outage independence, and an underlay outage longer than the peer-recovery
budget while M8 observes zero direct-WAN leakage.

Run:

```bash
make test-m9
make test-m9-netns
make test-m9-soak
```

See [`docs/architecture/m9-exit-reselection.md`](docs/architecture/m9-exit-reselection.md).

## M10 production runtime orchestration

M10 makes `internal/runtime.Daemon` the real owner of the production lifecycle.
`cmd/irviod --runtime` now composes the persistent Hydra and independent AWG
identities, SQLite PeerDirectory, marked libp2p/Kademlia control plane,
NetworkMonitor, marked underlay oracle, M8 protection, M9 selection,
TransportGeneration, authenticated M5b/ICE, selected-pair AWG, session-bound
TunnelLease, `hydra0`, routing and Exit forwarding. The CLI remains a thin
configuration and signal-handling layer and exposes bounded redacted lifecycle
events for future IPC/UI work.

Linux clients may configure up to eight literal-IP tunnel resolvers with
repeatable `--tunnel-dns` flags. The production runtime applies them to
`hydra0` through systemd-resolved only after AWG/routes are promoted, routes
their queries through the protected tunnel, and reverts the per-link policy
before teardown. Resolver failure prevents a false `CONNECTED` state; omitting
the flag leaves host DNS configuration unchanged.

The interim production CandidateProvider maps CountryIntent to the existing DHT
service CID and emits at most ten directly authenticated signed announcements.
It is intentionally replaceable by future Rendezvous. The current production
Authorizer accepts only authenticated, protocol-compatible peers requesting
bounded EXIT resources.

TunnelLease now has explicit allocation, activation, renewal, revocation,
quarantine and safe-free states. Renewal keeps the logical lease and addresses
while advancing its generation. Expiry, release, supersession and shutdown use
one revocation path; an address is released only after its AWG/ICE ownership is
removed, and failed cleanup quarantines it. The client daemon renews leases
before expiry with bounded retry.

The persistent AWG key is separate from the Hydra Ed25519 identity. Its public
key and monotonic epoch are authenticated inside the signed NodeAnnouncement,
persisted with rollback protection, refreshed before reconnect, and checked
again at Data Plane promotion. `--rotate-awg-key` performs an explicit
persistent rotation before publishing a new signed binding; existing sessions
are never mutated in place.

Underlay state is no longer inferred solely from a configured route. The
bounded per-family oracle challenges up to three literal-IP Hydra targets using
the existing bypass mark and either authenticated libp2p request/response or
the small marked TCP challenge. UNKNOWN, REACHABLE and UNREACHABLE remain
distinct, and no DNS or destination allowlist is introduced.
An ACTIVE failure forces an immediate marked re-evaluation. The production
netns test keeps route/link/address state valid while blackholing marked WAN
traffic, observes UNREACHABLE without consuming RESERVE, then restores the same
Exit before testing genuine Exit failover.

Run:

```bash
make test-m10
make test-m10-netns
make test-m10-soak
```

The privileged acceptance launches real `hydrad` bootstrap, Exit and Client
processes; the test runner supplies namespaces and independent Internet
oracles but does not assemble production components. It also changes the
Client's active outer address and verifies that the daemon creates a fresh
TransportGeneration and ICE session while preserving the same TunnelLease and
kernel `hydra0`. A live predecessor may retain the preferred UDP port, so the
replacement generation is permitted to use a new internally consistent mux
port during make-before-break.

The same real-process test kills the connected Client without cleanup. M8
policy remains fail-closed, the restarted daemon adopts it, reconciles only
Hydra-owned route residue, preserves the persistent PeerID, and restores
Internet. A later abandoned three-second lease is revoked before its sole
`/30` address is reused by a different authenticated client.

The production failover acceptance discovers two real Exit daemons through
Kademlia, kills ACTIVE, promotes cached RESERVE, proves the Internet-observed
NAT source changes, and records zero direct-WAN packets from a continuous
unmarked leak probe.

M10 still does not claim
Rendezvous/live presence, public inbound dual-role operation, privilege
separation, shared-engine background STUN refresh, GUI, Windows, or token
policy. See
[`docs/architecture/m10-runtime-orchestrator.md`](docs/architecture/m10-runtime-orchestrator.md).

## M11 public-node foundation

M11 proves a real `hydrad` can simultaneously remain a protected Client, serve
downstream Exit leases, and expose independently verified public libp2p control.
Its local application traffic follows its upstream Exit, while forwarded
downstream traffic is pinned to the physical WAN; full-process packet and
source-address oracles reject implicit downstream multi-hop. Accepted public
control replies use the bounded Hydra service ownership mechanism without
granting ordinary applications or all root traffic WAN bypass.

Public capability is separate from operator intent. Fresh inbound authenticated
connections from a configurable verifier quorum attest the externally observed
literal-IP endpoint, including static PAT. Proof expiry, endpoint change, or
NetworkGeneration change removes the advertised public capability. TCP public
control verification does not claim public UDP/STUN capability.

See [`docs/architecture/m11-public-node.md`](docs/architecture/m11-public-node.md).

## M12 Rendezvous v1 and direct maintenance

M12 uses verified public nodes as bounded Rendezvous services. Exits keep at
most three sticky authenticated registrations; each server owns only its local
RAM presence and country index. Clients discover literal-IP services through
Kademlia hints, acquire a progressive five-to-ten candidate sample, and feed
the existing M9 selector. `SAMPLE_LOCAL` is non-recursive, broker fan-out and
frames are bounded, descriptors remain the original signed announcement bytes,
and local MMDB country observations never promote claimed or unknown country.

Rendezvous forwards only the end-to-end signed PREPARE/READY/OFFER/ANSWER
transcript. ICE and AWG remain direct and use the exact selected HydraSocketMux
path. After authenticated TUNNEL_ONLY TCP+Noise BIND, the M10 runtime owns the
lease; renewal, rebind, state, release, and the supported single-relationship
planned AWG rotation operate inside AWG without a broker. TUNNEL_ONLY routes
are exact and source-explicit, with family fail-closed guards. Planned rotation
uses an old-key READY/COMMIT barrier before a fresh new-key handshake transfers
AllowedIPs, so neither an in-flight control frame nor lease ownership can race
the key change. Losing every Rendezvous therefore does not terminate a healthy
Internet path.

Run:

```bash
make test-m12
make test-m12-netns
make test-m12-soak
make test-m12-scale
make demo-m12
```

See [`docs/architecture/m12-rendezvous.md`](docs/architecture/m12-rendezvous.md),
[`docs/architecture/m12-session-control.md`](docs/architecture/m12-session-control.md),
and [`docs/testing/m12-acceptance.md`](docs/testing/m12-acceptance.md).

## Deliberately outside the current scope

Global registry/consensus/CRDT presence, globally uniform sampling,
failure-domain/Sybil diversity, global reputation, automatic country fallback,
two active Exit tunnels, TURN/user relay, public UDP/STUN service, centralized
telemetry, privilege-separation redesign, broad Data Plane fairness/DoS work,
tokens/Solana, mobile/Windows support, GUI, and general HTTP/gRPC control are
not implemented. Hydra bootstrap/control remains literal-IP based. Rendezvous
v1 is a bounded introducer and signaling broker, not a trusted global database
or user-traffic relay.
