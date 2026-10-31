# M5c1 direct ICE connectivity

M5c1 proves one direct, no-NAT ICE connectivity generation between two live
Hydra PreparedSessions. It is not a data plane and does not validate NAT hole
punching.

## Authenticated ownership boundary

```text
authenticated M5b Offer/Answer
          |
          v
PreparedSession bound once to
SessionID + peer pair + EXIT + roles
          |
          v
iceconnect.Attempt claims the Agent once
```

The initiator's PreparedSession is bound only after a complete Answer has been
validated against the authenticated libp2p peer. The responder binds its fresh
PreparedSession after Offer validation and before sending the Answer. An
unbound session or any mismatch in SessionID, initiator, responder, local peer,
remote peer, purpose, or roles is rejected before `AddRemoteCandidate`.
Binding and connectivity claims are concurrency-safe and one-shot.

`Attempt` owns the claimed PreparedSession lifecycle. A successful
`ConnectedSession` keeps the Pion Agent, Hydra ICE adapter, and ICE Control
lease alive. Neither object owns or closes HydraSocketMux. The internal
`ice.Conn` is deliberately not exposed as an application transport.

## State and loading

```text
Prepared -> Loading -> RemoteLoaded -> Connecting -> Connected
                    \                 \
                     +-----------------> Failed

Connected or Failed -> Closed
```

The authenticated remote description is validated and deep-copied during
construction. Loading converts its deterministic, complete non-trickle
candidate list through `icesession.CandidateToPion`, calls
`Agent.AddRemoteCandidate` for every item, then checks
`Agent.GetRemoteCandidates` until Pion's internal task queue exposes exactly
the expected set. The wait is bounded by caller context and a configurable
upper limit. A conversion, addition, or partial-load failure makes the Agent
terminal; rollback and reuse are deliberately forbidden.

The loader accepts the M5b HOST and SERVER_REFLEXIVE schema. M5c1 success
tests permit only a selected HOST-to-HOST pair. SRFLX path validation remains
M5c2 scope.

## Connectivity and evidence

The authenticated role mapping is fixed:

```text
Offer initiator  -> controlling -> Agent.Dial
Offer responder  -> controlled  -> Agent.Accept
```

Both are blocking Pion v4.4.1 APIs driven by the caller's context. There is no
`StartDial`, `StartAccept`, `AwaitConnect`, restart, renomination, or second
generation. Exactly one concurrent caller can move an Attempt to Connecting.

Success is accepted only when both peers return successfully and Pion reports
a selected pair that is:

- non-nil, nominated, and succeeded;
- HOST-to-HOST on one common UDP family;
- present exactly in the local and remote immutable descriptions;
- backed by nonzero connectivity-check requests and responses;
- accompanied by an increase in Hydra ICE-adapter outbound transactions;
- free of application packets and bytes.

The result snapshots SessionID, roles and PeerIDs, candidate metadata,
endpoints, network, selected pair diagnostic ID, RTT, nomination state, check
counters, and connection time. It never contains ICE passwords, raw signaling
messages, private keys, or a mutable Pion candidate.

## Physical transport proof

Unprivileged tests cover IPv4, IPv6, dual-stack, credential failures,
unreachable candidates, immutable descriptions, candidate-load failures,
concurrent misuse, cancellation, close ordering, and post-ICE Data usability.
Full-chain integration tests use real Hydra identities, an authenticated
libp2p `/hydra/ice/1.0.0` exchange, real M5a Agents, candidate loading, and
blocking Dial/Accept.

The privileged M4b-derived lab runs peers in separate dual-stack namespaces.
While their selected pairs remain live it verifies `/proc/net/udp` and
`/proc/net/udp6` contain exactly one Hydra socket for each enabled family.
Namespace-local nftables counters independently require all direct ICE UDP
traffic to use `Mux.Port()` and count any wrong source port as a failure. A
separate deterministic output-drop policy proves bounded failure with no false
selection. Host routes, addresses, firewall, and forwarding settings remain
unchanged and namespace cleanup remains transactional.

Use:

```bash
make test-m5c1
make test-m5c1-netns
make test-m5c1-soak
```

M5c2 owns NAT, srflx-selected and peer-reflexive connectivity, and real hole
punching. M5c3 owns restart/reconnect policy. M6 owns HydraBind, AmneziaWG, and
all application traffic.
