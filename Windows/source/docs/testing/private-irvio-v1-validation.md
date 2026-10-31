# Private IRVIO v1 validation

Status: implementation and integrated validation in progress; full P00–P11 acceptance is not complete. The dated entries below preserve the sequence of tests, failures and corrections; an older entry's pending status may be superseded by later measured evidence.

Tests run exclusively on the mini-PC. Workstation builds and static inspection are permitted; live networking and tests on the workstation are not. Production distributors and published releases remain untouched.

Current requirement coverage and remaining priorities: [acceptance audit](private-irvio-v1-acceptance-audit.md). This supplements, not replaces, the original plan and chronological evidence.

## Evidence ledger

| Stage | Status | Evidence / remaining work |
| --- | --- | --- |
| P00 | Contract documented | `docs/product/private-irvio-v1.md`; final requirement-by-requirement acceptance remains open |
| P01 | Storage and ownership implemented/tested | Race/shuffle tests, strict storage/deletion, product integration and persistent membership used by real traffic tests |
| P02 | Admission and revocation implemented/tested | Revision fencing and negative package tests; established-traffic revoke passes in real lab; full integrated race matrix remains |
| P03 | Private registration/public isolation exercised | Real private registration, signaling and traffic; public country results exclude private hosts; broader mixed-version/adversarial matrix remains |
| P04 | Distributed discovery, migration and restart exercised | Disjoint bootstrap/owners, loss/replacement, same-identity owner restart with 136s registration tail; stale/forged-provider end-to-end matrix remains |
| P05 | Real enrollment/approval and two-UI onboarding exercised | Signed links, routed enrollment, pending/no-access, native QR save/import, explicit owner approval and user CONNECT passed; full negative acceptance audit remains |
| P06 | Pinned traffic and recovery exercised | Real ICE/AWG/lease and HTTP/UDP/DNS/ICMP; client crash and NAT-remap recovery keep exact target; remaining NAT variants and timing analysis open |
| P07 | Lifecycle implemented; partial integrated acceptance | Hosting-only, upstream, combined hosting/upstream, full leave, client/host/combined-host crash, real systemd client restart/intentional exit and 100 cycles exercised; real system DNS/fresh-boot and remaining GUI lifecycle gates open |
| P08 | Local API implemented/tested | Negotiation, authorization, asynchronous command handling, state/revision fencing and real daemon API flows; complete acceptance audit remains |
| P09 | UI implemented; positive native onboarding exercised | Real two-UI QR save/import, pending/cancel/approve/CONNECT and client/hosting GUI SIGKILL/relaunch passed; connected tray Shutdown and complete acceptance audit remain |
| P10 | Partially exercised | Public critical privileged suites passed; real private traffic, resource, EIM and controlled host endpoint-dependent NAT evidence below; full adversarial/NAT/lifecycle matrix remains |
| P11 | Local preview built and installation exercised | Clean 90f8431 archive audited (567 files), extracted runtime passed, real mini-PC install/uninstall/reinstall preserved 13 persistent files; full private acceptance/handoff remains open, nothing published |

## Required negative tests

- Corrupt/unknown/duplicate-key trust data, insecure ownership/permissions, symlinks, oversized input, rollback after failed write, concurrent writers and profile deletion.
- Single-use invitation races, expiry, replay after revoke, refusal, restart with pending approval, capacity exhaustion and malformed PeerIDs.
- Broker impersonation, forged/expired receipts, old unsupported distributors, exact-identity mismatch and no public discovery/fallback.
- Revoke during PREPARE, ICE, activation, renewal and forwarding; all alternate admission paths.
- Cross-owner DHT lookup, migration/addressless providers, incompatible NAT, same-boot crash, fresh boot and complete teardown.
- 100 lifecycle cycles, real traffic, physical-WAN leak and anti-multihop oracles; affected public regressions.

Each entry requires measured artifacts and exact commands before being marked passed. Unit tests do not substitute for network or resource gates.

## 2026-10-06 — trust storage

Host: authorized mini-PC `192.168.23.2`, unprivileged isolated source directory `/home/ubuntu/irvio-private-validation`. No installed daemon/UI or production server changed. Go 1.27.1, CGO enabled.

```sh
export PATH=/home/ubuntu/irvio-country-validation-tools/go1.27.1/bin:$PATH
export GOMODCACHE=/home/ubuntu/irvio-country-validation-tools/mod
export GOCACHE=/home/ubuntu/irvio-country-validation-tools/cache
export GOPROXY=off CGO_ENABLED=1
go test -race -shuffle=on -count=20 ./internal/privateaccess ./internal/productprofile
go vet ./internal/privateaccess ./internal/productprofile
```

Both commands exited 0. Test output:

```text
ok github.com/Irviodotnet/Irvio/internal/privateaccess 3.875s
ok github.com/Irviodotnet/Irvio/internal/productprofile 1.087s
```

Coverage includes a 32-client simultaneous invitation reservation, concurrent approval/revoke, restart with pending approval, consumed-invitation replay, expiry boundary, pending/invitation capacity, duplicate/unknown/case-alias JSON fields, size/depth bounds, immutable snapshots, rejected transactions, disk-write failure poisoning, host-identity mismatch, permissions, symlinks, competing stores and profile deletion including interrupted-write files. Corrupt state remains intact and errors do not echo its secret contents.

Initial fixtures failed because Go's test subdirectory inherited group-write permissions from the mini-PC umask. Fixtures now set 0700 explicitly; production rejects insecure directories unchanged. The private store is not yet wired into runtime; these checks prove storage behavior, not admission fencing, network enrollment or traffic authorization.

The existing libp2p TLS transport exposes `SecureInbound`/`SecureOutbound` over `net.Conn` with authenticated PeerID verification; it is suitable for the proposed end-to-end enrollment carrier. Relay adapter, bounds and malicious-broker integration tests remain required in P05.

## 2026-10-06 — admission and revocation

New optional `exitaccess.Policy` fences lifecycle resource mutations. Public runtimes leave it nil. Private runtimes require authenticated session tokens, capture the grant revision at PREPARE and pass it through provisional allocation; activation, renewal, mutation and BIND/STATE lookup require that same live grant. A newer grant never authorizes an older pending transcript.

| Entry point | Enforcement |
| --- | --- |
| Signed PREPARE | Verify signed end-client; hold admission permit through pending transcript creation |
| Signed OFFER | Exact captured grant revision; track bounded asynchronous work and cancel on revoke |
| ICE legacy service | Reject before creating a prepared transport in private mode |
| Legacy TunnelLease allocation | Missing private admission revision is rejected even with a valid session token |
| Activation / renewal / mutation | Hold admission fence through resource commit |
| Direct control / AWG rotation | Existing authority uses fenced lifecycle lookup/mutation |
| RELEASE / administrative cleanup | Remains callable after denial; authorization cannot prevent cleanup |

`RevokePrivateClient` has a 10-second deadline: fence admissions, durably deny, cancel attempts, remove pending transcripts, revoke leases with existing resource cleanup, then wait for tracked work to drain. Cleanup failures remain visible; denial is not rolled back. Tracking is capped at 256 asynchronous operations per policy. Provisional cleanup now removes pending ICE resources as well as active resources.

Tests use the real trust store and lease lifecycle with controlled resource callbacks. Cases include unknown client, legacy bypass, regrant fencing, revoke racing activation, revoke racing resource mutation, asynchronous drain, drain timeout and cleanup failure. Signed initiator/responder tests verify that broker PeerID is not used as client authorization and that revoked PREPARE never reaches the resource processor.

Affected checks on the mini-PC use the same environment above:

```sh
go test -race -shuffle=on -count=10 ./internal/privateaccess ./internal/tunnellease ./internal/sessionestablish ./internal/sessioncontrol ./internal/tunnelsignal ./internal/runtime
go vet ./internal/exitaccess ./internal/privateaccess ./internal/tunnellease ./internal/sessionestablish ./internal/sessioncontrol ./internal/tunnelsignal ./internal/runtime
go test ./cmd/irviod ./internal/productprofile
```

All three commands exited 0. Final race/shuffle output: privateaccess 4.322s, tunnellease 1.103s, sessionestablish 1.738s, sessioncontrol 11.380s, tunnelsignal 4.110s, runtime 86.261s. Daemon/profile tests passed in 0.567s/0.004s; vet emitted no diagnostics.

These are unprivileged tests, not proof of real private traffic, netns revocation, Safety Policy or anti-multihop. Private publication and signed target negotiation remain P03/P06; product/API must use the policy's revoke operation rather than bypassing it through direct store writes. No private mode is exposed to users yet.

## 2026-10-06 — private Rendezvous isolation

Private registration uses a separate negotiated stream, registry and signed receipt domain. Public descriptors/receipts cannot enter private lookup; private descriptors/receipts cannot enter public candidate decoding or registration. Private records have no country index. Exact lookup checks target identity, locator commitment, descriptor freshness and live registration fencing. Owner-specific signaling authenticates the responding registration channel, including replacement races.

The mini-PC tests create real libp2p hosts: an old public-only service rejects private negotiation without registering the Exit; co-located public/private services support private registration, lookup, updates, heartbeat, opaque signaling and unregister, while public country results remain empty. Closing the private service leaves public handlers intact. This is transport-level evidence, not production ICE/AWG or user traffic evidence.

Negative coverage includes wrong locator/target, forged route incarnation, tampered receipt, cross-domain receipt verification, mixed public/private capabilities, oversized frames, unsupported version, canceled registration, stale/expired records, replaced channels, descriptor rollback and locator rotation. Replacement at registry capacity also preserves the existing record until the new receipt is successfully created.

Latest checks on the mini-PC, using the environment above:

```sh
go test -race -shuffle=on -count=10 -run 'Private|SignalingResponseFences' ./internal/rendezvous ./internal/serviceindex
go test -race -shuffle=on -count=3 ./internal/rendezvous ./internal/protocol ./internal/discovery
go vet ./internal/rendezvous ./internal/protocol ./internal/serviceindex ./internal/discovery
```

All exited 0. Focused results: rendezvous 4.168s, serviceindex 1.020s. Full affected race results: rendezvous 36.010s, protocol 1.566s, discovery 29.305s. Vet emitted no diagnostics. Earlier normal runtime and daemon tests passed too; these do not prove the still-unwired private runtime.

The private owner CID mapping is implemented and tested; DHT publication/discovery, M11 owner eligibility, enrollment relay and cross-distributor establishment remain outstanding. No installed application or production distributor was changed.

A subsequent mini-PC recheck, `go test -race -shuffle=on -count=1 ./internal/rendezvous ./internal/protocol ./internal/serviceindex ./internal/discovery`, exited 0: 12.792s, 1.243s, 1.026s and 10.513s respectively.

## 2026-10-06 — private owner DHT primitives

`Node.ProvidePrivateOwner` publishes the locator CID under a bounded context. `Node.FindPrivateOwners` requests at most eight hints and reuses existing addressless-provider resolution. Three verification workers authenticate remote announcements and require Rendezvous, verified Public Control and `private-rendezvous-v1`. Existing announcement verification checks M11 attestations without changing quorum. Empty results and interrupted/failed discovery are incomplete, not proof that the private Exit is absent.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/discovery
go vet ./internal/discovery
```

Both exited 0; race tests completed in 29.450s. New tests verify the three-worker bound, cancellation joining workers and invalid locator rejection before network access. Existing discovery tests also pass. These checks do not yet exercise the private lookup against a real DHT owner topology; publication lifecycle, feature activation and cross-owner integration are still required.

## 2026-10-06 — private publication lifecycle

`NewPrivateService` accepts an optional `PrivateOwnerPublisher`; public services reject it. Its service-owned worker uses only `Registry.PrivateLocators`, polls liveness every second, coalesces duplicate locators and limits in-flight DHT work to four calls with ten-second contexts. Successful publication refreshes after one minute; failures retry no sooner than ten seconds. Withdrawn/stale locators cancel outstanding calls and stop refresh. Existing DHT records may linger: exact live lookup, not provider presence, remains authoritative. Service shutdown cancels and joins publication before closing the registry.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=10 -run PrivatePublication ./internal/rendezvous
go test -race -shuffle=on -count=1 ./internal/rendezvous ./internal/discovery
go vet ./internal/rendezvous ./internal/discovery
```

All exited 0: focused publisher tests 1.690s; full package race tests 12.829s and 10.489s. Tests cover four-worker saturation, cancellation after withdrawal, no queued withdrawn work, deduplication, scheduled refresh and worker drain on shutdown. Publisher callbacks are controlled test doubles; real DHT refresh/withdrawal and full runtime shutdown remain integration gates. The production runtime has not enabled the private service yet.

## 2026-10-06 — exact private candidate acquisition

`PrivateLookup` pins a validated target PeerID and nonzero locator. Each serialized ten-second round gives owner discovery at most six seconds, then contacts at most eight unique eligible owners with three workers and 1.5-second per-call budgets. Responses are revalidated for exact target, locator, signatures, expiry and a single owner route matching the contacted server. Candidates merge only for the same descriptor identity/version; newer versions supersede older ones, while same-version conflicts fail closed. Public sampling is never called.

A valid result from one owner remains usable when another fails, with `Partial=true`. No valid candidate returns incomplete lookup, not authoritative offline. Current code rediscovers on every call and keeps no stale route cache; bounded caching and runtime route invalidation remain to be integrated.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=10 -run PrivateLookup ./internal/rendezvous
go test -race -shuffle=on -count=1 ./internal/rendezvous
go vet ./internal/rendezvous
```

All exited 0; focused tests 1.176s, full package tests 12.812s. Signed registry fixtures cover duplicate owner hints, one failed owner, owner migration between rounds, stale registry rejection, cross-owner receipt replay and cancellation. Discovery/RPC are controlled callbacks here: real cross-distributor DHT, production negotiation and user-plane traffic are not yet proven.

## 2026-10-06 — real DHT owner discovery and readiness

`SetPrivateRendezvousReady` adds/removes signed private support only on an already validated Rendezvous. Public Rendezvous withdrawal or loss of public reachability removes private support too. Private provider publication verifies the current local signed announcement and refuses refresh after readiness withdrawal.

`TestPrivateOwnersRealDHTAcrossBootstrapAndWithdrawal` starts four real discovery/libp2p/DHT nodes: one bootstrap, two owners and one fresh client configured only with that bootstrap. Both owners publish the opaque locator; the client discovers/authenticates both without preloaded owner endpoints. Withdrawing the first owner's private feature excludes its lingering DHT record; withdrawing the second owner's M11 proof excludes it too. Refresh by a withdrawn owner fails. Enabling private support before M11/Rendezvous readiness fails.

The M11 fixtures contain valid signatures from two distinct generated verifier identities, checked with the unchanged production proof policy. They are synthetic attestations, not live external reachability measurements. This test proves DHT discovery/authentication and stale-hint filtering, not registration RPC, NAT traversal or tunneled traffic.

Final mini-PC commands:

```sh
go test -race -shuffle=on -count=3 ./internal/discovery
go vet ./internal/discovery
```

Both exited 0; tests completed in 30.607s. Earlier three repetitions of the new real-DHT test passed in 2.462s before the added publication-readiness guard; the final command includes that guard and its negative test.

## 2026-10-06 — distributed registration-to-signaling path

`TestPrivateDistributedRegistrationLookupAndSignaling` joins an Exit to two real private registration services, each on a separate discovery/libp2p node. Their service-owned publication workers publish live locators through real DHT. A fresh client starts with only a separate bootstrap address, then uses `PrivateLookup` with the production discovery node and RPC client. It obtains the exact signed Exit descriptor and both owner routes, while public country RPCs remain empty.

After one private service stops, lookup returns the surviving route with partial status and opaque signaling reaches the Exit through that route. After the other registration leaves, lingering provider records produce no live candidate. Re-registration produces a different RegistrationID, and signaling with the former route is rejected.

The bootstrap is not a registration owner. No owner endpoint or country candidate is preloaded into the client. M11 proofs are signed synthetic fixtures under the unchanged production verification policy. The signal handler is an echo fixture, not session establishment: this test does not prove enrollment, ICE, AWG, TunnelLease or user traffic.

Mini-PC final checks:

```sh
go test -race -shuffle=on -count=3 ./internal/rendezvous
go vet ./internal/rendezvous
```

Both exited 0; full package race tests completed in 40.809s. Before adding the re-registration case, three focused distributed test runs also passed in 5.716s. Installed software and production distributors were not modified.

## 2026-10-06 — signed invitation links

`EncodeInvitation` / `DecodeInvitation` implement bounded `irvio-private:v1:` links through the existing libp2p signed-record mechanism, with a separate domain and codec. Verification binds Ed25519 signer to host PeerID and validates canonical protobuf/base64url, random-material sizes, nonzero values, lifetime and expiry. Parsing has no persistence, network or grant side effects. Default token string formatting is redacted; callers must still never log the raw link or secret fields.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=10 ./internal/privateaccess
go test ./internal/rendezvous
go vet ./internal/privateaccess
```

All exited 0: privateaccess 3.859s and rendezvous 9.830s. New cases cover roundtrip, exact expiry, tampered signature, different signer, consumed invitation, zero secrets/locators, excessive lifetime/future creation, overlong input, wrong scheme, padding/newlines, authenticated malformed payloads and duplicate/unknown/noncanonical protobuf fields. Existing storage/revocation tests still pass. Invitation creation is not yet exposed through Local API/UI, and no network enrollment or approval grant is implied by successful decoding.

## 2026-10-06 — authenticated enrollment transport

`EnrollmentServer` and `Enroll` use existing libp2p TLS identity transport over a caller-owned `net.Conn`. The initiator verifies the exact invited host before sending secret material. The server derives client PeerID from the secure connection, never from a request field. Reserve creates only pending state; a separate owner approval is necessary. Status requests reveal only the authenticated client's own invitation state and current grant. Revocation returns denied. Polling an already-approved invitation can continue after invitation expiry; a new reservation cannot.

Each connection carries one bounded canonical protobuf request/response, with a 4-KiB frame bound, thirty-second handshake/exchange deadline and eight concurrent server slots. Cancellation closes the carrier. Both sides require Ed25519 identities. No custom key exchange was introduced.

Final mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./internal/privateaccess
go vet ./internal/privateaccess
```

Both exited 0; tests completed in 2.789s. Real loopback TCP/TLS tests cover idempotent pending reservation, copied invitation from another identity, approval, unauthorized status polling, revoke, wrong host identity, concurrency capacity and canceled handshake drain. The updated protobuf also passed the Rendezvous suite (9.832s) before the final Ed25519/cancellation checks. An initial `net.Pipe` fixture passed but delayed TLS close notification; the main exchange fixture now uses TCP. No transport timeout was weakened.

This verifies the secure enrollment endpoint, not the untrusted distributor carrier. Routed encrypted byte transport, relay resource limits, broker-tampering tests, Local API/UI and crash-safe imported invitation workflow remain required before P05 is complete.

## 2026-10-06 — enrollment over a registered owner relay

Private Rendezvous now exposes an enrollment-only relay with exact target/locator/live registration/channel checks. It opens the endpoint stream only on an existing Exit connection (`WithNoDial`), limits eight concurrent sessions, thirty seconds and 64 KiB per direction, and joins both copy workers on cancellation. Service shutdown removes the relay handler and cancels tracked streams. No invitation secret or claimed end-client identity is included in the relay request.

`TestPrivateEnrollmentThroughRegisteredOwner` uses three real libp2p hosts and the production TLS enrollment implementation. The Exit test handler admits only its registered owner. Reserve and status return pending; the durable invitation records the actual client PeerID rather than the broker. Explicit approval then returns approved. Wrong locator and changed RegistrationID cannot open the relay. An oversized raw test stream is terminated without forwarding beyond the byte budget.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/rendezvous
go vet ./internal/rendezvous
```

Both exited 0; package tests completed in 41.418s. Three earlier focused relay/TLS runs passed in 1.561s before adding the byte-limit case. Production runtime still needs a managed Exit endpoint with owner-set lifecycle and shutdown tracking; malicious-relay substitution/corruption, complete DHT-to-enrollment orchestration and UI remain unproven.

## 2026-10-06 — managed enrollment endpoint

`PrivateEnrollmentEndpoint` starts with no admitted owners. `SetOwners` accepts at most three valid non-self PeerIDs, atomically changes admission and cancels active streams from removed owners. At most eight streams run concurrently, each with a thirty-second context. Close removes the handler, fences new work, cancels streams and joins all handlers. The relayed TLS test now uses this managed endpoint instead of a manual test handler.

Final mini-PC checks:

```sh
go test -race -shuffle=on -count=10 -run PrivateEnrollment ./internal/rendezvous
go test -race -shuffle=on -count=1 ./internal/rendezvous
go vet ./internal/rendezvous
```

All exited 0: focused tests 3.584s; full package race tests 14.616s. New real-host cases cover deny-by-default, removed-owner cancellation, owner re-addition, shutdown of a stalled exchange and refusal to reopen a closed endpoint. An initial negative fixture incorrectly required protocol negotiation to succeed before rejection; libp2p can reset during negotiation. The fixture now accepts either early rejection or reset on read while verifying the enrollment callback was never reached. Production admission was not weakened.

The registration manager still needs to drive this owner set automatically; no runtime product mode is enabled by this endpoint alone.

## 2026-10-06 — private registration manager

`NewPrivateRegistrationManager` requires a nonzero locator, a matching-host enrollment endpoint and an owner authorizer. It validates private descriptors, authorizes each prospective owner and opens only `OpenPrivateRegistration`, with no public fallback. `discovery.Node.VerifyPrivateOwner` provides production verification through current signed M11-backed Rendezvous/private capability. Successful registrations update the endpoint owner set; joined channel watchers remove lost owners and wake repair. Closing or canceling the manager clears endpoint admission; canceled managers cannot re-add owners through a late watcher.

The new real-libp2p test controls only the owner authorization decision: denied owners receive no registration, allowing an owner activates private enrollment admission, public registry stays empty, and closing the private service withdraws endpoint admission. Manager shutdown drains its channel watchers. Production M11 verification is separately covered by the real DHT tests, not by the controlled authorizer in this test.

Mini-PC checks before the final cancellation-order guard:

```sh
go test -race -shuffle=on -count=10 -run PrivateRegistrationManager ./internal/rendezvous
go test -race -shuffle=on -count=1 ./internal/rendezvous ./internal/discovery
go vet ./internal/rendezvous ./internal/discovery
```

All exited 0: focused 1.879s, full rendezvous 14.783s, discovery 10.959s. After the cancellation-order guard, focused race/shuffle ×10 and vet were repeated successfully (1.867s). Product runtime construction, private server selection policy and full DHT-to-enrollment UI flow remain to be wired and tested.

## 2026-10-06 — typed pinned selector target

ExitSelector configuration, candidate requests/batches and status/events now carry an explicit `Target`: public country or private PeerID. Legacy country-only configuration resolves to the public target. Private targets reject a country, self/invalid PeerID and country-change commands. Evaluation accepts only the pinned peer with private-only capability and brokered, versioned descriptor metadata; public candidates and unrelated private candidates cannot be probed or selected as ACTIVE/RESERVE/COLD. Common freshness, endpoint, bandwidth, protection and recovery behavior remains in the existing selector.

New tests cover identity pinning, no unrelated probes, no reserve/cold fallback, wrong-target batches, ambiguous configuration, expired/mixed-capability/direct private candidates and public exclusion of private descriptors. Legacy country batch matching retains its existing strictness.

Final mini-PC checks:

```sh
go test -race -shuffle=on -count=10 ./internal/exitselector
go vet ./internal/exitselector
```

Both exited 0; selector tests completed in 3.304s. Earlier affected checks also passed: runtime 7.795s, rendezvous 9.918s, cmd/irviod 0.527s, before the final legacy batch-case guard. No private production connector is wired yet: signed PREPARE mode binding, runtime candidate adapter and real pinned recovery remain required.

## 2026-10-06 — signed private establishment

`EstablishPrivate` signs version-2 PREPARE with the exact target PeerID and no country. Private-only signed Exit descriptors and their AWG metadata are checked before PREPARE and after READY. `ResponderOptions.PrivateMode` requires admission policy and rejects public-version requests; the public default rejects private-version requests. READY/OFFER/ANSWER/DECLINE/CANCEL preserve request version, and predecessor checks reject version changes. Existing ICE, AWG and TunnelLease payload semantics remain unchanged.

New tests run a complete signed PREPARE/READY/OFFER/ANSWER exchange through existing controlled carrier/offer processor, verify admission revision 7 reaches processing, and reject wrong target, mixed country/target, public descriptor, altered AWG binding, version downgrade and missing ACL. This is transcript evidence, not a real ICE/AWG tunnel.

Final mini-PC checks:

```sh
go test -race -shuffle=on -count=10 ./internal/sessionestablish
go vet ./internal/sessionestablish
```

Both exited 0; final tests completed in 1.756s. Earlier affected runtime and Rendezvous tests passed in 7.715s and 9.941s before the additional descriptor-binding guard. Vet caught a shallow protobuf copy in the new downgrade test; it was replaced with `proto.Clone`, then tests/vet passed. Runtime wiring must select both the private carrier and private responder mode; no user-facing private connection is enabled yet.

## 2026-10-06 — private broker carrier

`NewPrivateBrokerCarrier` revalidates the signed descriptor and locator-bound receipts for the exact target, then uses only direct private owner signaling. Existing route fallback, request deadlines and message/byte budgets are shared without public RPC fallback. The public carrier constructor rejects private candidates. Owner eligibility still comes from authenticated discovery, not a receipt alone.

The real distributed test exercises the carrier after one owner disappears, rejects wrong target and locator, checks opaque signaling through the surviving owner, rejects a replaced registration route and verifies message accounting.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/rendezvous
go vet ./internal/rendezvous
```

Both exited 0; tests completed in 41.894s. No workstation tests or installed-service changes were performed. This is carrier/registry evidence, not private ICE/AWG or user-plane proof. Runtime provider adaptation, private establishment selection and responder wiring remain required.

## 2026-10-06 — private selector provider and runtime establishment wiring

`PrivateCandidateProvider` adapts exact-target lookup to ExitSelector without a country, public candidate acquisition or forwarding cache. It holds one latest candidate, bounded by thirty seconds, descriptor expiry and all receipt expiries. New lookup/exclusion clears the old cache; failed lookup cannot retain a stale route. Resolve requires the unchanged issued candidate and returns a deep copy. Registration replacement changes the route epoch.

The connector now accepts a shared resolver interface and an explicit pinned private target. It rejects other peers, self, public/mixed capability and direct candidates for private connections before transport preparation. Private attempts use `EstablishPrivate`; public attempts retain `Establish`. Exit runtime sets responder private mode whenever private admission policy is configured. ICE, AWG and lease processing remain shared.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/rendezvous ./internal/runtime ./internal/sessionestablish ./internal/exitselector
go vet ./internal/rendezvous ./internal/runtime ./internal/sessionestablish ./internal/exitselector
```

All exited 0: rendezvous 42.204s, runtime 27.241s, sessionestablish 1.354s, selector 1.774s. After adapting the real distributed test to go through the new provider:

```sh
go test -race -shuffle=on -count=5 -run 'PrivateCandidateProvider|PrivateDistributed|BrokeredConnectorTarget' ./internal/rendezvous ./internal/runtime
go test ./cmd/irviod
go vet ./internal/rendezvous ./internal/runtime
```

All exited 0: rendezvous 9.200s, runtime 1.079s, command 0.515s. New cases verify cache expiry/copy isolation, request and candidate target isolation, exclusion, registration replacement and cache invalidation after failed lookup. The real distributed test proves DHT-to-provider-to-private-carrier signaling after an owner loss. These checks do not prove a production private tunnel: product lifecycle construction, private advertisement, API/UI integration and privileged user-plane/security/resource gates remain unfinished. Installed workstation/mini-PC services and production distributors were not changed.

## 2026-10-06 — private Exit descriptor lifecycle

`Node.SetPrivateExitReady` publishes a signed private-only descriptor without country or public Exit CID/provider work. It requires configured deferred Exit and existing session-establish/session-control support; ordinary signed-announcement validation still enforces AWG binding. Public/private activation shares the provider lock and rejects overlapping roles. Private withdrawal removes its capability/feature and stops accepting sessions; configured public country is retained for a later explicit public activation. Country mutation during private hosting is rejected.

Tests use real discovery Nodes and verify signed descriptors for unknown and configured country, idempotent activation, AWG rotation, announcement renewal, withdrawal, explicit public/private transitions and refusal to reactivate after Close. A concurrent public/private activation test requires exactly one successful role. Readiness here is a publication primitive: the future product lifecycle must establish ACL/data-plane readiness before calling it and manage private registrations separately.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/discovery
go vet ./internal/discovery
go test -race -shuffle=on -count=10 -run PrivateExit ./internal/discovery
go vet ./internal/discovery
go test ./internal/runtime ./cmd/irviod
```

All exited 0: full discovery 30.943s; final focused tests 2.615s; runtime 7.234s; command 0.575s. The focused rerun includes the additional concurrent activation case. No privileged tunnel or user-plane proof is claimed, and no installed services were changed.

## 2026-10-06 — distributor runtime private service integration

The existing M11-gated distributor startup now creates a separate private registry/service alongside the public one. It exposes the private support feature only after handler installation and public readiness publication. Private service receives the authenticated DHT owner publisher, no public forwarder or remote sampler. Startup errors close both services and their registries; normal withdrawal removes private readiness and closes private registration/enrollment handlers before releasing the shared country resolver. Public country aggregation still reads only the public registry.

The new runtime test uses real discovery/libp2p Nodes, the existing M11 controller with two independent verifier identities and a controlled inbound challenge fixture. It checks no runtime service before quorum, private capability after readiness, a private descriptor built through the new Node lifecycle, private registration and exact lookup, public country isolation, and registration channel closure after M11 role withdrawal. This is not an external reachability/NAT or private data-plane test.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 -run PrivateDistributorRuntime ./internal/runtime
go test -race -shuffle=on -count=3 ./internal/runtime ./internal/rendezvous ./internal/discovery
go vet ./internal/runtime ./internal/rendezvous ./internal/discovery
go test ./cmd/irviod
```

All exited 0: focused 4.475s; runtime 25.775s; rendezvous 42.262s; discovery 31.297s; command 0.574s. Installed services and production servers were not changed. Desktop private hosting construction, enrollment/import orchestration, Local API/UI and privileged acceptance remain outstanding.

## 2026-10-06 — managed private registration/enrollment teardown

`m12Runtime.StartPrivateExitRegistrations` uses the existing private registration manager with production owner verification and takes ownership of its enrollment endpoint only on successful start. A shared registration mutex serializes start/stop and event-worker accounting. Role mismatch, duplicate start, closed/canceled runtime and private start after readiness withdrawal are rejected. Private stop withdraws its announcement, closes enrollment carriers, unregisters and joins registration events; repeated stop is harmless. Caller still owns data-plane admissions and ACL setup.

The runtime test now also builds an owner-only durable trust store, real TLS enrollment server and managed endpoint. Through an M11-validated distributor, a separate libp2p identity receives PENDING, explicit approval is persisted, and a new exchange reports APPROVED. Stop removes the private advertisement and lookup, and a closed endpoint cannot be re-admitted. Public manager start during private hosting and private restart after withdrawal are rejected. This does not yet demonstrate private tunnel traffic or product/API lifecycle.

Fixture corrections during development: temporary profile permissions set explicitly to 0700, required PublicRole operation timeout supplied, and registration interval shortened only in this test to permit three rapid registrations instead of hitting the production two-registration burst limit. Production validation/rate limits were not weakened.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 -run PrivateDistributorRuntime ./internal/runtime
go test -race -shuffle=on -count=3 ./internal/runtime ./internal/privateaccess ./internal/rendezvous
go vet ./internal/runtime ./internal/privateaccess ./internal/rendezvous
go test ./cmd/irviod
```

All exited 0: focused 4.462s; runtime 27.320s; privateaccess 2.213s; rendezvous 42.188s; command 0.572s. After the final negative role/start assertions, focused race/shuffle ×5 and runtime vet passed again (6.415s). No tests ran on the workstation, no installed services were changed and nothing was published externally.

## 2026-10-06 — daemon hosting access ownership

The internal daemon config now accepts a caller-owned durable private hosting store. Startup binds it to the node identity before constructing the production Exit, creates one policy shared by PREPARE and TunnelLease admission, and builds enrollment against that same store. Private publication starts only after Exit construction; endpoint/announcement rollback occurs if registration startup fails. Daemon cleanup closes admissions without deleting durable approvals or closing the profile owner's store.

This intermediate config supports hosting-only: public upstream, public server role, public country publication and startup protection are rejected. Private upstream plus private hosting remains a required subsequent integration, not a removed feature. Product/API callers do not yet expose this config. The unchanged public path passes nil private admission.

New tests verify foreign identity rejection, denied unknown client, approval visibility in the shared policy, admission cancellation on daemon resource cleanup, durable approval retention and rejection of contradictory config. No privileged daemon startup/private forwarding is claimed by these unit tests.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./internal/runtime ./internal/privateaccess
go vet ./internal/runtime ./internal/privateaccess
go test ./cmd/irviod
go test -race -shuffle=on -count=10 -run PrivateHosting ./internal/runtime
go vet ./internal/runtime
```

All exited 0: runtime 46.971s; privateaccess 2.854s; command 0.526s. The final focused rerun after adding the zero-locator startup guard passed in 1.085s. Tests ran only on the mini-PC; installed applications, production nodes and GitHub were untouched.

## 2026-10-06 — typed coordinator connection intent

Coordinator commands/status now carry `exitselector.Target`. Existing `Connect(country)` delegates to a public-country target; `ConnectTarget` supports exact private PeerID without a fabricated country. Configuration accepts exactly one legacy or typed selector factory, and rejects private intent through the legacy factory before protection or selection starts. Intent is recorded before publishing the initial Protecting event and cleared only after successful disconnect teardown.

The new coordinator test uses controlled protection/network/selector adapters to verify invalid target rejection, unchanged private intent through CONNECTED/recovery/NO_EXIT/recovery, protection remaining active, and clearing intent/protection on disconnect. It also rejects mixed/unknown targets and ambiguous factory config. This is state-machine evidence, not production private upstream connection proof; production client configuration and product intents still need to call the typed path.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/runtime
go vet ./internal/runtime
go test ./cmd/irviod
```

All exited 0: runtime 32.551s; command 0.568s. No workstation tests or installed-service changes.

## 2026-10-06 — production private upstream wiring

ProductionConfig now carries a pinned private PeerID and locator. The M12 runtime supplies one shared private provider to connector, metadata probe and selector, rejects target/locator replacement, and refuses creation after cancellation/close. Production coordinator uses typed factories; private startup and reconnect use ConnectTarget, while the public Connect API refuses a private-configured daemon. Mixed public-country/private-target, public Exit with private upstream, missing locator, invalid PeerID and self-target are rejected. Private upstream may run without hosting or with the existing private hosting/data-plane installer; hosting-only remains unprotected ordinary WAN. The shared Exit reserves the upstream slot for private as well as public upstream.

A negative test exposed that libp2p `peer.ID.Validate` checks nonempty input only. Private target validation now uses `peer.IDFromBytes` at coordinator, daemon, lookup, connector configuration and selector boundaries. This rejects malformed multihash input before network work; public signature/authentication checks were not weakened.

Tests verify allowed/forbidden config combinations, public Connect rejection in a private daemon, shared route-cache identity, refusal to switch target/locator, and private metadata probing without claiming direct RTT. These tests do not prove privileged private forwarding or combined-role anti-multihop behavior; product/API/UI and integrated data-plane gates remain required.

Mini-PC checks after the initial malformed-PeerID test failure was fixed:

```sh
go test -race -shuffle=on -count=3 ./internal/runtime ./internal/rendezvous
go vet ./internal/runtime ./internal/rendezvous
go test ./cmd/irviod
go test -race -shuffle=on -count=10 ./internal/exitselector
go test -race -shuffle=on -count=5 -run 'Private|BrokeredConnectorTarget' ./internal/runtime
go vet ./internal/runtime ./internal/rendezvous ./internal/exitselector
go test ./cmd/irviod
```

All exited 0: runtime 27.546s; rendezvous 42.218s; command 0.571s. Final additional target guards: selector 3.336s; focused runtime 4.479s; command 0.519s. No workstation tests, installed-service changes or external publication.

## 2026-10-06 — private product configuration resolver

The desktop private resolver represents hosting-only, saved private upstream, or both. It obtains the locator exclusively from the durable saved target, rejects missing/removed/self/malformed identities and zero locator, and preserves the original public configuration. Mapping disables public Exit country requirements, uses private hosting only when requested, and enables protection only for upstream intent. Server presets are not silently converted to desktop private mode. OS/bootstrap defaults remain delegated to the existing resolver.

Tests exercise the pure mapping with a real owner-only store and pass each valid combination through runtime config validation. They cover unknown/deleted targets and contradictory server input. The OS resolver wrapper, desktop commands, same-boot private intent and UI are not yet wired/validated end to end; this is a configuration-layer step, not product completion.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./cmd/irviod
go vet ./cmd/irviod
```

Both exited 0; package tests completed in 19.221s. No installed applications, production servers or GitHub artifacts were changed.

## 2026-10-06 — versioned private same-boot intent storage

Session-state v2 stores a canonical private target PeerID and an independent hosting intent without invitation secrets or locator material. Hosting-only has no upstream ConnectIntent; combined mode and private upstream-only are represented explicitly. Version 1 public intent remains readable and is still written by the public API. Contradictory modes, unknown versions and malformed targets are rejected. BeginTeardown replaces every private intent with the existing teardown marker. Both writing and reading enforce a 512-byte limit.

Tests cover all three private intent combinations, reopening the store, teardown/clear, legacy public behavior, rejection of invalid state and preservation of the prior intent after invalid requests. This is storage-level evidence only: desktop commands and private crash recovery are not yet wired, and no reboot or private tunnel is claimed by these tests.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=10 ./internal/productintent ./cmd/irviod
go vet ./internal/productintent ./cmd/irviod
```

Passed: productintent 1.081s; command 37.546s. After adding the pre-write size guard, repeated productintent race/shuffle x10 (1.070s), command tests (0.573s), and vet for both packages; all exited 0. No workstation tests, installed-service changes or external publication.

## 2026-10-06 — desktop private intent lifecycle wiring

The desktop controller now owns a lazily opened private trust store tied to the profile identity, exposes an internal ConnectPrivate operation, and uses the saved-target resolver plus the common daemon startup path. Hosting-only does not request protection; private upstream does. Same-boot startup restores private target/hosting intent into the bounded existing retry loop. Pending private intent prevents idle auto-exit and public mode switching; changing the private target requires full leave. Store ownership ends only after runtime close succeeds, before profile deletion.

Disconnect durably replaces intent with the teardown marker before clearing either desired mode. Incomplete teardown retains DISCONNECTING and refuses another connection until cleanup succeeds. Hosting-only pending startup is not IDLE; a daemon with active private advertisement reports the new internal StateHosting rather than public contribution. This also prevents profile deletion from skipping Disconnect for hosting-only activity. The Local API wire representation and UI presentation of hosting remain part of P08/P09; private commands are not yet exposed through the installed GUI.

Tests cover restoration of all private combinations, protection callbacks, full leave, rejection of cross-mode/target changes, rejection of unsaved targets before network work, exclusive trust-store ownership and release, failed cleanup/retry, and profile deletion clearing hosting intent. The existing real-node private distributor fixture checks the internal hosting status. These checks do not prove successful privileged private startup, real crash adoption, downstream drain, DNS/routes or leak invariants; integrated P10 gates remain outstanding.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./cmd/irviod
go vet ./cmd/irviod
go test -race -shuffle=on -count=3 ./cmd/irviod ./internal/runtime ./internal/localapi
go vet ./cmd/irviod ./internal/runtime ./internal/localapi
```

Initial desktop checks passed in 19.223s. Final checks including hosting status and teardown fencing passed: command 12.077s, runtime 27.974s, Local API 4.550s; vet exited 0. Tests ran only in the isolated mini-PC source tree. No installed services, production distributors, GitHub or release artifacts changed.

## 2026-10-06 — daemon-owned private administration

Added internal desktop commands for redacted private state, signed invitation creation, approval/cancellation, member revocation and saved-target removal. These operations serialize with Connect/Disconnect/profile deletion and reject canceled, closed or unfinished-teardown state. Offline administration does not enable hosting, protection or discovery. The state view omits locator material, invitation secrets and signed links; only explicit invitation creation returns a link. Creating another invitation does not prune consumed receipts needed by a client still polling enrollment status. Capacity remains bounded by the existing store limits.

Runtime publishes its private administrative Exit only after successful Start and withdraws it during resource cleanup. Live member revocation uses ExitServer's admission fence and lease cleanup, not a store-only mutation. Offline revocation persists denial. Removing the current target is refused until full leave. Client-side invitation import/enrollment orchestration, Local API wire exposure and UI remain unfinished.

Fixed repeated approval of an already-authorized PeerID: it now preserves the live grant revision rather than invalidating its active authorization contexts. Regrant after explicit revocation still creates a newer revision. A focused regression covers both cases.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./cmd/irviod ./internal/privateaccess ./internal/runtime
go vet ./cmd/irviod ./internal/privateaccess ./internal/runtime
go test -race -shuffle=on -count=10 -run TestDaemonPrivateRevokeUsesLeaseFence ./internal/runtime
```

All passed: command 12.097s, privateaccess 2.102s, runtime 29.618s; focused daemon revoke 1.210s. The revoke test uses a real TunnelLease lifecycle with a controlled resource revoker: active lease removal, grant cancellation and renewal denial are proven, not real AWG forwarding. Management tests cover redaction, view isolation, signed link validity, pending-only approval, cancellation, offline denial and command guards. No installed services or external artifacts changed.

## 2026-10-06 — durable client enrollment and offline import

Private store v2 adds bounded imported invitations with per-import revision fencing. Existing v1 files remain readable and become v2 on first import; the owned file path and profile-deletion allowlist remain unchanged. Imports verify the signed invitation, reject self/expired introductions and conflicting concurrent introductions for the same host, and never create a saved target before approval. Pending signed links remain only in the owner-only daemon store. Management summaries expose host/name/state/expiry, not links, secrets or locators.

ResumeEnrollment binds the local signing identity to the store, queries status first over pinned-host TLS, and sends the invitation proof only if the host reports DENIED and the invitation is still valid. This supports a lost reply after reservation/approval. Only authenticated APPROVED atomically saves the target and removes pending secret material. Expired pending entries remain readable for status polling/cancellation; they are not accepted as new imports. Cancellation and reimport of the same link changes its revision, so late replies cannot promote a replacement request. Errors leave the stored pending request available for bounded explicit retry.

Desktop internal import/cancel commands and redacted summaries are wired without enabling network participation. The discovery-backed production enrollment dialer, Local API invocation and UI are still outstanding. The generic dialer is constrained by the existing exact-host TLS identity check; a malicious carrier cannot substitute a different endpoint identity.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./internal/privateaccess
go vet ./internal/privateaccess
go test ./cmd/irviod ./internal/runtime
go test -race -shuffle=on -count=3 ./cmd/irviod ./internal/privateaccess
go vet ./cmd/irviod ./internal/privateaccess
go test -race -shuffle=on -count=10 -run TestImported ./internal/privateaccess
```

All passed. Initial privateaccess 3.275s; command/runtime 0.516s/6.950s. Final desktop/privateaccess race checks 12.034s/2.368s, focused imported-enrollment checks including wrong-host TLS 1.859s. Tests use real loopback TCP/TLS, durable store close/reopen, manual host approval, cancellation/reimport during exchange, invalid identities, expired entries and redacted desktop status. No privileged tunnel, production discovery or UI flow is claimed. Installed applications and external releases remain unchanged.

## 2026-10-06 — discovered enrollment carrier and desktop command

Added an enrollment dialer that uses the existing exact PrivateLookup, verifies current owner receipts/routes and tries those routes with a three-second setup budget per owner. The established carrier retains the overall enrollment deadline rather than the short setup deadline. Each exchange re-resolves owners; no public candidate fallback or hidden Exit endpoint is supplied. The inner TLS exchange still pins the invitation host independently of the routing owner. Setup failure can try another owner; a failure after successful carrier setup is returned for explicit retry, not silently retried inside TLS.

Desktop ResumePrivateEnrollment checks for a saved import before network work. It uses an existing running node, or a temporary discovery-only daemon when fully idle and bootstrapped. This temporary daemon has no Exit or user tunnel/protection intent and is closed after the bounded operation. Cleanup failure retains ownership and DISCONNECTING for explicit retry instead of forgetting a live node. An unresolved upstream recovery intent is not replaced by temporary enrollment work. These internal commands are not yet exposed over Local API/UI.

Tests exercise actual libp2p owner lookup, relay and end-to-end TLS with an unavailable owner hint and a usable owner, pending approval and durable target promotion. This specific fixture supplies owner hints through a controlled finder; it is not the complete disjoint-bootstrap DHT enrollment gate. A separate stream test checks stalled setup timeout and verifies that a successful carrier remains usable past its setup deadline. Desktop guard tests reject unknown targets, missing bootstrap and interference with recovery before network startup. Temporary desktop-node cleanup still requires an integrated runtime test.

The first expanded fixture failed at its final relay byte-limit probe because additional exchanges exhausted the default signaling burst. Test-only GET/Signal intervals were reduced to one millisecond for this tightly packed sequence; production admission limits are unchanged.

Mini-PC checks after that fixture correction:

```sh
go test -race -shuffle=on -count=5 -run TestPrivateEnrollment ./internal/rendezvous
go test -race -shuffle=on -count=3 ./cmd/irviod
go vet ./internal/rendezvous ./cmd/irviod
go test -race -shuffle=on -count=3 ./internal/rendezvous
```

All passed: focused enrollment 4.187s, command 12.084s, full Rendezvous 43.027s; vet exited 0. No installed services or external releases were changed.

## 2026-10-06 — Local API v1.6 private contract and desktop adapter

Added GET_PRIVATE_STATE and PRIVATE_COMMAND behind negotiated Local API minor 6 and an explicit HELLO capability. Missing private backends do not advertise support; older negotiated clients cannot bypass the method gate. Commands cover invitation creation/import/approval/cancellation, enrollment resume/cancellation, member revocation, target removal and private connection/hosting-only startup. Existing Unix credential authorization remains the entry gate. Every command rejects irrelevant fields, malformed identities/IDs, control characters, invalid lifetimes, oversized links and unknown protobuf fields before invoking the backend. Errors are sanitized; only explicit invitation creation returns a secret-bearing link.

Private state uses bounded pages (default 32, maximum 64 per collection) with total counts and store revision, retaining the existing 64 KiB frame limit. It omits invitation links/secrets/locators. Desktop read access uses TryLock and does not cancel country discovery; a busy lifecycle returns BUSY rather than blocking this read. Internal StateHosting maps to the new HOSTING wire state, not DISCONNECTED. Go client helpers and Go/Dart generated bindings are updated.

This does not finish P08/P09: long commands still run synchronously in the session dispatcher, combined status/event responsiveness and cancellation need further work, and the Flutter private screen is not implemented. Dart bindings were generated but not analyzed/tested here; no Flutter SDK or prepared UI package configuration was found in the checked mini-PC locations. No UI tests were run on the workstation.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=5 ./internal/localapi ./cmd/irviod
go vet ./internal/localapi ./cmd/irviod
go test -race -shuffle=on -count=10 -run TestDesktopPrivateAPIOverUnixSocketAndPaging ./cmd/irviod
go vet ./cmd/irviod
go test -race -shuffle=on -count=3 ./cmd/irviod ./internal/localapi
go vet ./cmd/irviod ./internal/localapi
```

All passed: initial Local API/command 6.941s/19.351s, real desktop Unix-socket paging test 1.656s; final command/Local API 12.241s/4.569s. Tests cover version/capability negotiation, strict dispatch, unsupported backend, sanitized failures, oversized-response rejection without session loss, HOSTING mapping, real desktop invitation creation/cancellation, 40-target paging, unchanged revision between pages, read redaction and no network side effects. The final read test verifies that status polling neither cancels discovery nor waits behind the lifecycle lock. Installed apps, production nodes and external artifacts remain unchanged.

## 2026-10-06 — asynchronous private requests and lifecycle cancellation

Private mutations now run outside the Local API session read loop, with one globally admitted operation and no waiting mutation queue. A concurrent private mutation returns BUSY across sessions. Lifecycle commands cancel and join private work and fence new private commands until the lifecycle action completes. Session closure cancels its outstanding request and joins its worker; it does not issue Disconnect against an existing VPN. Country-query cancellation is requested before lifecycle work without synchronously waiting ahead of private cancellation, preventing a dependency inversion when discovery is waiting behind the desktop lifecycle mutex.

Desktop Status uses a cached transition snapshot while its lifecycle mutex is held, then resumes reading current runtime status after completion. Connect/Disconnect publish their transitional state into that snapshot. Private state reads still return BUSY during mutation. A desktop-specific DisconnectRetryNeeded hook permits retry of failed cleanup while retaining generic RuntimeController idempotence for a normal in-progress Disconnect.

The first regression run exposed the existing generic Disconnect idempotence expectation after unconditional retry was introduced. The explicit desktop retry hook fixed this without changing the generic controller contract. This step completes the previously missing async session dispatch, but does not prove all P08/P10 gates or implement the UI.

Mini-PC checks after that correction:

```sh
go test -race -shuffle=on -count=5 ./internal/localapi ./cmd/irviod
go vet ./internal/localapi ./cmd/irviod
go test -race -shuffle=on -count=20 -run 'TestPrivateOperation|TestPrivateLifecycleBarrier|TestDisconnectCancelsPrivate' ./internal/localapi
go vet ./internal/localapi ./cmd/irviod
```

All passed: Local API 6.874s, command 19.770s, repeated concurrency tests 1.264s. Unix-socket tests prove same-session status/private reads during a blocked request, global BUSY, cross-session Disconnect cancellation, no runtime Disconnect on GUI socket closure, lifecycle fencing and cancellation ordering with a dependent country query. Desktop tests prove nonblocking status reads and retry of failed protection cleanup through the API. These use controlled runtime/protection callbacks, not an actual VPN SIGKILL/GUI-kill forwarding test. No installed applications, production nodes or external artifacts changed.

## 2026-10-06 — Flutter private API transport and bounded snapshots

LinuxIrvioClient now negotiates Local API v1.6 and exposes a separate PrivateIrvioClient capability. Both negotiated minor and the daemon's explicit private-backend flag are required. State retrieval validates page sizes/count bounds and combines at most four pages per attempt. Revision, local identity, desired target, hosting intent and totals must remain consistent; one full retry is allowed, then BUSY. Returned snapshots are frozen. Private mutations use the existing 50-second product timeout, require accepted/result fields and are never automatically replayed. No invitation links are added to preferences, logs or state polling.

Prepared an isolated Flutter SDK and package cache under `/home/ubuntu/irvio-private-tools` on the mini-PC. System git installation failed because the mini-PC could not resolve its Ubuntu mirror; no DNS/network changes were made. A copied git executable in the isolated tools directory was sufficient for offline Flutter checks. The first offline preparation lacked package hash metadata and UI assets; these were copied and the original lockfile restored before successful `--enforce-lockfile` validation. Initial Dart compilation identified the base GeneratedMessage return type of freeze(); the typed cascade fixes this without a cast.

Checks executed only on the mini-PC in `/home/ubuntu/irvio-private-validation/ui`:

```sh
flutter --no-version-check pub get --offline --enforce-lockfile
flutter --no-version-check test --no-pub test/private_snapshot_test.dart test/linux_irvio_client_test.dart
flutter --no-version-check analyze --no-pub
flutter --no-version-check test --no-pub
```

Results: 16 focused tests passed, analyzer reported no issues, all 98 UI tests passed (12 seconds). New tests cover unsupported daemon negotiation, exact private wire fields, immutable multi-page assembly, revision/hosting changes, bounded retry, invalid counts, truncated pages and empty state. These are Dart/Unix-socket fixture tests, not a real private connection. HOSTING presentation, private navigation/controller/screens, onboarding, approvals and full P09/P10 integration remain outstanding. Installed UI/daemon, production servers and workstation networking were not changed.

## 2026-10-06 — explicit hosting-only UI status

The wire HOSTING status now maps to a distinct Dart state rather than the unknown/error fallback. HOME presents private sharing with a blue indicator and explicitly states that the owner's traffic uses ordinary Internet. It hides the public-country selector during hosting-only activity and does not replace active hosting with bootstrap onboarding. The existing full-leave button and tray Disconnect path remain enabled for this active state; it is not considered an established upstream tunnel. EN/RU/DE labels and the sidebar status are updated.

Mini-PC `flutter --no-version-check analyze --no-pub` passed. The final complete `flutter --no-version-check test --no-pub` passed all 100 tests in 10 seconds; the targeted polish/localization suite also passed all 11 tests. New transport coverage verifies HOSTING mapping. The widget test verifies active hosting labels, no public country/Connect action, blue glow and exactly one Disconnect call. It verifies that command acknowledgement alone does not synthesize DISCONNECTED: the authoritative daemon status event is required. Test development corrected an immutable fixture parameter, event-delivery expectation and test-controller disposal timing; no production lifecycle behavior was changed to satisfy the fixture.

This proves presentation and dispatch in fixtures only. The Private Network screen/controller, approval UX, combined private upstream presentation and real hosting teardown/traffic gates remain to be completed. No installed daemon/UI or production server was updated, and all tests ran exclusively on the mini-PC.

## 2026-10-06 — private UI controller and stale-response fencing

Added PrivateNetworkController, owned by the app controller and attached only to a negotiated, supported current client. Attaching performs no discovery, command or implicit Connect. Explicit reads are coalesced, snapshots frozen, mutations serialized without replay, and a successful mutation refreshes the redacted snapshot. Invitation results are returned directly to the caller and never stored as controller state or preferences. Error state contains only error codes, not daemon-provided strings.

Context epochs discard late reads and command results after daemon replacement, Disconnect, Shutdown or profile deletion. A separate read generation prevents pre-mutation reads replacing later state. Full-leave actions remain available while private work is pending; the daemon owns actual cancellation and cleanup. A public Connect is rejected while a private mutation is in progress. Failed leave may reattach management to the still-available daemon; no automatic Connect is issued.

Mini-PC checks: focused `flutter --no-version-check test --no-pub test/private_network_controller_test.dart` passed 7 tests; final `flutter --no-version-check analyze --no-pub` reported no issues and full `flutter --no-version-check test --no-pub` passed 107 tests in 10 seconds. Coverage includes coalesced reads, late profile responses, single mutation dispatch, discarded late invitation, code-only errors and app-level Disconnect/Shutdown during pending private work. These use controlled clients, not real encrypted enrollment or forwarding. Private navigation/screens, mutation UX and integrated P09/P10 proof are still pending. No installed applications or production servers changed.

## 2026-10-06 — initial Private Network screen

Added a dedicated sidebar destination and scrollable private management screen. It exposes text invitation import, explicit enrollment check/cancel, saved-target Connect/remove, optional concurrent private hosting, hosting-only startup, one-hour invitation creation, owner approval/cancel and member revocation. Own PeerID is visible for out-of-band comparison; Connect sends the saved exact PeerID, never public country selection. The screen displays the app's authoritative status and full Disconnect action. New labels are available in English, Russian and German.

Created invitations appear in an ephemeral dialog as QR/link with explicit Copy. A private context revision change hides the QR/link and disables copying the invalidated result. Import clears the input only after successful daemon acknowledgement; it does not implicitly enroll, CONNECT or host. No invitation storage was added to preferences. Screen construction requests only a redacted state read.

Mini-PC: `flutter --no-version-check analyze --no-pub` passed; 7 new screen tests passed; final `flutter --no-version-check test --no-pub` passed all 114 UI tests in 13 seconds. Coverage includes unsupported daemon messaging, import without Connect, exact target dispatch, invitation concealment after context replacement and default-window rendering/scrolling in all three languages. The first layout test incorrectly used ensureVisible for a not-yet-built lazy list item; scrollUntilVisible corrected the fixture. Existing public UI tests remained green.

P09 is NOT complete: QR-file import/save, clearer localized enrollment/expiry/error states, confirmation/approval edge cases, combined private upstream HOME/tray presentation, refresh after daemon reconnection and real two-user onboarding remain outstanding. P10 real tunnel/security/lifecycle gates and P11 preview packaging also remain pending. Tests here use fake command responses and do not establish actual authorization or traffic proof. No installed applications or production servers were updated.

## 2026-10-06 — private QR import/export and bounded image decoding

Private invitations can now be loaded from a PNG/JPEG file or explicitly exported as a PNG through the file picker. Image import only fills the text field; it does not dispatch enrollment, Connect or hosting. Decoding/encoding runs through Flutter compute rather than on the UI isolate. File input is streamed with a 10 MiB bound; async results and export are fenced against profile/API-context replacement. An invalidated invitation dialog exposes neither QR, link nor Save button. Exported images intentionally contain invitation secret material and must be treated as sensitive user-selected files; the daemon remains responsible for cryptographic validation and approval.

Shared QR decoding now preflights dimensions (maximum 4096 per axis), rejects multiple frames and limits accepted image formats to PNG/JPEG before pixel decoding. Bootstrap/private schemes remain separate. Private URI encoding is bounded to 2048 ASCII base64url-form characters with exact v1 prefix and complete-match validation; this is a format check, not signature verification. Existing bootstrap QR regression remains green.

Mini-PC analyzer passed after adding an explicit mounted guard for the post-save message. Full UI suite passed 118 tests in 14 seconds. Following a final strict trailing-newline validation refinement, both QR suites passed all 7 tests and analysis again reported no issues. Tests cover PNG/JPEG roundtrip, opaque quiet zone, cross-scheme rejection, malformed/oversized input, excessive dimensions and removal of the Save action after context invalidation. Native file-picker interaction and actual user-selected file writing are not yet exercised by these fixtures; they remain part of the mini-PC end-to-end onboarding gate. Localized enrollment/expiry/error states, approval edge cases and combined upstream UI still require further work. No installed apps or production services changed.

## 2026-10-06 — localized private states and explicit access confirmation

Private state labels now distinguish imported, pending owner approval, unused, consumed, denied and unknown records in EN/RU/DE. Expiry is displayed in local date/time, with invalid timestamps handled without DateTime overflow. Expired enrollment can still be checked for an existing approval, matching the daemon's status-only recovery behavior; UI wall-clock display does not override authoritative daemon validation. Error codes receive localized bounded explanations without displaying daemon error text.

Approval and revocation require a confirmation dialog showing the exact client PeerID. Both the UI connection-context epoch and private snapshot revision must still match when the user confirms; otherwise no command is dispatched. The visible private screen refreshes redacted state every five seconds, coalesced by the controller and suspended during mutations, and cancels its timer on disposal. This also reloads state after a daemon context is replaced, without automatically resuming enrollment or connecting.

Mini-PC `flutter --no-version-check analyze --no-pub` passed after lint-only brace corrections. The final full `flutter --no-version-check test --no-pub` passed 124 tests in 14 seconds. Added cases cover cancel/confirm, profile replacement, revision replacement, exact member revocation and automatic redacted-state reload without commands. These are widget fixtures; real owner approval, revocation/forwarding, native file dialogs, private upstream HOME/tray presentation and full P10/P11 gates remain outstanding. No installed applications or production nodes changed.

## 2026-10-06 — authoritative private intent in status, HOME and tray

Added private_target_peer_id and private_hosting_wanted to the local v1.6 Status protobuf and regenerated Go/Dart bindings. This is a Local API addition, not a network wire-protocol change. Desktop status projects durable desired private intent, including recovery and cached startup status; full leave clears it. The mapper omits public country/exit-country for a private target. Hosting-wanted is explicitly intent, not a claim of readiness.

Flutter status now carries those fields directly. HOME hides public country controls for private participation and shows the pinned identity; private NO_EXIT/RECOVERING keeps the full Disconnect action, not public Connect. Public Connect is blocked while private intent remains. Private NO_EXIT no longer triggers a forced country requery. Tray labels and notifications identify the private mode without falling back to a previously selected country or claiming migration to a different peer. Tray refresh signatures include target and hosting intent.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/localapi ./cmd/irviod
go vet ./internal/localapi ./cmd/irviod
flutter --no-version-check analyze --no-pub
flutter --no-version-check test --no-pub
```

Go passed (Local API 4.569s, command 12.230s); vet passed. After final UI guard/wording and lint corrections, analyzer reported no issues and all 127 UI tests passed in 14 seconds. Added mapper/lifecycle assertions cover intent preservation and full-leave clearing; UI checks cover wire mapping, connected/recovering/NO_EXIT HOME dispatch, hidden public country and private tray labels. These are controlled-status tests, not an actual restored private tunnel. P09 real two-user/native-file-picker proof, P10 integrated networking/security/resource gates and P11 packaging remain outstanding. No installed applications or production nodes changed.

## 2026-10-06 — first real private enrollment, forwarding and revocation lab

Added `TestPrivateRuntimeEnrollmentTrafficAndRevocation` under the existing privileged M4b harness. Eight independent node namespaces plus a router contain three verifier-only anchors, two production server/Rendezvous nodes, a private host, a private client and a traffic sink. All daemons use the same current production binary. The host and client receive only a bootstrap bundle containing the two full servers; no private owner endpoint, Exit endpoint or ACL is injected. Invitations, enrollment, owner approval, Connect and revoke use the real Local API. Fresh identities/state are created for each repetition and removed by scoped lab cleanup.

The mini-PC's SSH mount namespace had no cgroup2 mount, although its processes belonged to real systemd cgroups. The first attempt stopped at the host-before check (`52f3266b`) before creating nodes. Subsequent runners used `sudo unshare --mount --propagation private`, mounted cgroup2 within that isolated namespace and ran the prebuilt test executable; the host mount configuration was not changed. The existing DNS launcher substitutes resolvectl only inside each desktop's isolated mount namespace. This is not a system-resolved restoration proof.

The first networking attempt (`5f353695`) exposed incomplete initial owner discovery being mapped to generic INTERNAL by Local API. Added a sanitized NOT_READY mapping for `rendezvous.ErrPrivateLookupIncomplete`, with a focused regression retaining error-detail secrecy. Explicit enrollment retries in the harness are bounded to five attempts with two-second waits and are logged; the daemon/UI does not silently replay mutations. Successful runs needed one initial retry while private owner provider publication converged.

The next attempt (`f0029f75`) completed real routed enrollment but stayed NO_NETWORK. The harness had inherited a bundle whose first three entries were verifier-only anchors, not full product servers: they lacked the underlay challenge service and occupied the three configured probe slots. Restricted the client bundle to the two actual servers. No underlay, quorum, private authorization or protection semantics were relaxed. Both failed networking attempts completed with host_integrity=true.

The corrected initial HTTP-only smoke passed in 19.02s (`1030da50`). The final expanded test passed three independent repetitions in 19.99s, 20.07s and 20.03s; artifacts on the mini-PC: `/tmp/hydra-m4b-dbf2057e`. TestMain returned exit 0, including its host-before/after equality and run-specific namespace checks. A subsequent namespace listing contained only the pre-existing `hydra-mgmt`, not any lab namespace.

Verified in each final repetition:

- Pending enrollment identifies the authenticated client, does not add a saved target and rejects Connect before approval.
- Owner approval and a second real encrypted enrollment exchange create the saved exact target.
- Private Connect reaches CONNECTED with the pinned PeerID and no public country intent.
- Closing/reopening the API session preserves the established tunnel and forwarding; this is an API-client disconnect test, not a real Flutter process-kill test.
- Public country lookup still returns NL and does not expose the private host's synthetic RU country.
- HTTP and UDP replies report the private host's egress address; explicit DNS UDP/TCP queries and ICMP succeed through the connection.
- Revoke completes and a subsequent HTTP probe cannot pass; an independent postrouting counter sees zero direct client-WAN packets to the traffic sink before Disconnect.
- Explicit full leave clears both private intents/contribution and restores successful HTTP through the client's ordinary WAN address.

Mini-PC checks:

```sh
go test -race -shuffle=on -count=3 ./internal/localapi ./cmd/irviod
go vet ./internal/localapi ./cmd/irviod ./test/netns/m4b
# Prebuilt as ubuntu; executed as root in the isolated mount/netns lab:
netns.test -test.run '^TestPrivateRuntimeEnrollmentTrafficAndRevocation$' -test.v -test.count 3 -test.timeout 10m
```

Race suites passed (Local API 4.555s, command 12.238s); vet passed. The runner uses `HYDRA_M4B_PRIVILEGED=1` and absolute helper paths in `HYDRA_M4B_HYDRAD`, `HYDRA_M4B_LAB_DNS_LAUNCHER`, `HYDRA_M4B_RFC5780`, `HYDRA_M7_INTERNET`. Helpers and runner are built by the non-root mini-PC user, never by sudo.

Binary SHA-256: `dd42420938f21e34c66ee67f1f23d1a2bbd942ae1a130ff56a3b145afa78b9db`; expanded runner SHA-256: `fdaf8fd9643a6f3d4b9665cd25bab399fbb46ac7ee0d3a18deab18d82151ec13`.

Limitations: the WAN oracle covers the exercised IPv4 sink, not arbitrary destinations/IPv6 or every packet throughout every lifecycle. The negative post-revoke probe and counter do not replace all admission/renewal race tests. Public candidate/DHT exclusion is not exhaustively proven by the country assertion. Owner sets are not disjoint in this smoke; migration, NAT variants/remap, combined upstream+hosting anti-multihop, crash/boot, real GUI/native dialogs, 100-cycle resource proof and broader affected regressions remain P09/P10 work. P11 packaging remains outstanding. No installed applications, production servers, workstation networking or published releases were changed.

## 2026-10-06 — disjoint owners and complete private registration migration

Added `TestPrivateRuntimeDisjointOwnersAndOwnerLoss`, sharing the preceding smoke harness. The extended topology contains three full production server/Rendezvous processes in independent namespaces, in addition to the three verifier anchors and two desktop processes. The client's imported bootstrap contains exactly one server, B1. The host's TCP access to B1 is rejected in its own namespace before hosting starts. Its registration log must prove exactly the other two server identities as active owners. No owner endpoint is injected into the client, invitation or ACL.

The client completes real encrypted enrollment, approval, pinned ICE/AWG/TunnelLease establishment and HTTP/UDP/DNS/ICMP through those disjoint owners. Then the first owner process is terminated: established HTTP still works, and explicit client Disconnect/Connect creates a fresh runtime which discovers the remaining owner. Next the second owner process is terminated, the host-to-B1 TCP restriction is removed, and the harness waits for a registration snapshot containing only B1. Another full client Disconnect/Connect must obtain a fresh route through B1 and send HTTP from the same pinned host's egress. The normal revocation, public-country isolation, API reconnect, scoped WAN counter and final full-leave checks remain in the scenario.

An initial one-owner-loss version passed in 20.69s (`/tmp/hydra-m4b-c11b595a`). The first complete-migration version accidentally coupled owner loss with loss of every host STUN seed: the host bundle contained only its two original owners. Host registration migrated, but new connection attempts ended in NO_EXIT with signaling stream resets; no new host ICE_SRFLX_REFRESHED/ICE_SESSION_PREPARED followed the loss. All three repetitions reproduced that outcome (`/tmp/hydra-m4b-1f7ddc84`, exit 1, host_integrity=true). The three basic smoke repetitions in that same batch passed in 19.95s, 19.92s and 20.13s.

Source inspection confirms that production ICE Prepare requires a fresh STUN observation and returns the refresh error rather than reusing detached SRFLX. To isolate registration-owner migration, the final host bundle includes B1 as well, and the initial partition blocks only TCP; B1 UDP/3478 remains available throughout. Thus private registration owners are still disjoint from the client's sole bootstrap, while at least one normal bootstrap-derived STUN endpoint survives. No protocol, authorization, selector, STUN client or Core timeout was modified. The all-STUN-unavailable run is preserved as an infrastructure-dependency observation, not relabelled a passing migration test or a confirmed private discovery bug. This does not claim that the product discovers replacement STUN seeds beyond its configured pool.

Final mini-PC command, using the previously documented privileged environment and isolated cgroup2 mount:

```sh
netns-next.test -test.run '^TestPrivateRuntimeDisjointOwnersAndOwnerLoss$' -test.v -test.count 3 -test.timeout 10m
```

All three final repetitions passed: 36.80s, 31.94s and 32.15s (whole-scenario durations, not connection latency). Results: `/tmp/hydra-m4b-13fe840e/results.json`, exit_code=0, host_integrity=true. Post-run namespace listing again contained only pre-existing `hydra-mgmt`. `go vet ./test/netns/m4b` passed on the mini-PC; `git diff --check` passed. Production daemon SHA-256 remained `dd42420938f21e34c66ee67f1f23d1a2bbd942ae1a130ff56a3b145afa78b9db`; final runner SHA-256 `ec2d948ad0a6d6301cf98a3f7658366837199ad40297b7e00691c610432cf26c`.

This is real cross-distributor lookup and complete owner replacement proof, with explicit reconnect after migration. It does not yet prove automatic recovery of a failed data plane, owner-process restart with the same identity, forged-provider behavior in a production lab, NAT variants, same-boot crash/reboot or 100 lifecycle cycles. Real GUI/native file dialogs and local preview packaging remain outstanding. No production source changes, installed-app updates, GitHub pushes or production-server operations were made in this step.

## 2026-10-06 — private lifecycle resource gate in progress

Added a dedicated `TestPrivateRuntimeLifecycle100` runner scenario. It repeatedly tears down and restarts both private hosting and the pinned client without restarting either daemon process, exercises real HTTP every cycle, checks authoritative IDLE/traffic statistics, and compares links, addresses, IPv4/IPv6 routes, policy rules and nftables against a pre-first-CONNECT namespace baseline. Per-cycle JSON records FD, pipes, threads, socket FDs, AF_NETLINK socket FDs and RSS. Resource bounds retain room for Go/thread/cache warmup while rejecting netlink accumulation. The independent scoped WAN counter and final revocation/full-leave checks from the smoke scenario remain present.

Harness corrections before the full run:

- `/tmp/hydra-m4b-cdcce236` stopped at cycle 1 because the original baseline preceded IPv6 DAD completion. The exact diff contained the transition from a tentative link-local address to its ready address/local route; no extra IRVIO route or nft table was present. Baseline capture now waits for DAD readiness rather than ignoring address/route differences. Host integrity passed.
- `/tmp/hydra-m4b-3237cb62` completed cycle 1, then timed out waiting for the next hosting registration. It issued three starts inside the default RegisterBurst=2 / RegisterInterval=10s budget and received repair failures/backoff. The resource campaign now waits 10.2 seconds before each hosting restart, including the final reconnect; production limits/timeouts are unchanged. Host integrity passed.

The paced run `/tmp/hydra-m4b-b5155a31` terminated at cycle 19 on its baseline+64 MiB RSS bound: host RSS increased from 42476 to 109864 KiB. FD=9, pipes=2, sockets=2 and netlink=0 remained unchanged; threads increased from 13 to 14. This is not a 100-cycle PASS and does not by itself establish a memory leak. Runner SHA-256 was `cb34608b45366d9b725db62e73ea09bd563b7dc507e3317a92aeb07bab9bd15a`. The next diagnostic run records `GODEBUG=gctrace=1` and uses an absolute 512 MiB RSS lab safety ceiling; passing that ceiling is not leak acceptance and still requires analysis of the full GC/RSS trend. Production daemon and timeouts are unchanged.

Before crash testing, inspection found that the original launcher isolated mounts but left `/run/irvio/session-state` shared between desktop nodes. Earlier live traffic proofs do not establish independent intent persistence or crash adoption. The launcher now optionally binds a distinct owner-only lab runtime directory over `/run` in each child's private mount namespace. The first isolated run (`/tmp/hydra-m4b-4004472f`) failed immediately because the harness had not provisioned its `/run/irvio` parent; the daemon logged `inspect IRVIO runtime directory: lstat /run/irvio: no such file or directory`. The harness now creates that parent with mode 0700 before starting each desktop. No production persistence checks were relaxed.

The corrected isolated smoke passed in 20.07s (`/tmp/hydra-m4b-5de6fa56`, process exit 0), including routed enrollment, pinned traffic and revocation. A fresh 100-cycle run is required with this isolation; neither the 19-cycle diagnostic nor the smoke closes that gate.

The isolated 100-cycle diagnostic `/tmp/hydra-m4b-9b5478cd` completed PASS in 1189.86s, exit_code=0 and host_integrity=true (2026-10-06 16:46:11Z–17:06:01Z), with GC tracing inherited by the production child processes. Runner SHA-256: `af9a82fc12931c5617369ede0f08a5a8b1593e31277da7538822ad1c8822a027`; isolated launcher SHA-256: `4b47314ee8249fab430d681d0bfdd5d2775cc0f50644c32ff0229e381bb124cb`. Production binary SHA-256 remained `dd42420938f21e34c66ee67f1f23d1a2bbd942ae1a130ff56a3b145afa78b9db` throughout; it predates the later RELEASE result-handling fix.

All 101 idle resource samples (cycle 0 through 100) have FD=9, pipes=2, socket FDs=2 and AF_NETLINK FDs=0 for both daemons. Host threads went from 12 to 14, client from 11 to 12 and stayed bounded. Every cycle passed the namespace baseline comparison and real host-egress HTTP check; final revocation, scoped WAN counter and normal-WAN restoration passed.

| RSS (KiB) | Initial | Median | Maximum | Last | Median cycles 20–40 | Median cycles 80–100 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Private host | 43104 | 121728 | 124600 | 122184 | 119672 | 122300 |
| Private client | 43792 | 95016 | 122160 | 105076 | 95180 | 96140 |

GC post-collection heap reached at most 46 MB on the host and 44 MB on the client; last values were 44/43 MB, and medians of the last 20 GC observations were 45/41.5 MB. RSS warmed up substantially but did not exhibit monotonic growth across the campaign; the late-window increase versus cycles 20–40 was approximately 2.2%/1.0%. No generation FD/netlink/pipe/socket leak signature was observed. This evidence supports bounded resource behavior over these 100 cycles, not an unlimited-duration leak proof. DNS restoration is still outside this launcher’s scope. Client logs contained 19 unconfirmed-release warnings, so successful cleanup must not be read as proof that every RELEASE acknowledgement completed cleanly.

Prepared `TestPrivateRuntimeSameBootCrashRecovery` for a separate privileged run after the lifecycle campaign: real SIGKILL of a CONNECTED private client, unchanged isolated recovery intent, retained kernel protection, failed user probe and zero scoped WAN counter while dead, followed by restart using the same profile/runtime directory without bootstrap import or CONNECT. It requires automatic CONNECTED to the same pinned PeerID and real host-egress traffic before the existing revoke/full-leave checks. The common final leave also checks removal of both nodes' recovery-intent files. This scenario is not yet executed; compiling/vetting it is not crash-recovery evidence. The currently running lifecycle binary is not replaced by these subsequent harness edits.

At cycle 40 of the isolated run, both nodes still had FD=9, pipes=2, sockets=2 and netlink=0; host RSS was 119620 KiB and client RSS 91596 KiB (both below their cycle-30 samples). Some client disconnects log `TUNNEL_RELEASE_UNCONFIRMED` while the host logs data-plane lease revocation. This observation is not yet attributed to a particular transport error. A separate three-cycle `TestPrivateRuntimeDisconnectRelease` has been prepared: after client Disconnect it checks downstream count reaches zero within ten seconds while the host remains HOSTING, before allowing host teardown. This prevents stopping the host from masking a retained downstream lease. It is not yet executed and is not part of the already-running frozen 100-cycle runner.

## 2026-10-06 — acknowledged RELEASE versus channel-close failure

Targeted inspection found an independently reproducible result-handling defect: after receiving authenticated RELEASE_ACCEPTED, `sessioncontrol.Client.exchange` returned the error from closing its transport. `ReconnectingClient.Release` consequently retried an already acknowledged release and could exhaust its budget rebinding to the removed lease. This is not yet proven to explain every warning in the running lifecycle lab.

`TestAcceptedReleaseDoesNotRetryOnTransportCloseError` uses a real authenticated client/server exchange and injects only the subsequent connection-close error. Before the fix it failed in 0.51s with the injected error, short write and context deadline exceeded. Commit `bc48e5d` preserves the accepted result while still closing the channel; missing/invalid responses remain errors. No authorization, protocol or lease lifetime was changed.

Mini-PC checks passed: the new regression plus response-loss and silent-blackhole release tests, count=10 (4.173s); `go test -race -shuffle=on -count=3 ./internal/sessioncontrol ./internal/runtime` (4.133s / 30.009s); vet for both packages. New lab binary is built separately as `irviod-releasefix`, SHA-256 `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`. Pending crash/release runner SHA-256: `f92c49916fb7a2fcaa7bf659ae20b58d3898963fde68813b40a2dfc172131e63`. The already-running 100-cycle binary was not replaced. End-to-end checks on the new binary remain pending; nothing was published or installed onto production nodes.

Prepared `TestPrivateRuntimeClientEIMNAT` in a subsequent runner: the client router applies one fixed external UDP mapping, translates other outbound traffic, and drops inbound traffic to the client's inner address unless conntrack identifies a reply. The normal private enrollment/traffic/revoke flow must run unchanged, and the host ICE log must show the exact mapped client endpoint, not the inner address. Final explicit Disconnect must restore the translated ordinary WAN path. This is a pending client-NAT test, not evidence for host NAT, APDM, shared NAT or remap. The new runner compiles and passes vet on the mini-PC; privileged execution waits for completion of the existing isolated lifecycle run.

## 2026-10-06 — Linux preview UI build

Built the current UI on the mini-PC using the offline Flutter SDK/cache. The initial `--no-pub` build failed because plugin symlinks still referenced the source environment; `flutter --no-version-check pub get --offline` regenerated those paths without changing the locked dependency versions. The subsequent `flutter --no-version-check build linux --release --no-pub` passed (only the existing tray plugin's deprecated `app_indicator_new` warning). `pubspec.lock` SHA-256 matches the workstation source: `1cba490f65cfea4cdbf8d1b02fc0ec0b44713b1532bbade176266509698a8b24`.

Binary: `/home/ubuntu/irvio-private-validation/ui/build/linux/x64/release/bundle/irvio`, SHA-256 `a9806f3f3cba5bdc6499eed295eefde268f64f4a78f3b03d09fbe6799ae58770`. No missing dynamic libraries were reported by ldd. This is a build result only: no real two-person GUI, native picker, tray or session-restart acceptance is claimed, and the installed mini-PC applications were not replaced.

## 2026-10-06 — real private crash/release gates and initial NAT failure

Executed the three prepared scenarios sequentially on the mini-PC after the 100-cycle runner finished. New production binary SHA-256 `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`; runner `private-next.test` SHA-256 `8bf6f1bd65a9e5c0fc7a3a26b2a324e06a66fb404f12496f8715a964dbad7e8a`. Results: `/tmp/hydra-m4b-47d015f6`, 17:06:31Z–17:09:21Z, aggregate exit_code=1, host_integrity=true.

- `TestPrivateRuntimeSameBootCrashRecovery`: PASS, 25.61s. Actual SIGKILL after CONNECTED, unchanged per-node private intent, retained protection table, unsuccessful user probe and zero scoped WAN leak counter while daemon was dead. Same profile/runtime restart restored the same identity and private target without a new CONNECT or bootstrap import, and HTTP exited through the authorized host. Subsequent revoke, full leave, removal of both intent files and ordinary WAN restoration passed. This does not prove fresh-boot semantics, hosting-process crash, or continuous traffic during every instant of recovery.
- `TestPrivateRuntimeDisconnectRelease`: PASS, 65.15s. Three real reconnect/traffic/disconnect cycles additionally required downstream client count to reach zero within ten seconds while the Exit was still HOSTING, before the harness stopped the host. Resource/network checks and final revoke/full leave passed. This is stronger release-cleanup evidence than stopping both processes together.
- `TestPrivateRuntimeClientEIMNAT`: FAIL, 78.76s; investigation open. The private enrollment was approved, fresh STUN logged `198.18.57.2:45917 -> 198.18.90.2:42000`, and signaling reached both peers. Client ICE started at 20:08:10 local time and failed by 20:08:12 with context deadline exceeded / connecting canceled by caller; status remained NO_EXIT with the correct private target. Router evidence confirms 19 UDP SNAT first packets and 16 packets dropped by the no-direct-bypass rule. This is not yet classified as a production bug or lab artifact. No timeout was increased and the failure was not converted to a passing NAT result.

Remaining gates still include NAT/remap/shared-NAT and combined hosting, fresh boot/hosting crash, real GUI/native dialogs, system DNS, broader affected privileged regressions, and preview package installation. The full private-mode goal remains incomplete.

NAT forensic follow-up: `/tmp/hydra-m4b-2a92ed44` reproduced the failure with bounded UDP header capture (78.94s). The original client shared the public bridge; separating its router port and gateway from that bridge was necessary topology hygiene but did not resolve the failure: `/tmp/hydra-m4b-b29816d3` failed all three repetitions (78.95/78.85/78.89s). Packet headers show repeated ICE requests entering the router without a completed translated exchange. Thus L2 isolation alone is not an established root-cause fix.

The final harness revision adds the inbound half of the fixed EIM mapping, derived solely from the client's observed local UDP port, while retaining APDF filtering and the requirement that host ICE selects the external mapped endpoint. The production binary and all production budgets remain unchanged. That revision's runner SHA-256 is `24ee3fda784145ce72dd7fec5cbc8bf0de9fb49646db603832f6cf65bf79e851`. No private peer/owner/Exit endpoint is injected into either daemon by this NAT emulation.

All three corrected NAT repetitions passed: 21.37s, 20.16s, 20.14s; `/tmp/hydra-m4b-b50ba4e3`, exit_code=0, host_integrity=true. Each required authenticated enrollment, pinned connection, host-observed ICE endpoint `198.18.90.2:42000`, real HTTP/UDP/DNS/ICMP, revocation with zero scoped user-WAN counter, cleared intent and translated normal WAN after full leave. Both failed forensic runs also have host_integrity=true. After the batch, only the pre-existing `hydra-mgmt` namespace remained.

The earlier NAT failure is classified as LAB/HARNESS ARTIFACT: the fixed-SNAT model lacked the matching inbound mapping and did not establish the intended viable NAT path. Routing isolation alone was insufficient; completing the bidirectional mapping fixed all three repetitions on the identical production binary, without changing ICE budgets. This closes only the client EIM/APDF-to-public-private-host case; host NAT, shared NAT, APDM and remap remain unproven.

## 2026-10-06 — private client NAT-remap with ordinary traffic

Added `TestPrivateRuntimeClientNATRemap`. After real pinned traffic, the harness atomically changes the client's bidirectional UDP mapping from `198.18.90.2:42000` to `:42001`, then invalidates only UDP conntrack entries whose original/reply source is the internal client. The helper's existing full-flush mode remains available to earlier tests, but this scenario preserves other peers and client TCP control connections. Three client UDP entries were deleted in each observed run.

No manual CONNECT or bootstrap update follows the fault. Acceptance requires fresh ICE_SRFLX_REFRESHED with port 42001, host-observed ICE selection of that endpoint, CONNECTED with the original private target and no country intent, and real HTTP through the same authorized Exit. The first diagnostic passed in 114.12s (`/tmp/hydra-m4b-417bad62`, host_integrity=true), with remap-to-restored-traffic 93.857s.

The stronger repeat adds a bounded two-minute ordinary UDP stream throughout the fault/recovery interval. The helper rejects SO_MARK/device binding, succeeds only for the expected observed egress, reports every attempt as success/failure/wrong egress and cannot signal readiness before a successful pre-fault user probe. Its positive and wrong-egress tests and configuration-rejection tests passed `go test -race -count=10 ./test/cmd/irvio-test-internet` on the mini-PC (2.122s).

The stream run passed in 114.15s (`/tmp/hydra-m4b-a99f1dc4`, exit_code=0, host_integrity=true). Mapping recovery took 93.861s. The stream recorded 1837 attempts, 637 delivered responses from the private host, 1200 failures/timeouts and zero wrong-egress responses. The independent client physical-WAN counter stayed zero; revoke/full leave and intent cleanup passed. Only pre-existing `hydra-mgmt` remained after cleanup. These are IPv4 observations for the chosen sink, not an IPv6/global traffic proof.

Frozen production binary: `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1` (unchanged from the preceding gates). Runner SHA-256 `b026d1bfcea8b34124c74e425c9974460e43b0652a2cb2298ebdb4026199b953`; stream helper `903fa232724edfe8c7d183f2cf3210445873bca52cb863306ed222cce18d8ee4`; scoped conntrack helper `38f432d2fec2e290c71d808d70f2c8012f86d01dbcc00a933f2faf9a413d12ae`.

Timing observation remains open: both runs recovered in approximately 94 seconds. Logs put fresh SRFLX, subsequent ICE and direct BIND together near the end; they do not yet expose a full monotonic decomposition of detection/retry/suppression. Existing selector code has a 60-second AvoidDuration, but attributing the whole delay to it is not yet proven. No production timeout or suppression policy was changed. This passes the exercised automatic pinned-remap correctness path, not a latency SLA, and does not close host-side/shared/APDM NAT or the other outstanding GUI/boot/combined-hosting/package gates.

While this lab ran, the following checks completed exclusively on the mini-PC:

```sh
go test -p 2 -race -shuffle=on -count=3 ./internal/privateaccess ./internal/rendezvous ./internal/exitselector ./internal/sessionestablish ./internal/runtime ./internal/productintent ./internal/localapi ./cmd/irviod
```

All passed: privateaccess 2.413s, rendezvous 43.366s, exitselector 1.717s, sessionestablish 1.270s, runtime 27.855s, productintent 1.040s, localapi 4.540s, irviod 12.237s. These package tests do not substitute for privileged NAT/crash/GUI gates. Actual system DNS restoration remains unproven by this lab because the existing namespace launcher substitutes resolvectl.

## 2026-10-06 — combined private upstream and hosting

Added `TestPrivateRuntimeCombinedHostingAndUpstream`: three independent desktop identities, two real public Rendezvous servers and the existing three independent M11 verifier anchors. The intermediate desktop enrolls with its private upstream through real routed invitation/approval, then starts the combined pinned-upstream plus hosting intent. The downstream desktop independently enrolls with that intermediate node. Neither enrollment permits CONNECT before explicit approval. No peer endpoint or private owner route is injected by the harness.

The intermediate node's HTTP/UDP exits through the upstream's WAN. Downstream HTTP/UDP exits through the intermediate node's physical WAN; downstream DNS over UDP/TCP and ICMP also succeed. The upstream Exit's `masquerade-ipv4` counter is unchanged during those downstream-only probes, providing an independent anti-multihop oracle. Public country results remain NL; the private RU intermediate and DE upstream do not appear. Revoking the downstream member blocks its traffic with zero scoped IPv4 user-WAN counter, while the intermediate remains CONNECTED to its exact upstream and can still send HTTP through it.

Full Disconnect checks all three nodes: no private intent or contribution, no active traffic/downstream clients, no recovery-intent file, ordinary WAN restored, and exact normalized baseline equality for interfaces, addresses, IPv4/IPv6 routes/rules and nftables. This is not a real system-DNS restoration proof: the existing isolated launcher still substitutes resolvectl. No continuous IPv6 or arbitrary-destination leak claim is made.

Mini-PC results:

- Initial combined smoke: PASS 30.64s, `/tmp/hydra-m4b-798b6179`, exit_code=0, host_integrity=true.
- Strengthened combined test, three repetitions: PASS 26.98s / 26.71s / 27.16s.
- Existing enrollment/traffic/revocation test after sharing the enrollment helper: PASS 20.16s / 20.13s / 20.14s.
- Six-test batch: `/tmp/hydra-m4b-30a37c0b`, 17:43:37Z–17:45:58Z, exit_code=0, host_integrity=true; mini-PC vet for `./test/netns/m4b` passed.

Production daemon remains unchanged, SHA-256 `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`. Strengthened runner `private-combined.test`: `39887a4bd7af24d4abbbf0fa85374fd2ea5ca684f809f3200b527665fe96a5dd`. The initial smoke runner was `e5473ac9e9e10b5757bbbf943a84f58dc3ea07c735db427cf6440bc275da8da5`.

The unchanged legacy public `TestM12_IrviodDualRoleMaintainsIndependentTunnelControl` initially stopped before node-A readiness in 1.06s: `install accepted-control bypass ownership: cgroup v2 control bypass is unsupported`. Evidence `/tmp/hydra-m4b-7813974c`, exit_code=1, host_integrity=true; only pre-existing `hydra-mgmt` remained. Unlike the private launcher, this older scenario does not restore the cgroup2 mount hidden by iproute2's per-process sysfs remount. A separate lab-only mount wrapper is used for the repeat; it changes neither the production binary nor network configuration and does not substitute DNS.

That public regression repeat passed in 21.11s: `/tmp/hydra-m4b-9e2ff47b`, exit_code=0, host_integrity=true; cleanup again left only pre-existing `hydra-mgmt`. This covers the legacy dual-role control/forwarding/renewal path, not the full M12 suite. The complete private-mode goal still requires the remaining NAT variants, hosting crash/fresh-boot and real GUI/system-DNS gates, broader regressions and local preview packaging.

## 2026-10-06 — hosting and combined-host same-boot SIGKILL recovery

Added `TestPrivateRuntimeHostingSameBootCrashRecovery` and `TestPrivateRuntimeCombinedSameBootCrashRecovery`. Both establish real approved private traffic before killing the hosting daemon's process group with SIGKILL. The persisted per-node intent must remain unchanged. Restart reuses the same profile and isolated `/run`; neither host nor downstream client receives CONNECT, hosting-enable, bootstrap-import or a replacement invitation.

Hosting-only keeps the owner's normal WAN usable even while its daemon is dead. Combined hosting/upstream retains ProtectedMode and rejects the owner's ordinary HTTP while dead. In both cases downstream HTTP fails while the host is dead. After restart the same host PeerID, hosting intent and allowed client ACL must be restored; the restarted host must accept a new real ICE session, and the original client must automatically return to CONNECTED with the exact original private target and real host-egress HTTP.

The combined variant additionally requires automatic recovery of its own pinned upstream, own HTTP exiting through that upstream, and no change in upstream masquerade count during the subsequent downstream-only HTTP. A new host-local OUTPUT counter distinguishes leaked owner traffic from legitimate forwarded downstream WAN traffic. Its count and the existing downstream physical-WAN counter remain zero until explicit leave. Established membership revocation still works after crash recovery, without breaking the intermediate node's upstream. Full leave removes intents and active traffic, restores ordinary WAN, and returns links/addresses/routes/rules/nftables to normalized per-node pre-CONNECT baselines.

Results, exclusively on mini-PC:

| Scenario | Repetitions | Full-test durations | SIGKILL to restored downstream HTTP |
| --- | ---: | --- | --- |
| Hosting-only | 3 | 35.34s / 35.26s / 35.36s | 13.941s / 13.941s / 13.943s |
| Combined hosting + private upstream | 3 | 46.23s / 45.91s / 51.09s | 18.927s / 18.986s / 18.929s |

All six passed. Artifact directory `/tmp/hydra-m4b-e5018fd4`, 17:53:51Z–17:58:00Z, exit_code=0, host_integrity=true. Earlier hosting-only smoke also passed in 34.95s (13.942s recovery), `/tmp/hydra-m4b-579c654f`, exit_code=0, host_integrity=true. Final namespace listing contained only pre-existing `hydra-mgmt`. Mini-PC `go vet ./test/netns/m4b` passed.

Runner `private-host-crash.test` SHA-256 `aa97c055019228473ad6eabdd7c4a8ca59cf09dc2e929c561227bc0955618abe`; unchanged production daemon `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`. Durations use Go monotonic elapsed time around the injected kill and restored probe; they are observed bounds for this lab, not a production SLA or a decomposition of every retry. Probes/counters are scoped IPv4 evidence, not continuous arbitrary-traffic or IPv6 proof. DNS remains substituted by the lab launcher; fresh-boot, real systemd restart, GUI and package-install gates are not established by these direct process-restart tests.

## 2026-10-06 — actual host DNS component restoration

Strengthened `TestTunnelDNSPromotionInHostNamespace` rather than treating the netns launcher's substituted resolvectl as DNS evidence. The test now refuses to run outside PID 1's network namespace. This matters on the mini-PC: SSH is in a management namespace, where querying the host's systemd-resolved against management link indices fails. The privileged command explicitly enters PID 1 network and mount namespaces; it does not replace resolvectl or run a substitute DNS service.

The real production DNS manager/installer creates per-link DNS on a disposable real TUN and verifies the DNS server, routing domain `~.` and DNS default-route `yes`. After closing the installed resource and manager, the TUN still exists while the test verifies empty DNS/domain and default-route `no`. Thus deleting the link cannot mask a missing revert. After closing TUN it compares all-link DNS, domains, default-route, resolv.conf contents, mode and symlink target to the pre-test baseline.

Final test command, on the mini-PC only:

```sh
sudo nsenter --target 1 --net --mount -- env HYDRA_TUNNEL_DNS_PRIVILEGED=1 \
  /home/ubuntu/irvio-private-validation/build/private-netns/private-dns.test \
  -test.run '^TestTunnelDNSPromotionInHostNamespace$' -test.count 3 -test.v -test.timeout 90s
```

PASS 3/3: 0.16s / 0.14s / 0.15s. Runner SHA-256 `811fc716a3677149ceabc1724437c1f1611b14c17c46bda56739f4a6236f50c9`. Independent post-test queries showed the original physical-link DNS/default-route and no disposable TUN interface. An earlier baseline-only revision also passed 3/3 (0.16s / 0.10s / 0.11s). Mini-PC vet and `go test -race -shuffle=on -count=3 ./internal/runtime` passed (27.307s); unprivileged package runs intentionally skip this privileged test.

Scope: this proves actual systemd-resolved installation/revert for the shared production DNS component, with unchanged production code. Its base session stage is a test stage, not a complete private daemon CONNECT. Therefore it does not yet close the full private client host-namespace lifecycle, systemd crash/reboot or GUI acceptance gates. Workstation networking was untouched.

## 2026-10-06 — public Core compatibility regressions

Ran existing privileged public-mode regressions on the same production daemon used by the private scenarios (`9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`). These tests retain their existing engineering topology, injection and timeout controls; they are compatibility regressions, not private-mode NAT or product zero-argument acceptance.

| Existing scenario | Result | Duration |
| --- | --- | ---: |
| M11 dual-role public control / no implicit multihop | PASS | 33.50s |
| M12 brokered Exit failover remains protected | PASS | 19.33s |
| M12 client NAT-remap recovery | PASS | 72.65s |
| M12 Exit NAT-remap recovery | PASS | 72.70s |

Batch artifacts `/tmp/hydra-m4b-3b53cf1c`, 18:05:49Z–18:09:07Z, exit_code=0, host_integrity=true. Client remap-to-CONNECTED was 69.794s and remap-to-first-user-probe 69.819s; client FD count was 29 before and 27 after. The tests verify refreshed SRFLX/selected PRFLX, actual AWG endpoint, lease/transport ownership, real HTTP and their existing physical-WAN leak oracles. No latency SLA is inferred from these timings.

Mini-PC ordinary package gates also passed: `go test -p 2 ./...` and `go vet -p 2 ./...`. Some unchanged package results were cached; privileged gates are skipped in ordinary package runs and are counted only from the explicit root runner above. No production source or timeout was changed for these runs.

Runner `private-regressions.test` SHA-256 `aa97c055019228473ad6eabdd7c4a8ca59cf09dc2e929c561227bc0955618abe`; helper `killswitch` `d9624f3251658588f3c6256fdd5fb1115d1150b52d9f7cf4a691f0990a91e30c`; helper `icesignal` `8a4e80efa347db18050dd35a022afc93e093713e49bd9e65b7e8a2f0be29a884`. The earlier lab-only cgroup mount wrapper (`5d216bc19e024925f2fe3d216b63cce33c4ab493725005acb5a6948a5197c99d`) execs the frozen daemon after restoring cgroup2 inside each namespace; it does not modify the binary or DNS. Existing `internet-stream`, STUN and conntrack helpers are the versions recorded above.

Second sequential privileged batch: `/tmp/hydra-m4b-dfc7b5d5`, 18:09:43Z–18:11:09Z, exit_code=0, host_integrity=true. All eleven selected scenarios passed:

| Existing scenario | Duration |
| --- | ---: |
| M7 Safety Policy lifecycle stress / Exit crash adoption | 2.74s |
| M7 Egress Safety Policy v0 | 13.66s |
| M8 ProtectedMode kernel policy | 12.50s |
| M8 healthy VPN / Exit death fail-closed | 6.80s |
| M8 route and tunnel loss fail-closed | 21.65s |
| M8 kernel setup / teardown failure safety | 5.34s |
| M8 same-peer reconnect gap fail-closed | 6.13s |
| M8 NetworkGeneration migration fail-closed | 0.54s |
| M8 daemon crash adoption / explicit disconnect | 10.42s |
| M8 kernel policy lifecycle | 0.27s |
| M9 dead RESERVE → COLD → fresh batch | 6.52s |

The M7 lifecycle test performs 100 client-policy and 100 Exit-policy cycles, checking FD/netlink/goroutine equality and absence of owned nftables residue; it also tests adoption/repair of deliberately damaged Exit policy after SIGKILL. These are firewall component cycles, separate from the earlier 100 real private Connect/Disconnect cycles. M8 includes its existing IPv4/IPv6 physical-WAN probes. M9 uses a controlled candidate provider/connection-event harness with real data paths: A fails, B is unavailable, C works, C fails and fresh D works; four provider calls were observed. This validates the existing selector regression and must not be presented as permission for private mode to switch away from its pinned identity.

Third sequential privileged batch: `/tmp/hydra-m4b-7de7258a`, 18:11:43Z–18:13:25Z, exit_code=0, host_integrity=true:

| Existing scenario | Result | Duration |
| --- | --- | ---: |
| M10 automatic Exit failover remains protected | PASS | 22.42s |
| M10 daemon-owned production Internet lifecycle | PASS | 18.49s |
| P1 Core failover observed without Local API ownership | PASS | 19.10s |
| P1 Local API controls frozen M12 runtime | PASS | 42.62s |

Across the three batches, 19 selected privileged scenarios passed, with no production changes. Final checks found only pre-existing `hydra-mgmt` and no processes from the lab's daemon/runner paths. P1 uses the real Local API client, not a graphical Flutter window; it is not the remaining real-GUI acceptance gate.

Reproduction settings: execute the prebuilt runner as root inside `unshare --mount --propagation private`, mount cgroup2 at `/sys/fs/cgroup`, and set `HYDRA_M4B_PRIVILEGED=1`. All helper paths below are relative to `/home/ubuntu/irvio-private-validation/build/private-netns/`:

```text
HYDRA_M4B_HYDRAD=irviod-cgroup-launcher
HYDRA_M4B_RFC5780=stun                 # first batch
HYDRA_M4B_CONNTRACK=conntrack          # first batch
HYDRA_M5B_SIGNALHELPER=icesignal       # second batch
HYDRA_M7_INTERNET=internet-stream
HYDRA_M8_KILLSWITCH=killswitch
runner=private-regressions.test
```

Exact runner arguments (one sequential batch per line):

```sh
-test.run '^(TestM11_IrviodDualRolePublicControlAndNoImplicitMultiHop|TestM12_IrviodBrokeredNATRemapRecovery|TestM12_IrviodBrokeredExitNATRemapRecovery|TestM12_IrviodBrokeredExitFailoverRemainsProtected)$' -test.v -test.timeout 12m
-test.run '^(TestM8_.*|TestM7_EgressSafetyPolicyV0|TestM7_SafetyPolicyLifecycleStressAndExitCrashAdoption|TestM9_FullStackDeadReserveColdAndFreshBatch)$' -test.v -test.timeout 12m
-test.run '^(TestM10_IrviodOwnsFullProductionInternetLifecycle|TestM10_IrviodAutomaticExitFailoverRemainsProtected|TestP1_IrviodLocalAPIControlsFrozenM12Runtime|TestP1_CoreFailoverIsObservedWithoutLocalOwnership)$' -test.v -test.timeout 10m
```

This is substantial affected-public-Core coverage, not completion of every P10 scenario: private host/shared/APDM NAT, broader adversarial owner/revocation integration, fresh boot, real private GUI/systemd/system-DNS lifecycle and preview installation remain separately tracked.

## 2026-10-06 — private Exit behind EIM/APDF NAT

Added `TestPrivateRuntimeHostEIMNAT`. Generalized the existing client NAT fixture to a selected peer with its own routed inside port, external address, nftables table and bounded UDP header capture. The host is now behind `198.18.91.2:42000`; the client remains public. The fixed bidirectional UDP mapping is installed from the observed host mux port, without supplying its endpoint or private owner route to either daemon. Unsolicited inbound traffic to the inside host remains blocked by the existing conntrack-based filtering model.

Acceptance requires actual enrollment/approval, private pinned CONNECTED, real HTTP/UDP egress observed at the host's external NAT address, client ICE selecting precisely that mapped host endpoint, DNS over UDP/TCP and ICMP, no public RU country publication despite the synthetic external-address GeoIP mapping, revocation with no successful forbidden traffic or scoped user-WAN counter increase, cleared intent/stats, normalized peer network baseline equality and ordinary WAN after full leave.

The first host run did not pass: `/tmp/hydra-m4b-274b6d93`, aggregate exit_code=1, host_integrity=true. The existing client-NAT test passed in 20.17s, but the host-NAT test failed in 29.44s waiting for its ICE log oracle. Targeted logs showed successful traffic and the exact expected endpoint in `BROKERED_ICE_CONNECTED`; this particular connector emits text-format slog, whereas `waitM11Log` accepts JSON records. Classification: LAB/HARNESS ARTIFACT, not a HYDRA/IRVIO connection failure. The corrected assertion checks the actual text record with both expected PeerID and exact endpoint; it does not weaken the endpoint requirement. The original failed evidence is retained.

Corrected mini-PC batch `/tmp/hydra-m4b-d598091f`, 18:20:52Z–18:23:44Z: exit_code=0, host_integrity=true.

| Scenario | Result | Full-test durations |
| --- | --- | --- |
| Private client behind EIM/APDF, public private host | 3/3 PASS | 20.22s / 20.08s / 20.16s |
| Public private client, private host behind EIM/APDF | 3/3 PASS | 37.02s / 37.67s / 36.56s |

These durations include infrastructure startup, enrollment, traffic, revocation and cleanup; they are not connection latency. Mini-PC vet for the netns package passed. Runner `private-host-nat.test` SHA-256 `c0e3609b7420a2e7e9cc0b5b3dbafb5dd3a362829e14d13936b3a6a3585d1246`; production daemon remains `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`, unchanged. This adds host EIM/APDF evidence, not two-sided/shared/APDM/incompatible-NAT acceptance. The existing DNS-substitution and scoped-IPv4 limitations remain.

Batch runner arguments: `-test.run '^TestPrivateRuntime(Host|Client)EIMNAT$' -test.count 3 -test.v -test.timeout 7m`, using the same isolated DNS launcher, production binary, STUN and Internet helpers as earlier private tests. `HYDRA_M4B_HYDRAD` points directly to `irviod-releasefix`; the private harness launches it through `dns-launcher-isolated` per node.

Reran `TestPrivateRuntimeClientNATRemap` once with the generalized mapping helper (`-test.v -test.timeout 4m`): PASS 114.12s, `/tmp/hydra-m4b-acd35fed`, exit_code=0, host_integrity=true. Automatic pinned recovery took 93.882s after deleting three client UDP mappings. Continuous ordinary UDP recorded 1838 attempts, 638 correct-egress responses, 1200 failures/timeouts and zero wrong-egress responses. Scoped WAN counter, revoke and full leave passed. This preserves the earlier approximately 94-second timing observation; no production retry budget was adjusted. Final checks found only pre-existing `hydra-mgmt` and no lab daemon/runner processes.

## 2026-10-06 — private host endpoint-dependent mapping

Added `TestPrivateRuntimeHostEndpointDependentNAT`: the routed private host gets external UDP port 42000 for its bootstrap STUN responder tuples, but 43000 for other UDP destinations. Existing conntrack-based inbound filtering remains. This is a controlled destination-class-dependent mapping model, not a claim of universal APDM traversal or two-sided/shared-NAT coverage. No mapped endpoint is injected into invitations or daemon configuration.

The strengthened oracle requires a real host STUN observation at 42000, host signaling candidates containing 42000 and not 43000, and the client's established ICE endpoint selecting exactly the pinned host's external address at 43000. Existing HTTP/UDP/DNS/ICMP, egress identity, public/private country isolation, revocation, scoped IPv4 WAN counter, full leave and network-baseline checks also run. DNS remains substituted in this isolated fixture; actual host DNS was tested separately above.

Initial smoke `/tmp/hydra-m4b-345a796a` passed in 37.11s, exit_code=0, host_integrity=true, before adding the candidate-list exclusion assertion. The stronger repeat `/tmp/hydra-m4b-ca7e9677` (18:35:44Z–18:37:02Z) had two full passes (37.30s and 37.57s) and one startup failure (2.27s): `GET_PRIVATE_STATE` returned BUSY. Aggregate exit_code=1, host_integrity=true; the failed evidence is retained, not reclassified as a pass. Its runner SHA-256 was `1042f01c2111cd43726448d64070970601e356fea0d5e0f81bcb2fbe967353bb`.

The startup failure is a harness synchronization issue: `PrivateManagement` intentionally uses `TryLock`, returning `ErrInvalidState` during a concurrent lifecycle operation; the Local API maps that to BUSY, and `TestPrivateStateReadDoesNotCancelDiscoveryOrWaitForLifecycle` explicitly tests this contract. The harness previously treated every snapshot-read error as fatal. It now retries only BUSY reads at 50ms intervals, bounded to three seconds and the parent context; other errors and persistent BUSY still fail. Mutating commands are not retried by this change. Production code and its timeouts are unchanged.

Corrected mini-PC batch `/tmp/hydra-m4b-30080510`, 18:38:46Z–18:40:38Z: **3/3 PASS**, full-test durations 37.26s / 37.36s / 37.38s, exit_code=0, host_integrity=true. Runner `private-dependent-nat.test` SHA-256 `c123f6f8458c1c092a617a11ba7daacd4448ad26878e3fe08e522a65bd02e7ad`; production daemon stays `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`. Final namespace list contained only pre-existing `hydra-mgmt`. Mini-PC netns-package vet and the snapshot BUSY contract regression (`go test ./cmd/irviod -run '^TestPrivateStateReadDoesNotCancelDiscoveryOrWaitForLifecycle$' -count=10`) passed. No tests ran on the workstation.

Reproduction uses the preceding private NAT environment with `HYDRA_M4B_HYDRAD=irviod-releasefix`, `HYDRA_M4B_LAB_DNS_LAUNCHER=dns-launcher-isolated`, `HYDRA_M4B_RFC5780=stun`, `HYDRA_M7_INTERNET=internet-stream`; execute `private-dependent-nat.test -test.run '^TestPrivateRuntimeHostEndpointDependentNAT$' -test.count 3 -test.v -test.timeout 6m` under the same isolated cgroup mount. Broader two-sided/shared/incompatible NAT, real GUI/fresh-boot and local preview-package gates remain open.

## 2026-10-06 — same-identity private owner restart investigation

Added `TestPrivateRuntimeOwnerRestart`, retaining disjoint client bootstrap B1 and private registration owners R4/R5. After proving established traffic and reconnect via a surviving owner, stop both original owner processes, keep the host's TCP partition against B1, and restart R4 with its existing data directory. Assert unchanged PeerID and a different nonzero `RENDEZVOUS_SERVICE_ACTIVE` incarnation, wait for registration at the restarted owner, then explicitly reconnect the client to the same private Exit and check real HTTP egress, revocation and cleanup. This checks fresh-runtime owner rediscovery, not automatic data-plane recovery or explicit replay of an old receipt.

Initial run `/tmp/hydra-m4b-7c3a7069` failed in 115.81s: the harness's 90-second registration wait expired. Exit_code=1, host_integrity=true. Runner SHA-256 `b3fcbc0cea802587767643dbb32d1b2022f97bb0a80f56c2cb94315c203395e2`. Targeted host/owner logs show the restarted owner became M11/Rendezvous/Exit-ready and its announcements reached the host, which still reported zero registrations. Source inspection found existing `failedServerCooldown = 2 * time.Minute` in `discovery_provider.go`: the harness observation window was shorter than this policy. This failed run does not establish either successful recovery or a permanent product defect.

The follow-up harness permits three minutes for registration after restart (existing suppression plus reconciliation), with unchanged production binary and timers, and reports monotonic elapsed recovery time. Follow-up runner SHA-256 `8ee7a68115f672c9a5df01f946109fd6ae38895b6503524fc8997dbf3a80b979`. Separate mini-PC receipt/lookup regressions `TestPrivateRegistryIsolationAndReceipts`, `TestPrivateLookupPartialOwnersAndMigration`, and `TestPrivateLookupRejectsWrongOwnerAndCancellation` passed ten repetitions each (0.060s total). They use controlled fixtures and do not replace this integrated restart gate.

Follow-up `/tmp/hydra-m4b-8efe1ce6`, 18:49:14Z–18:51:56Z: **PASS 162.12s**, exit_code=0, host_integrity=true. Same-identity owner registration returned 136.166s after the harness began restart; a new client connection subsequently reached its pinned private host and HTTP egress passed, followed by membership revocation and full leave. Only pre-existing `hydra-mgmt` remained, with no matching lab daemon/test process. Production daemon SHA-256 remains `9a6a60b8b8a8ec93ae02e7c5bf0abf1d4d61d8081638816f81e2e77dd76d61c1`.

Classification: bounded recovery with a significant latency observation consistent with existing two-minute failed-owner suppression and subsequent reconciliation, not a demonstrated permanent stale-state defect. The initial 90-second failure is an observation-window mismatch and remains retained as a failed test. The 136.166s value measures owner registration recovery, not client blackout or connection latency, and is one sample rather than a distribution. Explicit stale-receipt replay against a restarted live server and automatic data-plane recovery are not proven by this scenario.

Reproduce with the previous private lab environment and `private-owner-restart.test -test.run '^TestPrivateRuntimeOwnerRestart$' -test.v -test.timeout 5m`. No production timeouts, quorum or protocol semantics were changed.

## 2026-10-06 — local private-preview packaging preflight

The existing allowlist builder now accepts optional `--private-preview`, deriving `irvio-private-preview-<12-character-commit>-ubuntu-amd64` and requiring a clean committed checkout, including nonignored untracked files. Failure to query Git is a build error, not an empty/clean status. Existing two-argument public Alpha naming is unchanged. Use a separate committed checkout and an external output directory; this does not replace the published artifact or authorize deployment.

On the mini-PC, `python3 packaging/ubuntu/tests/preview_build_test.py -v` passed all six tests in 0.170s; `bash -n packaging/ubuntu/build.sh` passed. Tests create real temporary Git repositories and check clean-preview naming, dirty tracked source rejection, untracked source rejection, Git failure rejection, unknown option rejection and unchanged public naming. A stub Go executable deliberately stops before compilation: these are preflight tests, not archive/build/install acceptance.

Initial fixture setup failed because Git was absent from the mini-PC. The first package-install attempt could not resolve DNS from the SSH management network namespace; installation through PID1's normal host network namespace succeeded without modifying DNS/routes. Git, git-man and liberror-perl were installed. This was lab preparation, not an IRVIO failure. Full clean-commit preview build, archive audit, install/update/uninstall and graphical acceptance remain outstanding.

## 2026-10-06 — first clean-commit local private-preview archive

Built on the mini-PC from a separate clean Git clone at `90f84316617e761726fce5aed812309b296f40ac`, branch `private-irvio`. No production installation or external release was updated. Canonical workstation source was not used to run tests or network services. Builder completed normally at 19:00:14Z using Go 1.27.1, Flutter 3.47.4, offline module/pub caches and the pinned September 2026 DB-IP database. The existing tray-manager `app_indicator_new` deprecation warning did not prevent the Linux release UI build.

Artifacts on the mini-PC:

- Clean checkout: `/home/ubuntu/irvio-private-preview-90f8431` (still clean after build).
- Archive: `/home/ubuntu/irvio-private-artifacts/90f8431/irvio-private-preview-90f84316617e-ubuntu-amd64.tar.gz` (40,876,025 bytes).
- Staging: `/home/ubuntu/irvio-private-artifacts/90f8431/.irvio-build.smqEgd/irvio-private-preview-90f84316617e-ubuntu-amd64`.
- `BUILD_INFO.json` and archive checksum file are beside the archive.

SHA-256:

| Artifact | Digest |
| --- | --- |
| tarball | `17d9d5f0f467ddae39c4c1b611d13b3974cc69491ff099df778f3370bbba54da` |
| packaged irviod | `c43a377bec00017f7c3dde2ea3609a7996ba9dd3dab19ae6852cfe01ce234667` |
| UI executable | `b9a7e5d8754de8dd9e39e9ff704f65373a9a4e2ae478d85d1f90ad5bd7153396` |
| UI libapp.so | `b3def0a31033a818fb74c7d6c6b0dbf534108ff0b264ae90f0941c060b185b31` |

`python3 packaging/ubuntu/tests/audit_archive.py <archive>` passed: 567 files, safe paths/types/modes, exact checksum inventory and required binaries/assets/services; no forbidden profile/key/log files. The external archive checksum also passed. `BUILD_INFO.json` records clean committed source and the empty tracked-diff hash. This proves archive construction/inventory, not live execution of these newly packaged binaries. Earlier lab results used a different binary hash; package-specific runtime, install/update/uninstall, GUI and remaining security/lifecycle gates are still required. Docker/Xvfb are not currently installed on the mini-PC; its existing public installation was left unchanged.

## 2026-10-06 — runtime proof from extracted preview tarball

Extracted the audited archive into `/home/ubuntu/irvio-private-artifacts/90f8431/extracted` and verified its complete `SHA256SUMS` before execution. The daemon used below is the extracted `payload/bin/irviod`, SHA-256 `c43a377bec00017f7c3dde2ea3609a7996ba9dd3dab19ae6852cfe01ce234667`, not the earlier development daemon or staging copy. Runner `private-owner-restart.test` remains `8ee7a68115f672c9a5df01f946109fd6ae38895b6503524fc8997dbf3a80b979`.

Mini-PC batch `/tmp/hydra-m4b-059a8c47`, 19:01:50Z–19:03:23Z, exit_code=0, host_integrity=true:

| Packaged-daemon scenario | Result | Full-test duration |
| --- | --- | ---: |
| Enrollment, pinned traffic, revoke/full leave | PASS | 20.02s |
| Private upstream plus private hosting / anti-multihop | PASS | 26.89s |
| Combined host SIGKILL / same-boot recovery | PASS | 45.96s |

Combined crash recovery restored hosting, member ACL and downstream pinned traffic automatically in 18.993s. Existing assertions verify owner's upstream egress versus downstream physical-WAN egress, scoped user-WAN counters, revocation and final network/resource cleanup. Final namespace list contained only `hydra-mgmt`, with no extracted lab-daemon processes. These are isolated production-daemon/API tests with the documented DNS substitution, scoped traffic oracles and direct process restart; they do not prove Flutter interaction, real systemd restart, fresh boot or live package installation.

Reproduction: same private lab helpers and cgroup mount as above, but set `HYDRA_M4B_HYDRAD` to the extracted archive's daemon; run `private-owner-restart.test -test.run '^TestPrivateRuntime(EnrollmentTrafficAndRevocation|CombinedHostingAndUpstream|CombinedSameBootCrashRecovery)$' -test.v -test.timeout 6m`. Existing installed mini-PC `irviod.service`/`irvio.socket` were active when inspected read-only; no installer/uninstaller or host lifecycle operation was run in this batch.

## 2026-10-06 — real mini-PC preview installation and socket activation

The previous installed daemon reported DISCONNECTED, no contribution and no active connection. Closed its confirmed `/opt/irvio/ui/irvio` process, then invoked daemon-owned Shutdown with the real Local API helper. A root-only backup of installation files, units and `/var/lib/irvio` is retained on the mini-PC at `/var/tmp/irvio-private-install.oos5XZ/previous-install-state.tar.gz`; it contains private profile data and must never be committed, published or included in the preview.

Executed the previous installation's `/opt/irvio/uninstall.sh` in PID1's real host network/mount namespaces: exit 0, owned binaries/launcher/desktop entry/units removed, profile and GeoIP retained. Installed the extracted preview with `install.sh --user ubuntu --no-deps`: exit 0, dependencies were already installed. This is the documented uninstall/reinstall update path, not an in-place overwrite or a clean-system dependency-install proof. No installer checks were bypassed and no manual firewall/DNS/session-state deletion was used.

Before the first API request, `irviod.service` was inactive with MainPID=0; `irvio.socket` was active and `/run/irvio/control.sock` was `ubuntu:root:0600`. Installed daemon and GUI executable hashes matched the archive (`c43a377b…234667` and `b9a7e5d8…153396`, full hashes above). Comparing saved `/var/lib/irvio` against the backup reported GID differences only, no content differences; existing systemd StateDirectory units have different group settings (daemon Group=irvio, boot-recover default root), so exact metadata equality is not claimed.

As ordinary ubuntu, the API helper activated the installed daemon via the real systemd socket. GET_STATUS returned DISCONNECTED, country empty, contribution=false, connection=null and error=null. No CONNECT was requested. API Shutdown succeeded; subsequent systemd inspection showed inactive/MainPID=0/NRestarts=0, socket still active, no `/run/irvio/session-state` and no nft tables. The extracted installer's read-only `--check` passed in the real host namespace. These results establish the installed preview's idle activation/shutdown and preserved profile contents, not private GUI enrollment or connected host-DNS/crash/reboot behavior.

The mini-PC now has the private-preview binaries installed and its GUI is closed; daemon is stopped, socket listening. Workstation and production servers were unchanged. Preview uninstall/reinstall, real GUI and remaining P10 gates still need completion.

## 2026-10-06 — installed graphical preview smoke and negative onboarding

Launched the installed GUI through `/usr/local/bin/irvio` in the mini-PC user's graphical session, with GDK_BACKEND=x11 for Xwayland automation. The window appeared, Local API reported DISCONNECTED without contribution, and journal showed only renderer/appindicator informational/deprecation output. A window capture returned black under the Wayland session, so that image was not accepted as visual proof.

For reproducible rendering, ran the same installed `/opt/irvio/ui/irvio` in authenticated local Xvfb `:99` (1280×1000, TCP disabled, software GL), managed by a transient user unit `irvio-private-gui-xvfb`. Added mini-PC-only xdotool/scrot/Xvfb tooling; apt also updated xserver-common/xserver-xorg-legacy dependencies. No workstation GUI or networking was changed.

Real pointer/keyboard interactions, not widget-test substitutes, verified:

- HOME rendered, including Private Network navigation, with Disconnected status.
- Private Network rendered the daemon's local identity, empty connections, invitation inputs and private-sharing controls.
- Import QR image opened the native GTK file picker; Cancel returned to the page.
- Importing `irvio-private:v1:invalid` showed a readable invalid-invitation error and remained Disconnected; no CONNECT or hosting action was requested.
- The window close control opened the Shutdown confirmation with its checkbox and Cancel/Shutdown actions.
- Clicking Shutdown IRVIO closed the GUI and its Xvfb session; systemd subsequently reported daemon inactive, MainPID=0, NRestarts=0. No remaining user irvio/Xvfb processes were listed.

Evidence images remain on the mini-PC under `/home/ubuntu/irvio-private-artifacts/90f8431/`: `gui-xvfb-home.png`, `gui-private.png`, `gui-picker.png`, `gui-invalid-invitation.png`, `gui-close.png`. They are local validation artifacts, not release payload or committed files. This establishes rendering, native picker cancellation, negative invitation handling and disconnected GUI Shutdown for the installed package. It does not establish positive QR decode/enrollment, two-person approval/connect, connected GUI death/tray behavior, all languages or native Wayland rendering; those gates remain open.

## 2026-10-06 — revoke authorized client during real ICE establishment

Added `TestPrivateRuntimeRevokeDuringICE`. After real routed enrollment/approval, the lab blocks only UDP between the private client and host, leaving bootstrap/STUN/signaling infrastructure reachable. It issues private CONNECT, waits for the host's `BROKERED_ICE_ACCEPT_STARTED` for the authenticated client, revokes that member through Local API, then restores peer UDP connectivity. Production daemon is the extracted preview (`c43a377bec00017f7c3dde2ea3609a7996ba9dd3dab19ae6852cfe01ce234667`), unchanged.

The oracle bounds the revoke call to 12s, observes at least 15s after restoring UDP, rejects CONNECTED/public-country/changed-target status, performs failing ordinary HTTP probes, requires no `TUNNEL_LEASE_DATA_PLANE_ACTIVATED` in host logs and zero downstream clients, checks the membership is no longer allowed, and requires the scoped user-WAN counter to remain zero. Full Disconnect on both nodes must clear their runtime intent, restore normalized network baselines and pass ordinary WAN HTTP with each node's own source IP. No unrelated Exit or public fallback is introduced.

Initial batch `/tmp/hydra-m4b-d044f379` failed all three attempts before CONNECT (4.73s / 4.93s / 5.03s): the new inline nft rules lacked statement terminators. Classification LAB/HARNESS ARTIFACT; exit_code=1, host_integrity=true. Corrected only the lab rule syntax; retained failed artifacts.

Corrected batch `/tmp/hydra-m4b-5f7e5782`, 19:14:37Z–19:15:49Z: **3/3 PASS**, 22.79s / 24.94s / 24.71s, exit_code=0, host_integrity=true. Runner `private-revoke-ice.test` SHA-256 `b58de7b2a3a4560950f7f6c26a456f845e656f36505c3e65bd9493720cdf1d73`. Mini-PC netns package vet/compilation passed. Only `hydra-mgmt` remained and no extracted-daemon process was found after completion.

Reproduction uses the packaged-daemon private lab environment with `private-revoke-ice.test -test.run '^TestPrivateRuntimeRevokeDuringICE$' -test.count 3 -test.v -test.timeout 4m`. This adds an integrated ICE-phase revocation proof with real protocol and firewall behavior, not an exhaustive scheduling proof at PREPARE/lease activation/renewal boundaries. DNS substitution and scoped IPv4 probe limitations remain. No production code, timers or wire protocol changed.

## 2026-10-06 — preview uninstall/reinstall with persistent-state equality

Completed a second real-host installation round trip on the mini-PC, this time removing the preview itself rather than the preceding public installation. Preflight showed daemon inactive/MainPID=0, socket active and no GUI/daemon/Xvfb process. Retained root-only `/var/tmp/irvio-private-install.oos5XZ/before-preview-reinstall.tar` containing `/var/lib/irvio`; this private backup must stay out of Git/release artifacts.

Ran the installed `/opt/irvio/uninstall.sh` in PID1's network/mount namespaces: exit 0. Explicit absence checks for `/opt/irvio`, `/usr/local/bin/irvio` and `/etc/systemd/system/irvio.socket` passed, while `/var/lib/irvio/profile` remained. Reinstalled from the extracted, audited preview with `install.sh --user ubuntu --no-deps`: exit 0. No force option, overridden ownership guard, manual firewall cleanup or manual session-state removal was used.

A read-only comparator hashed every regular file in the saved tar and current `/var/lib/irvio`, rejected unexpected links and compared both inventory and contents: **all 13 persistent files identical**, including profile and GeoIP. This deliberately does not assert GID equality across the known StateDirectory group transition. Installed daemon/UI executable hashes match the previously recorded preview. Before API activation: daemon inactive/MainPID=0, socket active, socket `ubuntu:root:0600`, no session intent and no nft tables.

Ordinary-user API access then activated the installed daemon and returned DISCONNECTED, contribution=false, empty country, connection=null and error=null. Daemon-owned Shutdown succeeded; final state inactive/MainPID=0/NRestarts=0. No VPN CONNECT was issued. This closes the local preview's basic real install/update-by-uninstall/reinstall/uninstall and idle socket lifecycle checks on the existing Ubuntu mini-PC, not clean-image dependency installation or connected private systemd/reboot/GUI acceptance. Production servers, workstation installation and public releases remain untouched.

## 2026-10-06 — persistent trust is not a startup connection intent

Added `TestPrivateDesktopEmptyRuntimeDoesNotRestorePersistentTrust` with client-only, hosting-only and combined cases. Each creates a real persistent identity/trust store, saved target and approved downstream membership, plus a separate old runtime intent. It starts the actual desktop product entry point and Unix Local API using the same persistent profile but an empty runtime directory. Repeated status reads must remain DISCONNECTED with no private target, hosting or public contribution; no protection hook may run and no new session-state may appear. Persistent trust must remain identical. The old runtime fixture is deliberately retained and checked, not cleared to manufacture a clean result.

Mini-PC focused test: three repetitions, nine subcases PASS (1.619s). Related private/same-boot tests with `-race -shuffle=on -count=3` passed in `cmd/irviod` (3.102s) and `internal/productintent` (1.039s); both packages passed vet. Initial fixture attempts incorrectly put session-state in the profile and then used an unsafe-permission temporary runtime directory; production validation correctly rejected both. Corrected only test layout/permissions.

Unfiltered `go test ./cmd/irviod ./internal/productintent -count=1` also passed on the mini-PC (1.128s / 0.008s).

This is a component startup regression with mocked protection inspection/hooks and synthetic trust, not a kernel reboot, prior active network session, real boot-recover execution, or proof of systemd crash recovery. Actual connected private reboot/systemd gates remain open. No production behavior or installed binaries changed.

## 2026-10-06 — real connected GUI SIGKILL and relaunch

Requirement audit found that the earlier API-client close/reopen did not prove the P07/P09/P10 real GUI-death invariant. Added `TestPrivateRuntimeGUICrash` and reusable `private_gui_test.go`. The existing real enrollment/approval/ICE/AWG/lease lab first establishes private traffic, then launches the extracted preview Flutter executable against that client's real Local API. An isolated Xvfb display has TCP disabled; GUI home/config/data directories are temporary and separate from the installed user's preferences. The lab socket environment override selects the namespace daemon only; it injects no target, candidates or status.

Twice per test: wait for the native IRVIO window, capture a screenshot, verify CONNECTED with unchanged private PeerID and correct HTTP egress, SIGKILL only the GUI process group, then sample daemon liveness/status and actual HTTP egress five times. No CONNECT, import, Disconnect or daemon restart occurs between GUI launches. The common scenario subsequently verifies HTTP/UDP/DNS/ICMP, revokes the member, checks its scoped user-WAN leak counter and performs full leave/host-integrity cleanup.

First attempt `/tmp/hydra-m4b-468870c2` stopped at the harness executable-owner guard because `/opt/irvio/ui/irvio` is root-owned after installation (9.03s, exit_code=1, host_integrity=true). Used the identical user-owned UI from the audited extracted tarball; did not weaken the guard. Follow-up `/tmp/hydra-m4b-d410215e` passed in 30.18s (exit_code=0, host_integrity=true). Its initial screenshot was black before first rendering, while its post-relaunch screenshot displayed the correct private CONNECTED state; only the rendering wait was raised from 2s to 5s.

Final batch `/tmp/hydra-m4b-0af1a252`, 19:32:56Z–19:34:08Z: **2/2 PASS**, 36.15s / 36.17s, exit_code=0, host_integrity=true. Runner SHA-256 `60040c3f8090ca0d8f36eacbbce8356a74bd5e1709402edaa3555e406f8ab229`. Both screenshots from the second repetition were inspected: Private Network, the same target PeerID, CONNECTED, no public-country dropdown. Screenshots remain local artifacts, not Git/release payload. The extracted UI/daemon are unchanged from the recorded 90f8431 preview. Compilation and netns-package vet passed on the mini-PC. Final namespaces: only `hydra-mgmt`; no Xvfb process, installed system daemon inactive/MainPID=0/NRestarts=0.

Reproduce with the existing private lab environment plus `IRVIO_PRIVATE_UI=<extracted>/payload/ui/irvio` and `private-gui.test -test.run '^TestPrivateRuntimeGUICrash$' -test.count=2 -test.v -test.timeout=3m`. Requires Xvfb, xdotool and scrot. The screenshot is a manual rendered-state oracle, not an automated OCR assertion. This proves private-client GUI death/relaunch using real binaries; it does not prove positive two-person UI enrollment, hosting-only GUI death, native Wayland/tray integration, actual host DNS or reboot. Production code and installed services were not changed.

## 2026-10-06 — cross-owner receipt replay over real distributed transport

Extended `TestPrivateDistributedRegistrationLookupAndSignaling`: after authentic registration and DHT discovery of two owners, close one owner's private service and replace its private stream handler with an adversarial responder. It returns the other owner's intact valid descriptor/receipt/route, with the correct request ID, over its own authenticated libp2p connection. A direct production `Client.LookupPrivate` must reject this with `ErrWrongPeer`. A fresh production distributed lookup must actually query the adversarial owner, report a partial result and retain only the honest owner's route, without changing the pinned target. The existing test then continues through real broker signaling, registration withdrawal, stale DHT hints, replacement registration and rejection of the old signaling route.

The adversarial handler counts received requests before writing its response, avoiding a test-only race between reply delivery and counter publication. No production verifier, registry or transport validation was changed. This adds the real authenticated transport case to the previous callback-based wrong-owner unit test.

Mini-PC results: initial focused three repetitions PASS (3.825s). Final `go test -race -shuffle=on -count=5 ./internal/rendezvous ./internal/discovery -run Private` PASS (14.671s / 4.287s), followed by `go vet ./internal/rendezvous ./internal/discovery` PASS. These tests use real libp2p/DHT nodes and signed synthetic M11 attestations under the unchanged proof policy; they do not perform real external M11 verification or ICE/AWG traffic during the attack. Full production netns discovery/traffic proofs remain separate. No installed application or production distributor changed.

## 2026-10-06 — private client and Exit share one external NAT address

Added `TestPrivateRuntimeSharedNATWithLAN` and `private_shared_nat_test.go`. Both private peers use independent identities, profiles, processes, namespaces and routed internal segments, but their router source-NATs external traffic to the same `198.18.91.2`. Inter-LAN traffic is allowed. Public unsolicited inbound traffic to either internal peer is dropped unless conntrack-established/related. No candidate, endpoint, invitation grant or ICE choice is injected; enrollment, approval, owner discovery and connection use production paths.

Both nodes must log fresh SRFLX observations with the shared external address. Because a sink observing only that address cannot distinguish direct client WAN from authorized Exit forwarding, the test additionally requires actual Exit masquerade-counter activity and retains the client-namespace physical-WAN leak oracle. The common scenario checks pinned identity, public/private country isolation, HTTP/UDP/DNS/ICMP, rejection after revocation and full Disconnect. Both peers' normalized links/addresses/routes/rules/nft state must equal their idle baselines and ordinary WAN HTTP must work afterward.

Mini-PC, unchanged extracted preview daemon: initial `/tmp/hydra-m4b-33e251a5` PASS 21.51s, exit_code=0, host_integrity=true. Repeated batch `/tmp/hydra-m4b-7fffb206`, 19:41:02Z–19:42:06Z: **3/3 PASS**, 21.07s / 20.99s / 21.15s, exit_code=0, host_integrity=true. Runner SHA-256 `e7e599c8cf16ecfdddd4f88c6ff4b16179b36c1cab6cda6cce7b50f6167b2056`; compilation and netns-package vet passed. Final namespaces only `hydra-mgmt`; installed system daemon remained inactive/MainPID=0.

Reproduce with the existing extracted-daemon private lab environment and `private-shared-nat.test -test.run '^TestPrivateRuntimeSharedNATWithLAN$' -test.count=3 -test.v -test.timeout=3m`. This establishes shared external IPv4 with a usable local path; it does not establish forced hairpin traversal, isolated LAN clients, arbitrary two-sided APDM, or bounded failure of an incompatible NAT pair. DNS substitution and scoped IPv4 security-oracle limitations remain. Production code, installed software and public servers were not changed.

## 2026-10-06 — isolated shared NAT without hairpin fails closed

Added `TestPrivateRuntimeIsolatedSharedNATFailsClosed`: the preceding shared-NAT topology now drops inter-LAN forwarding and has no hairpin DNAT. Bootstrap, Rendezvous, STUN and ordinary WAN remain reachable. Real routed enrollment/approval must complete and the host must record `BROKERED_ICE_ACCEPT_STARTED` for the authorized client before the failure oracle runs, preventing missing supply/discovery from masquerading as NAT incompatibility.

The client must reach NO_EXIT within the existing bounded state-wait allowance, preserve the exact private target and empty public country, and never report CONNECTED during at least ten further seconds of ordinary HTTP failure probes. ProtectedMode's nft table must remain installed, the scoped physical-WAN leak counter must remain zero, and the host's active client count must be zero. Explicit Disconnect on both peers must clear intent, return DISCONNECTED, restore normalized network baselines and permit ordinary WAN HTTP. The test does not request a different Exit or alter production retry/ICE timeouts.

Mini-PC, unchanged extracted preview daemon: initial `/tmp/hydra-m4b-e000ebe9` PASS 36.26s; observed CONNECT request to NO_EXIT 6.573s. Repeated batch `/tmp/hydra-m4b-080d2336`, 19:45:39Z–19:47:29Z: **3/3 PASS**, 36.77s / 36.81s / 36.28s; NO_EXIT after 6.578s / 6.580s / 6.585s. Both batches exit_code=0, host_integrity=true. Runner SHA-256 `de5f11fe0e61219f8fe5cd9fd6c0c0314f716626eb2ee968d4d813adfc5628c1`; netns-package compilation/vet PASS. Final namespaces only `hydra-mgmt`.

Reproduce with the existing private lab environment and `private-isolated-nat.test -test.run '^TestPrivateRuntimeIsolatedSharedNATFailsClosed$' -test.count=3 -test.v -test.timeout=4m`. This proves bounded, fail-closed behavior for the defined incompatible topology, not a universal NAT timeout SLA, all two-sided APDM behavior or automatic recovery after lifting client isolation. The ordinary probes cover the post-NO_EXIT observation window, not a continuous packet stream across startup. DNS substitution and IPv4 oracle limits remain. No production code or deployment changed.

## 2026-10-06 — positive native two-UI QR enrollment and connection

Added opt-in `TestPrivateRuntimeGUIOnboarding`, enabled only by `IRVIO_PRIVATE_MANUAL_GUI=1`. Without explicit opt-in it skips rather than hanging unattended suites. The harness prepares independent fresh namespace daemons and imports their normal bootstrap bundles, then starts two real extracted-preview Flutter executables in isolated Xvfb sessions with separate HOME/config/runtime directories and Local API sockets. Unlike the ordinary automated scenario, it does not enable hosting, create/import invitations, request enrollment, approve a member or issue private CONNECT through its API client. Those actions must occur in the graphical applications. A 20-minute lab-only interaction deadline bounds this manual test; production timeouts are unchanged.

On the mini-PC, used real pointer/keyboard input (xdotool) and inspected rendered screenshots:

1. Owner: Private Network → Start private sharing only; UI displayed Private hosting active.
2. Owner: Create invitation → Save invitation QR → native GTK save dialog → PNG file in the root-only lab artifact directory.
3. Client: Private Network → Import QR image → native GTK open dialog → selected the saved PNG. Decoded invitation filled the form; client remained Disconnected.
4. Client: entered a connection name → Import invitation → Check approval / retry. Client displayed Waiting for owner approval and no CONNECT action; owner displayed the requesting client's matching PeerID.
5. Owner: Approve this PeerID → Cancel in confirmation. A second client status check remained pending, without access.
6. Owner: Approve this PeerID → Confirm after comparing the displayed identity. Client: Check approval / retry → saved target with CONNECT appeared, still Disconnected.
7. Client: explicitly clicked CONNECT. HOME displayed Private Network, the correct owner PeerID and CONNECTED.

The harness then resumed its automated production-path checks: pinned identity, public/private country isolation, actual HTTP/UDP/DNS/ICMP traffic and expected egress, API-driven member revocation, scoped WAN-leak oracle and full leave. Revocation/teardown here were API-driven, not claims of manual GUI revocation or tray Shutdown.

Run `/tmp/hydra-m4b-a6830167`, 19:50:01Z–19:56:45Z: **PASS**, 404.07s including manual interaction, exit_code=0, host_integrity=true. This duration is not connection latency. Runner SHA-256 `d83a5a77e997b25a1539244242a5e48aeebd599fa0a9567dc673a8e58977ab87`; compilation and netns-package vet passed. Final namespaces only `hydra-mgmt`, no Xvfb processes. Root interaction shells were exited. No workstation networking, installed application or production server changed.

Private artifacts include console metadata, invitation PNG and pending/approved/connected screenshots. They are not committed or packaged. The invitation export used normal file permissions inside a root-only directory; treat exported invitation files as sensitive. Bootstrap setup was performed by the lab, so this is private onboarding acceptance after bootstrap, not a new GUI bootstrap-import claim. English/Xvfb, lab DNS substitution and namespace-launch limitations remain; native Wayland/all-language end-to-end and actual host reboot are not inferred.

## 2026-10-06 — real systemd private client crash recovery and intentional exit

Added `TestPrivateRuntimeSystemdCrashRecovery` and `private_systemd_test.go`. The real enrollment/approval/traffic lab launches only its private client through a uniquely named transient systemd service with `Restart=on-failure`, `RestartSec=2s`, and the client's isolated network/runtime namespaces. PID1 manages the actual extracted preview daemon; no harness process performs its restart. The installed desktop service and socket are not replaced or stopped by this test.

After CONNECTED with real private egress, SIGKILL the unit's MainPID. Require the protective nft table and unchanged private ConnectIntent to survive; a bounded HTTP probe during the restart delay must not pass. Observe a new MainPID and exactly one systemd restart, reopen Local API, require the same pinned PeerID and no country fallback, then prove restored HTTP traffic through the intended Exit with zero scoped physical-WAN leak packets. Continue through normal traffic/revocation/full Disconnect, compare both desktop nodes' interfaces, addresses, routes, rules and nft state against their pre-connect baseline, and require cleared session intents and restored ordinary WAN. Finally request daemon-owned Shutdown from idle, require `systemd-run --wait` to exit successfully, and observe the collected unit remain absent for three seconds, beyond RestartSec. This last step tests intentional exit after full Disconnect, not direct Shutdown from CONNECTED.

Initial crash-only run `/tmp/hydra-m4b-79e7470b`, 20:04:02Z–20:04:24Z: PASS 22.49s, recovery 2.401s, exit_code=0, host_integrity=true. Final extended batch `/tmp/hydra-m4b-8aaf2a0a`, 20:05:39Z–20:07:00Z: **3/3 PASS**, 27.05s / 26.49s / 27.00s. Fault-to-restored-user-traffic observations: 2.396s / 2.424s / 2.424s, not a product SLA. Each had NRestarts=1 and successful intentional exit without restart. Batch exit_code=0, host_integrity=true. Runner SHA-256: `9f9a8a5d4217c60074a48326f63c2fdb3bba8e43ffaa13c4c351b448ad5de6b2`.

Mini-PC compilation and `go vet ./test/netns/m4b` passed. Reproduce using the existing private lab environment and extracted preview with `private-systemd.test -test.run '^TestPrivateRuntimeSystemdCrashRecovery$' -test.count=3 -test.v -test.timeout=5m`. Final host checks: only `hydra-mgmt` namespace, no `irvio-private-*` transient units, installed `irviod.service` inactive/MainPID=0/NRestarts=0, `irvio.socket` active. No production code, installed binaries, workstation networking or production distributors changed.

This closes real systemd-supervised private-client same-boot recovery, not packaged socket-FD adoption, hosting/combined systemd restart, kernel reboot, or full private host-DNS validation. The isolated launcher still substitutes resolvectl; actual system DNS and fresh-boot gates remain separate. The scoped leak counter covers the lab user-probe destination, not arbitrary Internet destinations or exhaustive crash interleavings.

## 2026-10-06 — hosting GUI death preserves downstream service

Added `TestPrivateRuntimeHostingGUICrash`; factored the existing real GUI launch/SIGKILL/relaunch helper so the original upstream-client check remains unchanged. The new case opens the real extracted Flutter UI against a private hosting daemon with one authorized, connected downstream client. Before and after each of two GUI SIGKILLs, require the host daemon alive, HOSTING with private hosting intent, no own target/country/public contribution, and exactly one downstream client. Require the client still CONNECTED to the same pinned host. Real HTTP probes from both nodes must retain the host's physical egress: ordinary WAN for the owner and private-tunnel forwarding for its client. Neither approval, hosting activation nor client CONNECT is repeated between GUI launches.

Mini-PC batch `/tmp/hydra-m4b-a85a5fa2`, 20:09:21Z–20:11:46Z: **4/4 PASS**. Original client GUI test twice: 36.19s / 36.19s. New hosting GUI test twice: 36.09s / 36.12s. Each case killed and relaunched the real GUI twice; all continued through real HTTP/UDP/DNS/ICMP, revocation, zero scoped user-WAN leaks, full leave and cleanup. exit_code=0, host_integrity=true. Runner SHA-256 `43873da4902f4901f12ba6dcca7b96d093d1f8714a7d10d948c425b43c45e6c8`.

Inspected the hosting GUI screenshot after relaunch: HOME displays HOSTING, private-sharing/ordinary-Internet guidance, CLIENTS 1 and Private hosting active. Screenshots remain local evidence, not source/release payload. Final host checks found only `hydra-mgmt`, no Xvfb process, installed daemon inactive/MainPID=0/NRestarts=0. Compilation, netns-package vet and local diff check passed; production code, installed binaries, production distributors and workstation networking were unchanged.

Reproduce with the existing private lab environment, `IRVIO_PRIVATE_UI=<extracted>/payload/ui/irvio`, and `private-hosting-gui.test -test.run '^TestPrivateRuntime(Hosting)?GUICrash$' -test.count=2 -test.v -test.timeout=5m`. This is Linux/Xvfb GUI-process death, not desktop-session logout, native Wayland, tray Shutdown or a reboot. Lab DNS substitution and synthetic GeoIP remain as previously documented. Hosting remains private by authoritative runtime status and the common public-country exclusion oracle; no claim of exhaustive distributed snapshot inspection is made.

## 2026-10-06 — confirmed native GUI Shutdown of combined upstream and hosting

Added opt-in `TestPrivateRuntimeCombinedGUIShutdown` with `IRVIO_PRIVATE_MANUAL_GUI=1`. It establishes the existing real three-peer topology: private upstream, combined client/host, downstream client. Native Flutter GUI targets the combined host's actual Local API. Before interaction require CONNECTED, the exact upstream PeerID, private hosting intent and one downstream client; initial HTTP checks establish distinct upstream and downstream egress. The harness supplies no Shutdown API command. It exports root-only console metadata and waits for an operator to use the real GUI; a timeout fails the test.

Used the native top-right close button, Cancel, then close and confirmed Shutdown IRVIO. The test requires successful GUI and daemon process exits, absent combined recovery intent, complete host interface/address/route/rule/nft baseline restoration, RELEASE observed as zero clients on the still-running upstream, and restored owner WAN. Five downstream HTTP probes must fail after host shutdown with zero scoped WAN leak packets. The remaining peers explicitly Disconnect and their network baselines must also match.

Mini-PC `/tmp/hydra-m4b-17f990e9`, 20:15:01Z–20:17:34Z: PASS 153.38s. Repeat `/tmp/hydra-m4b-a3c25c50`, 20:18:23Z–20:20:35Z: PASS 131.77s. Both exit_code=0 and host_integrity=true. These durations include manual screenshot review and are not Shutdown latency measurements. Runner SHA-256 `bafb687ed547f5e16ba031512ab20c00b73f60a8a98008637ca32940484c5604`. Compilation and netns-package vet passed. Final namespace inventory contains only `hydra-mgmt`, with no Xvfb left; installed daemon remains inactive/MainPID=0/NRestarts=0.

The first run produced a black rendered window in repeated screenshots after immediately issuing Cancel and close without a rendering pause; clicking the confirmation's previously observed location still completed the fully verified Shutdown. The second run separated actions: after Cancel, a real API read remained CONNECTED and the screenshot showed CONNECTED/CLIENTS 1; reopening showed the full confirmation normally before the actual click. Preserve the first run's black-frame observation as **unclassified rendering/timing evidence**, not proof of a Core failure or a proven lab artifact. Its root cause remains unverified. Screenshots are local artifacts only.

Reproduce with the existing private environment plus `IRVIO_PRIVATE_UI=<extracted>/payload/ui/irvio`, `IRVIO_PRIVATE_MANUAL_GUI=1`, and `private-gui-shutdown.test -test.run '^TestPrivateRuntimeCombinedGUIShutdown$' -test.count=1 -test.v -test.timeout=5m`. Console metadata identifies the runner mount namespace, GUI namespace/display/window and isolated socket; no invitation secrets are exported by this helper. The test checks close-button confirmed Shutdown, not the native tray menu. Tray and close share `requestShutdown()` in source, but that is not substituted for a native tray interaction test. No real host DNS, fresh boot or universal desktop-rendering claim is made; production code and deployed binaries remain unchanged.

## 2026-10-06 — acceptance coverage audit and current security regression

Compared the complete P00–P11 plan with concrete tests, runtime admission/discovery code and recorded artifacts; added `private-irvio-v1-acceptance-audit.md` with remaining evidence boundaries. Found that the old 100-cycle production binary predates the RELEASE result-handling fix. A new current-preview run was launched autonomously as documented in that audit; its result is pending, not PASS.

On the mini-PC, `go test -race -shuffle=on -count=5 ./internal/privateaccess ./internal/rendezvous ./internal/discovery ./internal/sessionestablish ./internal/tunnellease ./internal/localapi ./cmd/irviod -run 'Private|Enrollment|Policy|Pinned|Admission'` exited 0: privateaccess 2.882s, rendezvous 15.069s, discovery 4.719s, sessionestablish 1.339s, localapi 1.198s, command 4.324s. Tunnellease explicitly reported no matching tests; do not count that filtered package as tested.

To include storage corruption/single-use cases and the actual lease suite missed by the filter, ran unfiltered `go test -race -shuffle=on -count=5 ./internal/privateaccess ./internal/tunnellease ./internal/productprofile ./internal/productintent ./internal/exitselector`. All passed: 3.403s / 1.040s / 1.053s / 1.046s / 2.095s respectively. These are component/race regressions, not replacements for integrated revocation, reboot or actual host-DNS evidence. No production source changes were needed in this audit.

## 2026-10-06 — private addressless owner lookup over actual DHT transport

Added `TestPrivateAddresslessOwnerUsesFindPeerAndAuthenticates`. Each case creates fresh real discovery/libp2p nodes. The client knows only its bootstrap and must have no owner addresses or connections before lookup. The bootstrap's controlled Kademlia stream handler returns the exact private-locator GET_PROVIDERS response with an owner PeerID but no addresses. Only a subsequent FIND_NODE for that PeerID supplies its endpoint. The production `FindPrivateOwners`, DHT query implementation, address-resolution helper, authenticated connection and signed announcement verification remain unchanged. Atomic wire counters are restricted to the lookup client's authenticated PeerID, excluding background queries from the owner.

Three cases: eligible M11-backed private owner must be returned; public Rendezvous without private capability must yield no owner and incomplete lookup; a DHT response associating the desired PeerID with the bootstrap's different-identity endpoint must also yield no owner and incomplete lookup. All require that the addressless provider response and exact-peer FIND_NODE were actually observed. No preloaded owner endpoint or direct call to a fake client resolver is used.

Initial fixture setup tried to advertise a Rendezvous on a standalone node with an empty DHT table and correctly failed before the scenario. Corrected only the fixture by bootstrapping that owner before advertising; the lookup client still receives no hidden owner address. Initial focused run passed all three cases in 0.465s. Final mini-PC `go test -race -shuffle=on -count=10 ./internal/discovery -run TestPrivateAddresslessOwner` passed in 11.146s (30 subcases), `go vet ./internal/discovery` passed, and unfiltered `go test ./internal/discovery -count=1` passed in 9.417s.

This proves the actual private lookup's addressless fallback and post-resolution identity/capability enforcement with controlled wire responses. The fixture uses valid signed synthetic M11 proofs rather than real external reachability challenges, and does not establish ICE/AWG/TunnelLease traffic in this test. Those production traffic gates retain their separate netns evidence. No production source or frozen resource-run binary changed; the autonomous current-preview 100-cycle unit was still active when checked during this work.

## 2026-10-06 — client endpoint-dependent NAT scenario prepared, execution queued

Added `TestPrivateRuntimeClientEndpointDependentNAT` and generalized the existing host NAT rule helper to either peer. Client outbound destination-class mappings are installed before the desktop starts: STUN endpoints use external port 42000, other UDP uses 43000, with the existing conntrack-dependent inbound filter retained. Reverse mapping to the observed production UDP socket is added after the adapter starts; the initial rule set never guesses a local port. This avoids initially creating a peer flow under the old EIM mapping. No endpoint is passed into the client or signaling API.

The new oracle requires fresh client STUN at port 42000, client OFFER candidates containing that STUN endpoint but not the peer-specific 43000 endpoint, and host ICE-selected remote endpoint at port 43000. It then uses the existing real traffic/revoke/WAN-leak checks and additionally compares both peers' final network state with baseline. This is a controlled endpoint-dependent mapping/APDF model, not an exhaustive implementation of every commercial NAT or two-sided APDM reachability proof.

Mini-PC test compilation and `go vet ./test/netns/m4b` passed. Prepared runner SHA-256: `38fe80cfe430cf0eb618cf3d168068aaea8a3b30e521e85acd9957678923dc47`. **Network results are pending.** Both refactored host and new client scenarios are queued with `-test.run '^TestPrivateRuntime(Host|Client)EndpointDependentNAT$' -test.count=3 -test.v -test.timeout=6m` using the unchanged extracted preview daemon.

Queue unit `irvio-private-dependent-5b030bb.service`, invocation `671ca92b482e4a3b9273133b71a2c475`, initial PID 272711, runs `/home/ubuntu/irvio-private-artifacts/90f8431/client-dependent-after-lifecycle.sh`. It waits locally for `irvio-private-lifecycle-15c65d8-r1.service` to stop, then requires `/tmp/hydra-m4b-3a70e777/results.json` to report exit_code=0 and host_integrity=true before creating another lab. If that prerequisite fails or is missing, the queue exits without launching the NAT tests. At the initial check only the waiting script, not the NAT runner, was present. Do not start another privileged lab or reboot while either unit is active. The prepared helper is not yet an acceptance PASS and production code remains unchanged.
