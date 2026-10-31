# M10 Production Runtime Orchestrator

M10 closes the boundary between the individually proven M0–M9 components and
the long-lived `hydrad` process. `internal/runtime.Daemon` is the process owner;
`cmd/irviod` parses bounded configuration, creates one daemon, invokes its
lifecycle API, reports redacted events, and handles process signals. Tests no
longer need to call the production constructors in the correct order on behalf
of the daemon.

M10 does not add a new network protocol family. Rendezvous, public inbound
Client+Exit operation, privilege separation, TURN/relay, ControlRouter, GUI,
and token policy remain outside this milestone.

## Ownership hierarchy

One daemon owns resources in this order:

```text
Daemon
  persistent Hydra Ed25519 identity and PeerDirectory
  persistent independent AWG identity
  libp2p Host and Kademlia DHT
  NetworkMonitor
  marked UnderlayReachabilityOracle
  M8 ProtectedMode controller
  serialized Runtime coordinator
    M9 ExitSelector
    TransportOwner
      current and preparing TransportGeneration
        HydraSocketMux
        PhaseManager
        shared ICEEngine
    ClientDataPlane
      logical hydra0 / AWG / routes
      optional per-link systemd-resolved DNS owner
    TunnelLease Renewer
```

An Exit daemon replaces the client selector and Data Plane with one
`ExitServer`. The Exit server owns its lease lifecycle, long-lived `hydra0`,
forwarding/firewall installation, authenticated ICE and lease services, and a
generation-scoped shared AWG transport. Individual leases own only their AWG
peer, AllowedIPs and ICE session. One lease revocation cannot close unrelated
peers, the ICE engine, or the mux.

The transport owner deliberately keeps the last committed generation alive
while a replacement is prepared. Only a successful Data Plane promotion
commits the replacement and closes its predecessor. A stale commit token is
rejected.

## Runtime state and serialization

The externally visible state is bounded:

```text
STOPPED -> STARTING -> IDLE
IDLE -> PROTECTING -> DISCOVERING -> CONNECTING -> CONNECTED
CONNECTED -> SAME_PEER_RECOVERY | NETWORK_MIGRATION | EXIT_RESELECTION
any protected connection state -> NO_EXIT
protected state -> DISCONNECTING -> IDLE
any state -> STOPPING -> CLOSED
```

One coordinator event loop is the authority for promotion and demotion. A
monotonic runtime generation fences Connect, Disconnect, protection setup,
selector work and late asynchronous results. Network and transport work carry
separate monotonic sequences. M9 continues to fence candidate, session and
ACTIVE promotion generations.

The connector stages the real client Data Plane after authenticated signaling,
ICE, selected-pair conversion, AWG binding verification and lease allocation.
It does not install AWG or routes yet. The M9 selector calls `Promote` only from
its serialized event loop after accepting the connection result. This prevents
a stale connection goroutine from mutating routing or the current AWG peer.

`Status` and bounded events expose only redacted state: country, ACTIVE/RESERVE/
COLD PeerIDs, Network/Transport/Session generations, lease ID and expiry, AWG
key epoch, selected public endpoint, mux port, underlay evidence, ProtectedMode
and a typed transition reason. ICE passwords and private keys are never
included.

## Startup, Connect, Disconnect and shutdown

Production client startup is:

1. Load/create the persistent AWG key independently of the Hydra Ed25519 key.
2. Start the persistent Hydra identity, SQLite directory, marked TCP-only
   libp2p host and Kademlia DHT.
3. Publish the signed NodeAnnouncement containing the current AWG public key
   and key epoch.
4. Start the authoritative netlink NetworkMonitor.
5. Reconcile the M8 protection backend.
6. Create the TransportGeneration builder and marked underlay oracle.
7. On `Connect(country)`, install and commit ProtectedMode before any provider
   or candidate work.
8. Discover and authenticate candidates, then let M9 select and connect.
9. Build/reuse a generation-bound mux, STUN observation and shared ICE engine.
10. Complete M5b signaling and Pion ICE, verify the selected pair, verify the
    signed AWG binding, obtain a session-bound lease, establish AWG, install
    `hydra0` routing, apply configured tunnel DNS, and only then publish
    CONNECTED.

On Linux, each repeatable `--tunnel-dns <literal-ip>` enables a bounded
systemd-resolved adapter. After AWG and user routes are installed, the adapter
sets the servers on `hydra0`, assigns the route-only domain `~.`, and marks the
link as the DNS default route. Only resolver addresses matching an address
family present in the current TunnelLease are installed. DNS packets are
ordinary unmarked traffic and therefore follow `hydra_out` through `hydra0`;
the adapter does not add a physical-WAN exception. Failure to apply the policy
fails session promotion rather than publishing a connection with unusable DNS.
If no `--tunnel-dns` is configured, Hydra retains the existing host-resolver
policy.

The per-link owner is session-token fenced. Promotion on the same kernel TUN
transfers ownership without reconfiguring the resolver, so a late close from
the previous session cannot revert the current state. A recreated TUN is
detected by ifindex and receives a fresh configuration. Disconnect and daemon
shutdown run `resolvectl revert hydra0` before route/TUN teardown; a partial
configuration failure also performs bounded rollback. Enabling this adapter
requires `/usr/bin/resolvectl` and a running systemd-resolved service.

Disconnect first advances the runtime generation and closes the selector. That
closes renewal, lease, optional per-link DNS, Data Plane and ICE ownership. The
kill switch is removed last. If Data Plane cleanup fails, the runtime returns
an error and remains in a protected `NO_EXIT` state instead of exposing the
physical WAN.

Clean daemon shutdown performs the same safe Data Plane closure, then closes
the underlay oracle, transport, NetworkMonitor, protection controller,
challenge service, DHT/libp2p host and PeerDirectory. Child errors are joined
and surfaced. A failed protected teardown does not claim that ordinary WAN was
restored. M8 kernel ownership markers still provide crash adoption after an
unexpected process death; in-memory sessions are never assumed to survive.

Client policy routes and rules use the reserved Hydra tables/priorities plus
route protocol `0x48`. A namespace-local abstract Unix socket is the live
single-owner lock. The kernel releases that lock on process death while routes
may remain; a successor can therefore reconcile only protocol-marked Hydra
residue before reinstalling current routes. A second live owner and any
foreign entry in a reserved table or priority remain hard conflicts. M8
protection stays installed throughout reconciliation.

Production Exits defer `CAP_EXIT` and their DHT provider record until the
transport, ICE, AWG, TunnelLease and signaling services are ready. Readiness is
then published as a new signed announcement sequence before provider
advertisement. A stale provider record from an earlier process therefore
cannot expose a newly restarted Exit during partial initialization.

## Interim production CandidateProvider and authorizer

`DiscoveryCandidateProvider` is the bounded pre-Rendezvous adapter:

```text
CountryIntent
  -> ServiceCID("hydra/v1/exit/<country>")
  -> Kademlia FindProviders
  -> direct signed NodeAnnouncement verification
  -> PeerDirectory
  -> random reservoir sample, at most ten
```

A DHT provider record is only a hint. Candidates are emitted only after direct
PeerID/signer/announcement verification, current protocol/capability checks,
expiry validation and a literal-IP TCP endpoint. Sampling is local to the
bounded result and does not introduce global quality ranking. M9 remains
unchanged and a future Rendezvous provider can replace this adapter.

`CoreAuthorizer` accepts only an authenticated compatible Hydra peer for the
EXIT purpose when local bounded capacity is available. It refreshes and
verifies the remote signed announcement before granting ICE resources. It does
not implement payment, reputation, Rendezvous trust, or token entitlement.

## TunnelLease lifecycle

The Exit uses one `tunnellease.Lifecycle` and one expiry worker. Production
allocation requires the immutable ICE SessionID-derived session token. Lease
messages are bounded authenticated libp2p requests for allocate, renew and
release; the observed stream PeerID must match the encoded owner.

```text
ALLOCATED -> ACTIVE -> RENEWING -> ACTIVE
ACTIVE -> REVOKING -> REVOKED -> FREE
                    -> QUARANTINED -> retry revocation
```

A renewal retains lease ID and addresses but advances its generation and
validity. Replayed generations, wrong PeerIDs, wrong session tokens, malformed
expiries and renewals after revocation are rejected. The client uses one
bounded renewer with configurable lead, retry and request timeout. Renewal is
scheduled early with cryptographic jitter bounded to one quarter of the lead
interval; short configured TTLs scale safe lead/retry defaults instead of
inheriting production-duration constants. Successful renewal publishes
updated redacted lease expiry without recreating the logical tunnel.

Every expiry, explicit release, supersession, session loss and shutdown enters
the same revocation operation. The entry is fenced first; the Exit removes the
AWG peer/AllowedIPs and closes the lease ICE resource before the allocator is
allowed to release the addresses. Cleanup failure quarantines the address and
uses bounded retry. A quarantined address is never reallocated. A newer
session token from the same authenticated PeerID revokes the old resources
before advancing ownership of the same address set. Unrelated leases remain
active.

## Hydra PeerID to AWG key binding

The AWG private key is a persistent 0600 local identity file and is never
derived from the Hydra Ed25519 key. Its public key and monotonic key epoch are
inside the existing signed NodeAnnouncement. Discovery verifies and persists
the binding with the announcement epoch and sequence. PeerDirectory rejects a
lower key epoch or a different key at the same epoch.

Before every connection, `BindingResolver` refreshes the directly authenticated
announcement when possible, then commits the binding to a process-local
registry. Refresh failure may use a still-valid cached signed binding, but a
registry or persistent rollback conflict remains fatal. The exact key/epoch is
copied into the immutable session. Immediately before Data Plane promotion the
registry is checked again, so a late epoch-7 result cannot win after epoch 8
was committed.

M9 path suppression remains keyed by the signed process `SessionEpoch`, not by
the periodically increasing announcement sequence. A failed old path stays
suppressed, while the same Hydra PeerID from a new authenticated process epoch
can be selected and can establish the replacement AWG key epoch.

`--rotate-awg-key` is the bounded production rotation hook. It atomically
persists a fresh local key at the next epoch before startup and publishes the
new signed binding while preserving the Hydra PeerID. Existing processes do
not mutate an already-connected AWG peer in place; replacement sessions use
the refreshed binding. NetworkGeneration changes alone never rotate AWG
identity.

## Underlay reachability

Netlink and reachability remain different truths:

```text
NetworkMonitor: routes, addresses, link, gateway and MTU
Underlay oracle: marked Hydra request/response can actually traverse the WAN
```

The oracle reports UNKNOWN, REACHABLE or UNREACHABLE independently for IPv4
and IPv6. No usable default-route snapshot is immediately unreachable. A
configured route with no evidence is unknown. A recent successful marked
challenge is reachable; bounded failures across available targets make that
family unreachable. No trustworthy target is UNKNOWN, not a false hard
failure.

Targets are bounded to three per family and come from configured literal
endpoints or recently verified signed Hydra announcements in PeerDirectory.
The production peer challenge uses authenticated libp2p streams over the same
SO_MARK-enabled host as all other Hydra TCP control traffic. The request and
response are fixed-size, nonce-matched and non-amplifying. Raw literal test or
infrastructure targets use the existing marked TCP challenge. Neither path
uses DNS or creates a kill-switch exception. One live target is sufficient, so
a dead Exit does not by itself classify the physical WAN as dead.

M9 consumes this evidence: definite UNREACHABLE pauses same-peer recovery
budget, REACHABLE permits progress, and UNKNOWN is conservatively bounded by
the authoritative configured network state rather than waiting forever.
An ACTIVE disconnect triggers an immediate marked evaluation rather than
waiting for the periodic oracle tick. One production evaluation already
challenges up to three independent targets, so one all-target failure round is
sufficient to publish UNREACHABLE; a successful target still wins the round.

## Network migration and STUN refresh

A semantic NetworkGeneration change starts a new TransportGeneration with a
new mux, fresh generation-bound STUN observations and a new shared ICE engine.
The selector sees the new underlay generation; stale probe, signaling, ICE,
AWG and route results cannot promote. The old generation is retained until a
new Data Plane promotion commits the handover.

During make-before-break, the predecessor may still own the configured UDP
port on an unchanged address family. The replacement first tries that preferred
port and, only when a live predecessor makes it unavailable, falls back to a
kernel-selected port shared by the replacement's enabled families. Initial
startup never silently abandons the configured port. STUN and ICE within each
generation continue to use that generation's one immutable mux port.

The phase-exclusive Control design remains deliberate. STUN cannot run in the
background while the shared ICE engine owns `Mux.Control()`. A requested STUN
refresh therefore requires safe TransportGeneration regeneration. M10 handles
normal NetworkGeneration replacement; it does not claim an independently
refreshable shared multi-client transport or add a ControlRouter.

## Acceptance boundary

The privileged M10 acceptance launches the actual prebuilt `hydrad` binary for
bootstrap, Exit and Client. The runner creates namespaces and deterministic
Internet services, supplies literal configuration, injects faults and observes
kernel state; it does not construct internal production components. The client
daemon performs DHT discovery, authenticated binding and signaling, ICE, AWG,
lease, `hydra0`, routing and protection itself. The Internet server observes
the Exit NAT source. Runtime status reports the selected endpoint, mux port,
lease and generations, and graceful shutdown removes both TUNs and Hydra-owned
policy.

The process acceptance also runs with a three-second lease across multiple
production renewals, then restarts the Exit with the same persistent Hydra
identity and a rotated persistent AWG key. The client accepts only the newer
signed epoch, establishes a fresh ICE/AWG session and reaches Internet again.
It then replaces the Client's active IPv4 address through real Netlink state,
observes a higher NetworkGeneration, reconnects through a replacement mux and
fresh ICE SessionGeneration, and proves that the TunnelLease, AWG key epoch and
kernel `hydra0` ifindex remain unchanged.

The same process scenario then sends `SIGKILL` to the connected Client.
The kernel protection table remains and an unmarked Internet probe is blocked.
A successor with the same data directory retains the Hydra PeerID, explicitly
reports adopted protection, reconciles crash-resident Hydra routing state, and
re-establishes Internet connectivity. A second abrupt death omits lease
release; after the advertised three-second validity and expiry sweep, a
different authenticated PeerID obtains the sole address in a `/30` pool and
reaches Internet. The lease ID changes, proving safe reuse after revocation
rather than ownership aliasing.

A second process acceptance obtains two independently authenticated Exits from
the real DHT provider, kills the selected ACTIVE daemon, and lets M9 promote
the cached RESERVE without test-side lifecycle calls. The deterministic
Internet server observes the NAT source change. A concurrent physical-device
probe attempts 64 unmarked packets across the failure boundary and observes
zero direct-WAN delivery while ProtectedMode remains active.
Before killing ACTIVE, that scenario keeps Netlink route/link/address state
unchanged while nftables blackholes only Hydra-marked WAN packets. The marked
oracle publishes UNREACHABLE without consuming ACTIVE or RESERVE. Removing the
blackhole restores marked evidence and the same Exit plus encrypted Internet
path before the later genuine Exit failure is allowed to promote RESERVE.

M10 does not claim production Rendezvous/presence, public inbound dual-role

bypass, privilege separation, an external kill-switch watchdog, Data Plane
fairness/DoS hardening, full PeerDirectory GC, GUI/Windows, or token economics.
