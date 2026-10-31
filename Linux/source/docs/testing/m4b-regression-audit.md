# M0–M4a regression audit

This audit records the M4b review boundary. It supplements automated tests; it
does not prove the absence of every defect. Production packages were not
changed because the review found no demonstrated production invariant failure.

## Findings

| Area | Reviewed invariant | Finding |
|---|---|---|
| Identity | Ed25519 generation, atomic non-replacing persistence, `0600`, stable PeerID, explicit corruption failure | Preserved; unit and repeated race tests cover reload and corruption. |
| SQLite | Transactional migrations and peer/announcement update, WAL, foreign keys, busy timeout, parameterized SQL, stale sequence rejection, close | Preserved; signed envelope bytes are retained and concurrent access is covered. |
| Announcement | Bounded payload/frame/address fields, signature before trust, signer/provider/NodeID equality, expiration, unknown capability bits | Preserved; deterministic tests plus a bounded fuzz corpus exercise malformed envelopes. |
| Kademlia | Provider record is a hint, direct signed announcement fetch is mandatory, lookup is context-bounded | Preserved. The netns stale-provider test additionally requires direct fetch failure without persistence. |
| Socket mux | One socket per family, `IPV6_V6ONLY`, common port, bounded queues, payload copy, write/close synchronization | Preserved. Existing race tests directly inspect `IPV6_V6ONLY`; the M4b runner repeats those tests inside a namespace and independently inspects visible sockets. |
| STUN | One Control reader, registration before send, exact transaction/source/family policy, bounded active/retired state, unknown traffic stays Data | Preserved; malformed, wrong-source, unknown, late, duplicate, close, and concurrent cases remain covered. |
| NAT profiler | Baseline then filtering then mapping, liveness after filtering timeout, no mapping inference from timeout, strict topology, concrete-address translation inference, bounded observations and one run per family | Preserved; M4a unit/integration tests remain the algorithmic oracle boundary. |

## Laboratory findings fixed during M4b

These were defects in newly added test infrastructure, not in production code:

- child stdout/stderr initially had no hard size limit; each stream is now
  capped at 4 MiB with a truncation marker;
- optional `conntrack` capture could initially fail an otherwise valid lab;
  it is now bounded best-effort diagnostic evidence;
- discovery data directories initially lived under retained artifacts and
  would have retained private identity files; runtime data now uses a separate
  strict `/tmp/hydra-m4b-runtime-...` path that is removed automatically;
- process-group shutdown now treats an already-exited process (`ESRCH`) as a
  successful idempotent stop;
- final address, route, and nftables snapshots are captured for every namespace
  before deletion.
- nftables rules originally placed comments before terminal statements, which
  nftables 1.0.9 rejects; rules now place comments after actions and verdicts;
- the socketmux helper originally sent to wildcard receiver addresses; it now
  binds receivers to the concrete family-local address being exercised;
- the bootstrap-removal test originally treated an idle libp2p connection as
  proof of direct protocol availability; it now restarts A with its unchanged
  identity and the previously discovered C address, then requires a fresh
  signed announcement fetch while B is absent;
- host-integrity normalization originally compared DHCP lifetime countdown
  fields; it now excludes only those volatile lifetimes while continuing to
  compare every host address and route;
- routed IPv6 profiling originally raced neighbor discovery with intentionally
  short STUN deadlines; the disposable namespaces now install exact permanent
  neighbor entries from their actual veth MAC addresses before traffic starts.

## Validation boundary

The normal-user build, unit, integration, race, shuffle, and demo gates can run
without privileges. Physical namespace, routing, NAT44, firewall, two-address
IPv6, host-integrity, and soak claims require `make test-m4b` and
`make test-m4b-soak` to execute rather than skip. A host that rejects
`sudo -n true` cannot establish those claims; that condition is a failed M4b
acceptance gate, not a passing skip.
