# M9 Exit Selection and Underlay-Aware Failover

M9 adds the client-side policy that chooses one Exit for an explicit country
and replaces it after a genuine remote failure. It composes with M8 rather
than controlling it: selection starts only after `ProtectedMode` has been
committed, and no selector state can disable the fail-closed policy.

## Boundaries

`CandidateProvider` is a replaceable source boundary. Its implementation may
later use Rendezvous, DHT, cache, or several services, but the selector sees
only a bounded batch. `SamplingProvider` is the current provider-side
reference: it applies hard eligibility filters, deduplicates identities by
libp2p PeerID, excludes the local peer, and samples without replacement using
cryptographic randomness. It returns `K = min(10, eligible count, requested
limit)` and never globally quality-ranks a country pool.

A candidate carries a PeerID, country, literal control endpoint, Hydra
protocol/capability fields, accepting state, coarse available-bandwidth and
uptime hints, a path `NetworkEpoch`, and bounded generation/expiry times. The
PeerID is identity; an IP address and port are transient provider metadata.
Provider hints are not liveness proof.

Production Rendezvous registration, heartbeat/presence synchronization,
replication, country sharding, public reachability verification, Sybil
defense, and global reputation are outside M9. A future production provider
should diversify random batches across infrastructure failure domains, but
the selector deliberately has no ASN, prefix, or hosting-provider policy.

## Candidate probing and ranking

`CandidateProbe` is small, bounded, non-tunneling, and non-reserving. The Linux
`MarkedTCPProbe` accepts only a literal IP endpoint and creates its socket with
the existing `controlsocket.Control` hook and
`HYDRA_BYPASS_MARK=0x48594452`. It performs only a TCP establishment and close;
it neither depends on ICMP nor accepts an amplified response. There is no DNS
resolution in this path.

Batch evaluation defensively validates the provider response again. It rejects
expired, wrong-country, non-Exit, incompatible, non-accepting, local,
insufficient-bandwidth, and same-epoch locally suppressed candidates. Probe
fan-out is a worker pool, with a default and hard maximum of five. Each probe
has its own 750 ms context. Canceling selection, changing country or network
generation, losing the underlay, or shutting down cancels obsolete work.

Bandwidth is a sufficient/insufficient threshold, not a score. Latency uses a
5 ms tolerance bucket because tiny RTT differences are noise. Within one
bucket a mature candidate precedes a new candidate, then PeerID supplies a
stable tie break. Only three distinct metadata records survive:

```text
ACTIVE  real complete Hydra relationship
RESERVE metadata + recent bounded probe only
COLD    metadata + recent bounded probe only
```

The `Connector` boundary may report connected only after authentication, ICE,
AWG, TunnelLease, M7 Internet validation, and routing are all ready. Probe
success alone never promotes ACTIVE. RESERVE and COLD own no ICE Agent, AWG
peer, lease, or capacity reservation.

## Sticky ACTIVE and failure policy

A healthy ACTIVE is sticky. A background batch with a lower-latency peer may
replace RESERVE or COLD but never the healthy ACTIVE or its public Exit IP.
Normal refresh is independently randomized over 45–75 seconds. Provider
failure only ages fallback metadata; it does not touch a working Data Plane.

An established ACTIVE receives one total recovery budget of at most two
seconds. Recovery starts immediately; the budget is not a sleep. Natural Pion
recovery and a fresh same-peer SessionGeneration share that authority through
the injected Connector. A monotonic selector lifecycle generation fences a
completion that arrives after the budget, a newer failover, country change,
NetworkGeneration, or shutdown.

After recovery exhaustion, RESERVE and then COLD receive independent fast
connection attempts. The default fast deadline is 1.5 seconds and is strictly
less than the ACTIVE recovery budget. BUSY advances immediately without hard
failure suppression. A genuine unavailable/authentication/incompatibility or
connection failure adds a bounded, local, 60-second `avoid_until` entry. The
entry is path-specific: a different `NetworkEpoch` is eligible. RESERVE and
COLD never receive another two-second same-peer recovery window.

Full-stack repetition exposed the pinned AWG implementation's five-second
default retransmission after a lost first handshake initiation. That exceeds
the fast-candidate deadline. Hydra now configures the upstream-supported
`rekey_timeout=1` before `Device.Up`; the timeout remains an explicit bounded
AWG transport setting and no custom handshake or retry algorithm is added.

When all three are exhausted, a fresh bounded provider request starts
immediately rather than waiting for the periodic timer. A successful failover
also refreshes RESERVE/COLD immediately. Country intent never falls back
silently: no usable DE peer means `NO_EXIT` with country DE unchanged and M8
still dropping user traffic.

## Underlay-aware recovery

`UnderlayFromNetworkState` derives IPv4/IPv6 physical availability from the
authoritative M5c3 NetworkGeneration snapshot: a usable default route must
reference an up link with a usable address of that family. M9 has no second
link detector.

With no usable family the selector enters `WAITING_FOR_UNDERLAY`. It cancels
network work, retains ACTIVE/RESERVE/COLD, performs no provider request, and
creates no peer suppression. Time in this state does not consume the ACTIVE
budget. When an underlay returns, the same logical ACTIVE is always attempted
first, using the new TransportGeneration when NetworkGeneration changed. Only
failure during usable-underlay time permits RESERVE consumption. Repeated
up/down events are fenced and do not burn candidates or flood the provider.

Throughout selection, recovery, failover, and `NO_EXIT`, M8 remains ON:

```text
ordinary application traffic -> Hydra or DROP
Hydra provider/probe/control   -> marked physical WAN
```

## Abandoned leases

Failover correctness does not depend on a graceful `ReleaseTunnelLease`.
M7's Exit-local allocator keeps one authenticated-client lease, expires it at
its configurable bounded `valid_until`, frees addresses during authoritative
allocation/inspection, and permits the same authenticated PeerID to acquire a
fresh lease afterward. Before expiry, a reconnect by the same authenticated
client receives the existing logical lease rather than BUSY. This is separate
from client-side `avoid_until`.

## Lifecycle and observability

The selector exposes typed states and bounded typed events for selection,
probe work, promotion, disconnection, underlay loss/return, same-peer recovery,
fallback, provider failure/recovery, and closure. Statistics are race-safe and
bounded; they include provider/batch counts, live maximum probe fan-out,
connect outcomes, failovers, stale results, and P50/P95 probe and connection
latencies. They contain no ICE passwords, AWG private keys, or control secrets.

Shutdown cancels provider, probe, refresh and connect work, closes candidate
connections that complete late, closes ACTIVE, and makes CLOSED terminal.

## Full-stack acceptance

The privileged M9 laboratory keeps one M8 protection policy active while
real M5b signaling, Pion ICE, AmneziaWG, TunnelLease, `hydra0`, policy
routing, Exit forwarding, NAT44/NAT66, and deterministic Internet servers are
composed underneath the selector. It verifies all of the following without
turning a probe into an ACTIVE tunnel:

- a real A connection is promoted only after IPv4 and IPv6 Internet traffic
  is observed with A's Exit source address;
- persistent A failure receives one two-second recovery budget, then a real B
  connection changes both independently observed Internet source addresses;
- continuous unmarked IPv4 and IPv6 physical-WAN probes remain at zero while
  A recovery expires and B is selected;
- an unavailable metadata-only B advances immediately to real C without a
  second recovery window, and exhausting the retained set triggers an
  immediate fresh batch whose real D path reaches the Internet;
- a physical link outage longer than two wall-clock seconds consumes no
  fallback candidate or provider request, and the same real A path is used
  first after the link returns;
- a transient path fault recovers A inside the budget, and a provider outage
  does not disturb A's working encrypted Data Plane.

The laboratory's provider/probe fixtures remain replaceable M9 boundaries;
the promoted connections themselves use the complete existing Hydra stack.
The separate marked-probe test independently proves literal IPv4/IPv6 probe
access while ordinary sockets to the same targets are blocked by M8.

## Deferred work

M9 does not implement production Rendezvous, distributed presence, country
pool distribution, server verification, diversity/Sybil policy, two active
Exit tunnels, automatic country fallback, GUI, telemetry, or token economics.
Those policies can replace CandidateProvider or sit above CountryIntent
without changing the selector's fail-closed and underlay-aware invariants.
