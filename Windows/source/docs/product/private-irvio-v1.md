# Private IRVIO v1 contract

Implementation branch: `private-irvio`. This is a contract, not a claim that the feature is operational.

## Intent and publication

Upstream is NONE, PUBLIC(country), or PRIVATE(saved target PeerID). Hosting is OFF or PRIVATE. PUBLIC upstream retains its existing public contribution behavior and cannot coexist with PRIVATE hosting in v1. Switching public/private requires full teardown before enabling the new intent. PRIVATE upstream may coexist with PRIVATE hosting; downstream traffic must still leave the physical WAN, never the upstream tunnel. Hosting alone leaves owner traffic on ordinary WAN.

Full Disconnect and Shutdown clear both recovery intents before teardown, close admissions, withdraw advertisements, revoke downstream leases, close the upstream, leave discovery, restore DNS/routes and release protection. Only then may global status become IDLE. Hosting-only has an explicit active status and prevents idle auto-exit. GUI closure/crash is not Disconnect. Same-boot daemon recovery restores intent; fresh boot does not.

Private registrations use a separately negotiated stream and separate registry/indexes. Unsupported servers never receive a public registration as fallback. Private Exits must not appear in public country lists, samples, or direct Exit provider publication. Signed identity, AWG bindings, M11 owner eligibility, lease fencing and Safety Policy remain mandatory.

## Identity, invitation and enrollment

The authoritative store belongs to the daemon. It contains the local host identity binding, a random 256-bit locator, trusted Exit identities, invitations and member grants. Display names are not identities. Missing store means no private grants, not permission to accept private traffic. Invalid/unknown store fails closed; it is never silently reset.

An invitation contains a version, host PeerID, random locator, random 128-bit invitation ID, expiry and random 256-bit enrollment secret, authenticated by the host identity. It uses a separate `irvio-private:v1:` scheme. Bootstrap import is required separately in v1; an invitation is not an implicit network join or CONNECT.

The v1 link is at most 2048 bytes: the prefix followed by canonical unpadded base64url of a libp2p signed record. Its record domain is `irvio/private-invitation/v1`, codec `84 80 c0 01`; the deterministic `PrivateInvitationPayload` includes version, host identity, locator, invitation ID, secret, creation and expiry timestamps. Unknown fields, duplicate/noncanonical protobuf encodings, zero locator/ID/secret, lifetime beyond 24 hours and creation over one minute in the future are rejected. Only an Ed25519 signer matching the embedded host identity is accepted. The secret is deliberately present in the invitation handed to a friend; the link must never be logged or exposed to a distributor in plaintext.

Enrollment is carried through a bounded distributor byte relay, but runs the existing libp2p TLS identity transport end-to-end. Verify the remote host against the invitation PeerID before sending the secret. The host authenticates the client's transport PeerID. Do not design a new key exchange or send the secret in clear to the relay. Relay identity never substitutes for end-client identity. Application requests bind invitation ID and the authenticated client; transport authentication/replay protection is supplemented by durable invitation consumption.

The carrier negotiates `/irvio/private-enrollment-relay/1.0.0`; its initial request contains only locator, target and signed-route-derived owner identifiers, not invitation secrets. The owner opens `/irvio/private-enrollment-endpoint/1.0.0` on an existing authenticated connection to its live registered Exit, with dialing disabled. A one-byte ready response precedes end-to-end TLS. Relay limits are eight concurrent sessions per owner, thirty seconds and 64 KiB per direction; both copying workers are joined before releasing the slot. It is not an unrestricted address/port proxy. The Exit endpoint must accept carriers only from its current registration owners, independently of TLS end-client authorization.

First valid redemption atomically reserves the invitation for one client and creates a pending approval. Retries by that client are idempotent; any other client is denied. Host approval consumes the invitation and grants that identity atomically. Expiry applies to unapproved requests; approval never silently grants after expiry. Refusal/cancellation consumes the invitation without a grant. Restart retains reservations, preventing duplicate use. A copied invitation can deny its intended user by winning the reservation race, but cannot obtain access without explicit host approval.

Revocation persists before success is reported. It rejects new PREPARE/allocation, invalidates in-flight authorizations and boundedly revokes every lease for the member. Activation and renewal use an admission fence coordinated with revoke, not an unchecked earlier boolean. A revoked grant cannot be restored by replaying its consumed invitation. A new invitation and explicit approval are required.

## Discovery and establishment

Provider keys use a new domain-separated CID from the random locator. Eligible registration owners publish themselves, not the Exit. Clients learn owners through DHT, validate owners and signed live receipts, and require the exact selected target identity. Stale DHT hints are not proof of liveness or access. Hints are never authorization. Loss of all owners causes bounded rediscovery of the same target, not public fallback or selection of another saved friend.

Private establishment has an explicitly negotiated version and signed target-kind/PeerID binding. Public country-bound transcripts remain unchanged. Private requests cannot be downgraded into public requests, use a fabricated country or relax descriptor/AWG/freshness validation. Geographic country is descriptive, not private target selection authority.

Public transcript version 1 retains country selection and forbids `private_target_peer_id`. Private transcript version 2 requires empty country and `private_target_peer_id` equal to the signed recipient identity in PREPARE. Existing signature/hash domains still cover the complete payload, including version and target. Every predecessor link requires the same version. A private responder must explicitly enable private mode with a non-nil admission policy; public responders reject version 2 and private responders reject version 1. Private establishment validates signed private-only Exit capability, availability and exact descriptor/AWG metadata before PREPARE and after READY. ICE/AWG/TunnelLease wire messages themselves are unchanged.

## Initial v1 bounds

### Private Rendezvous wire boundary

Private registration and exact lookup negotiate `/irvio/private-rendezvous/1.0.0`. The initial frame uses private protocol version 1; subsequent registration heartbeats, updates, acknowledgements and owner-routed signaling reuse existing messages inside that private stream. Every frame is limited to 16 KiB and private descriptors to 8 KiB. Unsupported negotiation fails without retrying public registration.

A private descriptor has `CapPrivateExit` and feature `private-exit-v1`, never `CapExit`. Its country may be unknown: geographic eligibility is not private authorization. Public descriptor validation still requires its existing capabilities and country. Private registries have no public country index or sampling path.

Private receipts use record domain `irvio/private-registration-receipt/v1`, codec `83 80 c0 01`, and a locator commitment SHA-256 of `IRVIO-PRIVATE-LOCATOR-V1\x00` followed by the 32 locator bytes. They retain existing subject, descriptor, owner incarnation, registration ID and expiry bindings. Public and private receipt verification are not interchangeable.

The owner provider CID is raw CIDv1 with SHA2-256 of `IRVIO-PRIVATE-OWNERS-V1\x00` followed by the locator. A lookup result must match both locator and selected Exit identity. Receipt validity alone does not establish owner M11 eligibility; discovery must verify that separately. Signaling replies are fenced to the current registration channel and the request's owner, target, deadline and hop count.

| Item | Bound |
| --- | --- |
| Trust file | 1 MiB, schemas v1/v2, strict JSON, owner-only; importing an enrollment upgrades to v2 |
| Saved targets / member records | 128 / 256 |
| Invitations, including consumed records | 128 |
| Display name | 80 UTF-8 bytes, no control characters |
| Invitation validity | at most 24 h; explicit replacement after expiry |
| Registration owners per Exit | 3, existing registration lifecycle |
| Private lookup providers / parallel calls | 8 / 3 |
| Lookup round | 10 s; cancellation propagated |
| Cached owner result | at most 30 s and never beyond receipt validity |
| Control frame | 16 KiB, validated before allocation |
| Enrollment transport | 30 s handshake/request; approval is polled, not an open stream |
| Concurrent enrollment exchanges per Exit | 8 |
| Per-client pending invitation reservations | 4 |
| Private owner records per distributor | 1024 |
| Provider publication workers per distributor | 4, coalescing bounded queue |
| Revocation cleanup | 10 s; report incomplete cleanup, keep admissions closed |

Retries use existing recovery budgets with cancellation; no unbounded per-request loops or goroutine queues. Expired/consumed invitations may be explicitly pruned without removing member revocations. Grant revisions never decrease or wrap.

The filename remains `private-access-v1.json`; schema v2 adds bounded durable imported-enrollment records. Readers accept v1 without enrollments and v2, reject unsupported versions and refuse enrollments encoded as v1. Schema upgrades do not change the invitation or network protocol version. Cancellation removes the pending introduction; only authenticated host approval creates a connectable saved target.

## Errors and threat boundaries

Distinct errors: UNSUPPORTED, INCOMPLETE_LOOKUP, OFFLINE, DENIED, PENDING_APPROVAL, EXPIRED, INCOMPATIBLE, CAPACITY, INVALID_STATE, CONNECTION_FAILED. Do not leak invitation existence to unauthenticated requests through detailed errors. Local API may show detailed local state without secrets in logs.

A malicious distributor may censor, reorder, replay or return forged routing data, but cannot grant membership or impersonate the selected Exit. Tests must cover those actions, stale receipts, owner migration, downgrade attempts and concurrent revocation at every admission boundary. Losing durable writes fails closed. Host root compromise, deliberate profile rollback by root, traffic observation by an authorized Exit and universal NAT traversal are outside the guarantee.

Knowing a locator can reveal owner availability even after revocation; revocation blocks traffic, not knowledge. Locator rotation invalidates saved locators and requires explicit reinvitation. No claim of anonymity, hidden metadata, LAN routing, chaining or serverless availability is made.
