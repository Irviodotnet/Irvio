# Private IRVIO — implementation plan v1

Status: P00–P11 local implementation and validation complete on `private-irvio`, PASS WITH OBSERVATIONS. Requirement evidence, migration requirements and test boundaries: `docs/testing/private-irvio-v1-handoff.md`. Final packaged 100-cycle result: `docs/testing/private-irvio-final-package-resources.md`. This is not a public release or production deployment claim.

Baseline: `27851e57b2f856e3134377143f8240734056d07f` (`main`).
Local branch: `private-irvio` (Git branch names cannot contain spaces).
User approval: trusted peers through existing distributors; mini-PC may be used for development/testing. No GitHub push or release publication is authorized for this branch yet.

## Working rules

- Keep public mode working throughout; do not turn private mode into a public-mode rewrite.
- Edit canonical sources in `/home/ubuntu/Work/Hydra` and make narrowly scoped local commits on this branch.
- Run tests on the mini-PC, including isolated network namespaces; do not run tests or change live networking on the workstation.
- Build/test private distributors in the lab. Do not roll development binaries onto production distributors without separate approval.
- Never commit identities, invitations, enrollment secrets, profiles, MMDBs, packet captures or credentials.
- Each implementation commit records its checks and remaining limitations; never describe a planned gate as passed.
- No new long soak, Windows port, public release, or website changes in this workstream.
- The baseline country-presence grace is a UI mitigation, not proof of complete discovery; the unresolved M11 timeout incident remains a separate issue.

## Product scope and invariants

- One upstream tunnel at a time: public country OR an explicitly selected private Exit PeerID.
- Several saved private Exits and several authorized downstream clients are supported; no automatic switching between different private Exit identities in v1.
- Private hosting may run without an upstream tunnel; the owner's ordinary traffic keeps using WAN in hosting-only mode.
- Private mode never silently enables public contribution, public discovery publication or public fallback.
- Hosting and upstream intents are separate internally, but full Disconnect/Shutdown still stops all HYDRA/IRVIO participation. Do not show globally DISCONNECTED while hosting is active.
- GUI death does not end a session; same-boot recovery restores authorized intent; new OS boot does not auto-connect or auto-enable hosting by default.
- An invitation/discovery locator is not an authorization decision. The Exit authorizes authenticated end-client PeerID.
- Revocation rejects new work and boundedly cancels pending establishment and revokes active leases, including races with activation/renewal.
- M11 quorum, signed identity/AWG binding, fencing, ProtectedMode, Safety Policy and anti-multihop remain enforced.
- No mesh, LAN access, subnet routing, exit-to-exit chaining, new payload relay or serverless mode in v1.
- Privacy means restricted access and exclusion from public selection, not invisibility to distributors, DHT observers or the Exit operator.

## Dependency graph

`P00 contract -> P01 storage -> P02 authorization -> P03 private wire/registry -> P04 distributed lookup -> P05 enrollment -> P06 pinned connection -> P07 lifecycle -> P08 Local API -> P09 UI -> P10 integration/security -> P11 local packaging`

Some unit-level implementation can proceed independently after P00, but do not expose an end-to-end private mode until authorization, publication isolation and teardown gates pass together.

## P00 — freeze the implementable contract

Files: this document; add `docs/product/private-irvio-v1.md` and `docs/testing/private-irvio-v1-validation.md`.

Tasks:

1. Define private hosting/upstream intent combinations and switching semantics, including the full-leave action and hosting-only status.
2. Specify invitation, enrollment, access grant, revocation, registration, exact lookup and migration semantics.
3. Define wire feature negotiation and error categories: unsupported, unknown/offline, denied, pending approval, expired invitation, incompatible, connection failure.
4. Set explicit bounds for invitations, pending approvals, memberships, registrations, frame sizes, lookup concurrency/deadlines/cache lifetimes and retry budgets.
5. Decide how private intent is bound to the signed establishment transcript; do not synthesize a country or repurpose an existing field silently.
6. Threat-model malicious distributors, leaked invitations, forged providers, replay and permission changes during connection.

Gate: every security-sensitive ambiguity has an explicit decision and a test case before its implementation. Do not invent custom cryptography to avoid a design decision.

Suggested commit: `docs(private): define trusted-peer contract and gates`.

## P01 — daemon-owned trust and invitation storage

Add `internal/privateaccess/` with types, bounded storage, policy snapshots and tests. Integrate ownership/deletion with `internal/productprofile/profile.go`.

Store:

- Local private Exit configuration and a random locator secret/key.
- Trusted target PeerID and display name; initial owner hints are optional, not authoritative.
- Authorized client PeerIDs, grant/revocation state and policy revision.
- Invitation IDs, expiry, consumed/pending state and only the secret material needed for the chosen enrollment protocol.

Tasks: atomic writes, strict versioning/validation, owner-only permissions, crash consistency, bounded state, concurrent enrollment/revocation and profile deletion. Keep keys and bootstrap/profile authority in daemon, not Flutter preferences. Review existing profile-content allowlists before adding files.

Gate: corrupt/unknown state fails closed; ACL does not revert to public access; profile deletion removes all private state; simultaneous use of one invitation cannot authorize two devices.

Suggested commit: `feat(private): add bounded daemon-owned trust store`.

## P02 — enforce access at all Exit admission paths

Existing integration points:

- `internal/sessionestablish/responder.go`: `PrepareAuthorizer`, authenticated `EndPeerContext`.
- `internal/runtime/m12_exit_linux.go`: `m12PrepareAuthorizer`, `m12OfferProcessor`, provisional lease allocation.
- `internal/runtime/exit_server_linux.go`, `internal/icesignal/`, `internal/sessioncontrol/`, `internal/tunnelsignal/`: audit alternative entry points.
- `internal/tunnellease/lifecycle.go`: existing activation, renewal, release and revoke lifecycle.

Tasks:

1. Add explicit public/private admission policy; preserve public semantics by default.
2. Check authenticated end-peer ACL before PREPARE/resource allocation and again at activation/renewal boundaries.
3. Bind in-flight operations to a policy revision or equivalent revocation fence; checking only at PREPARE is insufficient.
4. Revocation stops admissions, cancels pending handshakes and revokes resources by client PeerID through the existing lifecycle.
5. Audit broker/direct/legacy paths so none bypasses the policy; reject unsupported paths in private mode.

Gate: knowing PeerID, locator, endpoint or old receipt cannot allocate/activate a lease; broker identity cannot impersonate its client; revocation races leave no usable revoked session or resources.

Suggested commit: `feat(private): enforce trusted-peer Exit admission and revocation`.

## P03 — private registration and routing isolation

Add a versioned private protobuf/stream protocol, proposed `proto/irvio/v1/private_rendezvous.proto`, and matching handlers/client code in `internal/rendezvous/`. Integrate with `registration_client.go`, `registration_manager.go`, `registry.go`, `service.go`, `receipt.go`, `candidate_wire.go`, and `internal/runtime/m12_runtime_linux.go` as needed.

Tasks:

1. Use an explicitly negotiated private protocol, never an optional field silently ignored by old public servers.
2. Separate private records from public country indexes, candidate sampling and public Exit provider publication; audit both Rendezvous and direct DHT discovery.
3. Reuse signed descriptors, owner receipts/routes, heartbeat freshness and registration fencing without weakening their validation.
4. Implement exact private lookup, minimal enrollment routing and owner-specific signaling; domain-bind new authorization data to its private purpose.
5. Keep memberships/enrollment secret material out of public announcements and public APIs.
6. Require supported/validated distributors; no fallback to public registration when private support is absent.

Gate: private Exit is absent from every public country/candidate/discovery result; old distributors reject unsupported private protocol without publishing the subject; public regression suites pass unchanged.

Suggested commit: `feat(private): isolate private Rendezvous registration and lookup`.

## P04 — find registration owners across distributors

Files: `internal/serviceindex/service.go`, `internal/discovery/node.go`; add exact private discovery/provider code in `internal/rendezvous/`; integrate private registration publication lifecycle.

Design:

- Derive a dedicated domain-separated provider CID from a random private locator, not merely a publicly enumerable PeerID.
- Each live registration owner advertises itself as a provider for that locator after authenticating/accepting the private registration.
- A client bootstrapped through any distributor queries DHT for the locator and contacts candidate owner servers.
- Require authenticated eligible owner identity, the expected Exit PeerID, current descriptor, signed receipt, registration ID and server incarnation; DHT hints alone establish none of these facts.
- Reuse `OwnerRoute` and existing bounded broker forwarding for establishment after lookup.

Tasks:

1. Implement publication/refresh only for authorized live registrations, bounded by per-server resource budgets.
2. Probe multiple owner hints with bounded parallelism, no full-network scan or recursive broadcast.
3. Cache successes with expiry; invalidate failed owner routes; rediscover after migration or outage.
4. Stop refresh when a registration leaves; tolerate lingering DHT provider records and reject them at live lookup.
5. Preserve identity/authentication when a provider is initially addressless; reuse the existing resolution path.
6. Distinguish an incomplete lookup from confirmed target absence; a single timeout is not permanent offline.

Gate topology: client knows only B1, private Exit registers only on R4/R5; no R4/R5 endpoint is preloaded into the client. Lookup, establishment and traffic succeed. Repeat with one owner dead, both old owners replaced, stale records, forged providers and reordered responses.

Suggested commit: `feat(private): discover private registration owners through DHT`.

## P05 — invitation and enrollment flow

Add invitation codec and enrollment logic in `internal/privateaccess/`; add a versioned enrollment protocol/carrier, reusing existing identity validation and routed control transport where appropriate.

Tasks:

1. Encode version, host PeerID, locator, invitation ID, expiry, enrollment secret and host authentication; use a separate scheme from bootstrap codes.
2. Optionally embed a validated existing bootstrap bundle; otherwise require bootstrap import explicitly.
3. Authenticate the destination against the invitation's host PeerID. Protect enrollment end-to-end even when distributors carry it.
4. Bind secret proof and approval to client PeerID, invitation ID and fresh exchange context; never log/transmit the reusable secret in plaintext to the distributor.
5. Obtain host approval and atomically consume the invitation only under the defined enrollment state machine.
6. Handle duplicate retries idempotently, expiry, refusal, cancellation, crash/restart and host-offline cases.
7. Persist the resulting trusted identity/access state; enrollment alone must not CONNECT or enable public sharing.

Gate: replay, tampering, copied QR racing, unapproved client and malicious broker cannot grant unauthorized access. Revoke blocks data-plane access even if the former member still knows the locator. Locator rotation/privacy after revocation must be explicitly documented, not implied.

Suggested commit: `feat(private): add authenticated single-use enrollment`.

## P06 — pinned PeerID connection and recovery

Files: `internal/exitselector/types.go`, `candidate.go`, `provider.go`, `selector.go`; `internal/runtime/m12_connector_linux.go`, runtime connection configuration; `internal/sessionestablish/{initiator,responder,transcript}.go` and related protocol definitions if needed.

Tasks:

1. Introduce a typed target: public country OR private profile/PeerID. Preserve the existing public API through a compatibility adapter where practical.
2. Implement exact-target candidate acquisition using P04; only the selected PeerID is admissible, regardless of other available public/private peers.
3. Extract a minimal broker route-resolution interface from the concrete public CandidateProvider dependency.
4. Separate country selection constraints from common identity, descriptor, freshness, endpoint and AWG validation.
5. Resolve the current country-bound PREPARE transcript explicitly with negotiated private semantics; never invent `ZZ` or let UNKNOWN bypass public eligibility checks.
6. Reuse ICE, AWG, TunnelLease and data-plane promotion; any required transcript extension must bind the private mode/target cryptographically.
7. On failure re-resolve owner routes for the same PeerID; do not fall back to public country selection or another friend.

Gate: pinned identity survives owner migration, recovery, NAT remap and retry; changing countries on the host is not misinterpreted as permission to select another peer. No unrelated Exit can become ACTIVE/RESERVE/COLD in this mode.

Suggested commit: `feat(private): add pinned Exit target and recovery`.

## P07 — private hosting and on-demand lifecycle

Files: `cmd/irviod/product_linux.go`, `internal/productruntime/`, `internal/productintent/store_linux.go`, `internal/runtime/m12_runtime_linux.go`, Exit startup/teardown, profile reset and idle shutdown integration.

Tasks:

1. Represent upstream and hosting intents separately; support private-hosting-only without a fake upstream connection.
2. Never publish public contribution from private mode; make switching modes explicit and teardown-safe.
3. Hosting-only uses normal WAN for owner traffic while downstream Safety Policy/anti-multihop remain installed as required.
4. Full Disconnect/Shutdown first clears recovery intent, stops new admissions, withdraws registration, drains/revokes downstream leases, closes own tunnel and leaves network participation.
5. Preserve fail-closed behavior for an active upstream intent and ordinary WAN behavior for hosting-only operation.
6. Version session intent storage deliberately: the existing schema is country-only and bounded to 256 bytes. Update strict validation, size limits and migration tests; do not store secrets there.
7. Restore appropriate same-boot intent after crash; a new boot remains inactive. GUI death does not disable hosting or a tunnel.
8. Integrate idle auto-exit and profile deletion so private hosting cannot be killed as an idle daemon and cannot survive full deletion.

Gate: every allowed intent combination has correct status, routes, DNS, nft state, public/private advertisements and resource ownership; full shutdown returns to a clean idle/stopped state with no hidden contributor.

Suggested commit: `feat(private): add independent private hosting lifecycle`.

## P08 — daemon-owned Local API

Files: `proto/irvio/local/v1/control.proto`, `internal/localapi/{server,client}.go`, generated Go/Dart bindings.

Tasks: version/capability negotiation; bounded commands to list private profiles, create/import/revoke invitations, list/approve/revoke members, set hosting, connect a private target and query combined status. Apply existing Local API credential checks and command/discovery serialization. Redact secrets in logs/events/errors; UI disconnect must not accidentally disable only half of the promised lifecycle.

Gate: unsupported daemon is clearly reported; unauthorized local users cannot manage grants; calls remain responsive during lookup and teardown; profile deletion cancels private work.

Suggested commit: `feat(localapi): expose private network intents and membership`.

## P09 — Flutter Private Network UI

Files: new `ui/lib/screens/private_network_screen.dart`; `ui/lib/app/{irvio_shell,irvio_app_controller,irvio_app}.dart`; `ui/lib/models/irvio_models.dart`; `ui/lib/irvio/linux_irvio_client.dart`; `ui/lib/l10n/irvio_localizations.dart`; QR helpers and tray integration as needed.

Tasks:

- My connections: import QR/link, name saved Exit, show lookup/enrollment/connected/recovering/offline/denied states, connect only on explicit action.
- Share my connection: private hosting control, invitation QR/link, pending approvals, allowed devices, active clients and revocation.
- Immediate UI feedback; no blocking network/crypto work on UI thread.
- Clearly display hosting-only activity; global Shutdown still shuts down everything.
- Keep authoritative grants and secrets in daemon, not preferences; preserve public HOME flow and all supported languages.

Gate: two people complete enrollment and connection without engineering flags; cancel/retry, GUI restart, denied access and unsupported server cases are understandable and leave no unintended activity.

Suggested commit: `feat(ui): add Private Network onboarding and controls`.

## P10 — integrated lab and regressions

Add reusable bounded harness/scripts under the project's testing conventions, with artifacts outside production sources. Record results in `docs/testing/private-irvio-v1-validation.md`.

Required scenarios:

1. Cross-distributor discovery with disjoint initial bootstrap/registration owners.
2. Owner loss/migration/restart, stale provider/receipt, addressless provider, expired descriptor and locator refresh.
3. Invitation replay/tampering/concurrent consumption, revoked or unknown PeerID, broker impersonation and all admission bypass paths.
4. Revoke during PREPARE, ICE, lease activation, renewal and established forwarding.
5. Public/private isolation, mixed-version distributors and no public fallback.
6. Public/EIM/APDM/shared-NAT clients; bounded failure on incompatible NAT, no promised universal reachability.
7. Real ICMP/DNS/HTTP/UDP user traffic and egress identity; WAN leak and anti-multihop oracles.
8. Hosting-only, private upstream with/without private hosting, full leave, GUI death, daemon crash/recovery and fresh boot.
9. At least 100 lifecycle cycles with FD/netlink/pipe/socket/TUN/nft/route/DNS/lease/registration baseline-vs-final checks.
10. Affected M8/M9/M10/M11/M12, bootstrap, addressless-provider, COLD recovery, NAT-remap, DP promotion, Safety Policy and Local API/UI regressions.

All tests run on the mini-PC, not the workstation. Classify limitations and NOT EXERCISED explicitly. No new multihour soak unless separately approved.

Suggested commit: `test(private): cover distributed discovery, authorization and lifecycle`.

## P11 — local packaging and handoff, no publication

Files: `packaging/ubuntu/` and local installation/product docs.

Tasks: build a distinctly named private-preview tarball from a recorded clean commit; audit exact allowlist/checksums; verify install/update/uninstall and UI/daemon protocol compatibility on the mini-PC. Retain public bootstrap compatibility and do not replace the published Alpha artifact.

Final handoff includes commits, binary/archive SHA-256, scenario results, migration requirements, resource/security evidence and limitations. Explicit approval is required before updating GitHub, public releases, website links or production distributors.

Suggested commit: `build(private): package locally validated private preview`.

## Completion checklist

- [x] P00 Contract and security decisions frozen.
- [x] P01 Trust storage and deletion safe.
- [x] P02 Exit-side enforcement and revocation proven.
- [x] P03 Private/public registration isolation proven.
- [x] P04 Exact target found across different distributors.
- [x] P05 Enrollment authenticated, single-use and replay-safe.
- [x] P06 Pinned connection/recovery has no public fallback.
- [x] P07 Hosting, disconnect, crash and boot semantics proven.
- [x] P08 Local API negotiated, authorized and responsive.
- [x] P09 UI flow complete and localized.
- [x] P10 Integration, security and resource gates recorded.
- [x] P11 Local preview packaged; no external publication.
