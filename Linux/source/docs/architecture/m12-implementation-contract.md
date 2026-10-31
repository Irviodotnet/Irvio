# M12 implementation contract and preflight

This document freezes the implementation contract established before M12 code
changes. It records facts observed in the M11 tree and the exact compatibility
decisions that M12 must preserve. The detailed protocol and ownership documents
remain `m12-rendezvous.md` and `m12-session-control.md`.

## Baseline and toolchain

- Baseline: `2311127cae462affaddfea62772185ad563b3ff9`.
- Branch: `feature/m12-rendezvous`.
- Annotated tag `hydra-m11` points at the baseline.
- The baseline worktree was clean and `go test ./...` passed before M12 edits.
- Go module language version: Go 1.27.1.
- Protobuf generator: `protoc` 3.21.12 and `protoc-gen-go` 1.36.12.
- libp2p: v0.49.0; go-libp2p-kad-dht: v0.42.2; Pion ICE: v4.4.1;
  Pion STUN: v3.1.7; AmneziaWG: v3.1.20260828.

The pinned libp2p Noise transport exposes `SecureInbound`, `SecureOutbound`,
`WithSessionOptions`, `Prologue`, and optional early-data handlers. M12 uses a
domain/version prologue, verifies the expected remote PeerID on outbound
connections, and permits no lease operation before authenticated `BIND` on an
inbound connection. It does not use `DisablePeerIDCheck` for outbound sessions.

The pinned kad-dht source defines provider-record validity as 48 hours and its
separate provider subsystem default reprovide interval as 22 hours. Hydra's
existing discovery owner explicitly republishes its own service records every
12 hours. These are discovery hints, not M12 liveness. Rendezvous country keys
are provided on bounded state changes and on the existing explicit refresh
lifecycle, never per heartbeat.

## Existing compatibility constraints

The M11 signed `NodeAnnouncement` permits 32 KiB payloads and 64 KiB signed
envelopes. M11 public-control proofs can legitimately use that compatibility
budget. M12 therefore does not silently reduce the announcement limit to 8 KiB.
It applies an 8 KiB preferred descriptor budget to new records, retains the
64 KiB signed-envelope compatibility ceiling for verified M11 records, and
always enforces the 64 KiB Rendezvous frame limit. Frame overhead can therefore
make the deliverable per-operation limit smaller than the envelope ceiling, and
a `CANDIDATES` response may contain fewer than ten records when byte accounting
requires a partial result.

libp2p signed envelopes expose the exact authenticated `RawPayload`. M12 hashes
those original bytes for `descriptor_id`; it never reserializes protobuf to
verify a descriptor. Deterministic protobuf serialization is used only to
produce a new local payload and is not treated as canonical encoding.

M11 starts each announcement publisher with a random process epoch and sequence
one, while PeerDirectory orders a changed epoch by wall-clock creation time.
That does not meet the M12 durable-order contract. M12 replaces local publisher
ordering with a persistently incremented incarnation and a monotonically
increasing sequence inside that incarnation. PeerDirectory compares only the
lexicographic `(incarnation, sequence)` for one PeerID, while retaining the AWG
key-epoch rollback rule. A same-version identical payload is idempotent; a
same-version different payload is a conflict; a lower version is stale.

The protection/routing identifiers already owned by M7-M11 remain authoritative:

```text
HYDRA_BYPASS_MARK = 0x48594452
hydra_out         = 51820
hydra_wan         = 51821
```

M11's cgroup route/output rule currently marks hydrad-owned traffic for WAN
bypass. M12 reserves a distinct centrally defined `TUNNEL_CONTROL` mark and
changes the cgroup rule so it cannot overwrite an explicit nonzero transport
intent. A higher-priority exact inner-prefix rule routes that mark only through
`hydra0`; missing inner ownership resolves to unreachable, never the main WAN.
The M8 output policy permits it only on `hydra0`. This keeps public accepted
control replies on the M11 WAN path while binding session-control SYN,
SYN-ACK, and data to the tunnel.

## Component and ownership contract

The single production `runtime.Daemon` remains the top-level owner. M12 adds
generation-fenced children in this order:

```text
Daemon
  -> Rendezvous service (optional, public-proof gated)
  -> Rendezvous discovery/client working set
  -> Exit registration client (optional, CAP_EXIT gated)
  -> Rendezvous CandidateProvider adapter
  -> signed session-establishment verifier and broker carrier
  -> direct in-tunnel session-control carrier
```

The live registration registry and signaling pending contexts are bounded RAM
and are never reconstructed as online after server restart. Persistent state is
limited to durable local announcement incarnation/version, verified peer/AWG
binding rollback data, identity/configuration, and bounded discovery cache.

One registration owner is:

```text
ExitPeerID + ServerIncarnation + RegistrationID + owning channel token
```

Only that tuple may heartbeat, update, unregister, or be removed by a close
callback. `SAMPLE_LOCAL` reads the local country index and is structurally
incapable of issuing `GET_CANDIDATES`, another `SAMPLE_LOCAL`, or DHT discovery.

The direct session owner starts only after signed PREPARE/READY/OFFER/ANSWER,
ICE-selected endpoint validation, AWG authentication, provisional TunnelGrant,
and authenticated in-tunnel BIND. Promotion transfers ICE/AWG/lease/control
ownership to the existing M10 runtime. Broker context expiry, descriptor or
receipt expiry, owner loss, and late CANCEL cannot close a promoted session.

## Protocol and resource contract

- Rendezvous protocol: `/hydra/rendezvous/1.0.0`.
- Direct signed establishment: `/hydra/session-establish/1.0.0`.
- In-tunnel authenticated maintenance: `/hydra/session-control/1.0.0`.
- Rendezvous frame: 64 KiB, checked before allocation.
- Receipt: 2 KiB. Candidate count: 10. Owner routes: 3. Exclusions: 32.
- ICE candidates per side: 16. Signaling messages per attempt: 16.
- Aggregate brokered signaling payload per attempt: 64 KiB.
- Session-control frame: 16 KiB.
- Registrations: 10,000. Pending signaling contexts: 256.
- Concurrent outgoing SAMPLE: 32. Concurrent DHT lookup: 8.
- Client acquisition GET concurrency: 2; four GET RPCs and ten unique peers per
  round; server remote SAMPLE cap: three per GET including retries.

All limits are normalized once in configuration and enforced before expensive
signature verification or nested allocation where possible. Overload returns a
typed `BUSY`, `PARTIAL`, or `RATE_LIMITED` response. It never creates an
unbounded queue or retry loop.

## Country and discovery truth

`CountryResolver` reads an operator-installed local MaxMind DB snapshot. It
looks up only the IP observed on the direct authenticated registration
connection. Missing/corrupt DB is an explicit degraded state. `UNKNOWN` and
claimed country never silently become `OBSERVED`. IPv4 and IPv6 observations
retain separate provenance; conflicting observed countries do not satisfy an
exact `CountryIntent` in v1.

The synthetic DHT keys are:

```text
hydra/v1/rendezvous
hydra/v1/rendezvous/<COUNTRY>
```

They locate candidate Rendezvous servers and do not encode a live registry.
The existing `hydra/v1/exit/<COUNTRY>` path remains an explicit compatibility
fallback. No DHT timeout is interpreted as proof that a country has no exits.

## Establishment authentication and replay contract

Every M12 establishment message signs its exact payload in a separate domain.
The payload binds protocol/version, kind, sender, recipient, nonzero random
128-bit AttemptID, owner-local token, predecessor hash, expiry, and complete
kind-specific data. READY adds an independent 256-bit Exit challenge. Brokers
route opaque signed messages and never become the end-peer authorization
identity. Mutated, reordered, expired, wrong-peer, wrong-predecessor, or replayed
messages are rejected before allocation or mutation.

PREPARE creates only a bounded pending context. OFFER may create one idempotent
provisional allocation tied to the AttemptID. Duplicate identical messages
return the stored result; the same request ID with changed bytes is a conflict.
Unactivated grants expire into the M10 revoke/quarantine path. CANCEL controls
only a matching pending attempt and loses authority after promotion.

## Healthy-session independence

Lease renewal, state query, rebind, release, bounded health, and supported AWG
rotation use authenticated TCP+Noise inside the existing AWG tunnel. They use
neither Rendezvous nor `ice.Conn`. Loss of every Rendezvous, DHT provider,
registration receipt, descriptor freshness, and legacy WAN control connection
does not terminate a healthy active lease. New signaling may require an
introducer only after the direct path itself is lost.

## Explicit non-claims

M12 does not provide consensus, a global live database, globally uniform
sampling, Sybil resistance, geographic egress attestation, public UDP/STUN
capability, TURN/relay, arbitrary proxying, global telemetry, token entitlement,
privilege separation, or the shared-engine STUN ControlRouter. Disk snapshot
rollback or cloned Hydra identity cannot be made rollback-proof without an
external monotonic authority; this limitation remains explicit.
