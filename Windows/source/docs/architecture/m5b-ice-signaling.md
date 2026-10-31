# M5b authenticated ICE description signaling

M5b prepares live Pion ICE Agents and exchanges complete, non-trickle session
descriptions. It deliberately does not install remote candidates or start the
ICE state machine.

## Ownership and phase order

```text
HydraSocketMux (owns UDP4/UDP6)
       |
       +-- STUN Control phase: stunprobe/natprobe
       |          |
       |          +-- close client, release STUN lease
       |
       +-- ICE Control phase: iceadapter + Pion Agent
                  |
                  +-- gather local host candidates
                  +-- add detached Hydra-derived srflx metadata
                  +-- authenticated one-shot libp2p signaling
```

`icesession.PhaseManager` is instance-local and admits one STUN or ICE owner.
It does not own the mux. After the M5c3 ownership correction, the transport
generation acquires the ICE lease and creates one shared ICE engine. That
engine owns the M5a Registry and Adapter whose classifier was installed before
mux construction.

A PreparedSession owns its Pion Agent and immutable local description plus a
registration in the shared engine. It does not own HydraSocketMux, the shared
ICEAdapter, the phase lease, or the libp2p host. Close is concurrency-safe and
idempotent and cannot affect other sessions on the engine.

## Local description

Pion gathers only HOST candidates over UDP4 and UDP6 through Hydra's
`ice.UDPMux`. mDNS, TCP, Pion srflx gathering, TURN, and relay candidates
remain disabled. Every host port must equal `Mux.Port()`.

Verified M3b Basic STUN results may be supplied as bounded
`SrflxObservation` values. M5b uses Pion's official server-reflexive
constructor to obtain foundation, priority, component, and related endpoint
metadata. The detached candidate is signaling metadata only and is not added
to the Agent. If the mapped endpoint equals the host endpoint, the redundant
srflx candidate is omitted.

Descriptions contain Pion-generated ufrag/password credentials and at most 16
candidates by default. Production validation accepts HOST and
SERVER_REFLEXIVE, UDP4/UDP6, component 1, concrete unicast non-link-local
addresses, nonzero ports, bounded foundations, and ICE character strings.
IPv4-mapped IPv6, TCP, relay, signaled peer-reflexive, mDNS names, multicast,
and duplicate candidates are rejected. Loopback is disabled except by explicit
test configuration.

## Wire protocol and authentication

The stream protocol is `/hydra/ice/1.0.0`. Each stream carries one 4-byte
big-endian length plus one protobuf Offer, followed by one bounded protobuf
Answer or generic Error. `MaxFrameSize` is 32 KiB. Candidate addresses use
exactly 4 or 16 raw bytes; the wire format does not depend on Pion's SDP or
`Candidate.Marshal` strings.

An Offer contains:

- Hydra protocol version 1;
- a nonzero crypto-random 16-byte SessionID;
- initiator and responder PeerIDs;
- EXIT purpose and future CONTROLLING role;
- complete credentials and candidate description;
- bounded creation and expiry timestamps.

The Answer must copy SessionID, peer pair, and purpose unchanged, use the
future CONTROLLED role, and carry a complete responder description.

The authenticated libp2p connection is the trust boundary. The responder
requires `stream.Conn().RemotePeer()` to equal Offer.initiator and its own
host identity to equal Offer.responder. The initiator performs the symmetric
check on the Answer. No extra message signature is needed. The stream uses the
existing default secure libp2p transport; Hydra's insecure transport is never
enabled.

ICE passwords exist only in live memory and the encrypted signaling stream.
They are excluded from normal structured logs, helper status JSON, netns
artifacts, NodeAnnouncement, Kademlia, and SQLite.

## Bounds, replay, and handoff

The service bounds active stream handlers, queued PreparedSessions, sessions
per authenticated remote peer, and recent SessionIDs. The replay registry is
limited by count and TTL. An already seen peer/SessionID pair returns
SESSION_CONFLICT without creating another PreparedSession. Authorization runs
before session allocation. Capacity failures return a generic BUSY error.

Successful responder sessions enter a bounded AcceptedSession queue. Calling
`NextAccepted` transfers PreparedSession ownership to the application;
Service.Close closes only sessions that have not been handed off. Closing the
service removes its handler, resets owned streams, cancels preparation, waits
for handlers, and is idempotent.

## Validation evidence and boundary

Unprivileged tests use real secure TCP-only libp2p hosts and live M5a/Pion
Agents for IPv4, IPv6, dual-stack, concurrent peers, replay, identity forgery,
expiry, authorization, capacity, cancellation, frame bounds, and candidate
round trips.

The privileged M4b laboratory adds:

- IPv4 and IPv6 signaling across separate network namespaces;
- exact PeerID, SessionID, ufrag, and candidate comparison;
- real NAT44 Basic STUN observation propagated as the exact srflx endpoint;
- host-integrity and transactional namespace cleanup;
- proof that outbound ICE transaction counters remain unchanged.

Use:

```bash
make test-m5b
make test-m5b-netns
make test-m5b-soak
```

M5b makes no `AddRemoteCandidate`, Dial, Accept, connectivity-check,
nomination, or application-data call. M5c owns those future steps. M6 owns
AmneziaWG/HydraBind and the data plane.

The privileged NAT signaling test first obtains an independent M3b/responder
oracle, then repeats Basic Binding and the STUN-to-ICE phase transition on the
signaling helper's same `HydraSocketMux`. The carried server-reflexive endpoint
must match both observations.
