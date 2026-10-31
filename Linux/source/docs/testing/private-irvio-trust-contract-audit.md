# Private IRVIO — trust/enrollment contract audit

Audited against production preview `01cfb88`; this follow-up adds only tests and documentation. It does not change the binary in the running 100-cycle campaign and does not close the remaining integrated lifecycle gates.

## Concrete checks

| Requirement | Implementation and evidence |
| --- | --- |
| Bounded, authenticated invitation | `invitation.go`: 2048-byte cap before decoding, strict canonical base64url, signed-record domain/codec, Ed25519 identity binding, canonical payload/unknown-field rejection, nonzero locator/ID/secret, 24-hour lifetime and future-clock check. `TestInvitationAuthenticatedRoundtripAndBounds` and `TestInvitationRejectsAuthenticatedMalformedPayload` exercise tampering and malformed authenticated data. |
| Durable identity-bound trust | `store_linux.go`: directory/file ownership checks, NOFOLLOW, regular 0600 single-link files, exclusive writer flock, bounded read, strict duplicate/unknown JSON rejection, write/fsync/rename/directory-fsync. Failed persistence poisons the open store. Storage tests cover ownership, symlink, competing writer, corrupt state and failed-write denial. This is not an assertion that malicious root rollback is prevented. |
| Schema migration | New stores use v1; importing an invitation upgrades to v2 for durable enrollment records. `state.validate` accepts only v1/v2 and refuses nonempty enrollments in v1. The TLS restart test reopens the imported store and completes approval without granting access at import. Updated the product contract's stale v1-only table entry. |
| Single-use approval | Reservation and approval are serialized durable mutations; `TestSingleUseRace`, `TestApproveRevokeRace`, expiry/capacity tests and policy tests cover competing clients, revocation and expiry. A copied QR can win reservation but cannot bypass approval. |
| End-to-end enrollment | `enrollment_linux.go` authenticates pinned host and end-client via libp2p TLS before secret exchange, with 30-second deadline/cancellation, eight slots and a 4096-byte application frame. It never grants membership itself. |
| Bounded indirect carrier | `private_enrollment_relay.go` limits each direction to 64 KiB and routes only to the registered endpoint; endpoint owner-fencing tests remain distinct from inner TLS identity tests. The owner necessarily sees routing metadata, not an anonymity guarantee. |
| Import is not CONNECT | `ImportInvitation` saves a pending introduction without a target; only the authenticated APPROVED path stores a target. Cancellation revision fencing prevents late responses from resurrecting replaced work. `TestImportedEnrollmentTLSRestartAndCancellationFence` and wrong-host/self/key-mismatch tests directly cover those paths. |
| Public/local presentation | Existing command-level tests assert state encoding excludes invitation links, enrollment secrets and locator bytes. Local API error tests assert generic sanitized messages even when the underlying error contains secret text. Private access/enrollment source contains no direct logging calls; this limited source inspection is not a proof about every future formatter/caller. |

## Added carrier confidentiality observation

`TestEnrollmentTLSApprovalAndRevocation` now wraps both ends of the actual TCP carrier below libp2p TLS and captures successful writes. Both directions must carry nonempty data, and neither may contain the reusable invitation secret in its raw 32-byte or hexadecimal representation. Existing exercises cover repeated pending reservation, another client's copied invitation, approval/status, revocation and substituted host identity. Capture buffers stay in test memory; no secret or wire dump is emitted or committed.

This is a regression against accidental plaintext delivery at the selected transport boundary, not a replacement for TLS cryptographic analysis or a statement that ciphertext has no metadata. Outer owner routing remains separately tested and necessarily carries locator/target information.

Mini-PC: unfiltered `GOMAXPROCS=2 go test -p=1 -race -shuffle=on -count=10 ./internal/privateaccess` PASS 5.269s; `go vet ./internal/privateaccess` PASS. Tests ran in the SSH management namespace, without modifying the active privileged lab, installed daemon or clean preview checkout. No production source was changed.

Remaining acceptance boundaries include real revocation/activation/renewal interleavings, fresh boot after an active session, native tray/rendering behavior and final package/resource results. This audit does not waive them.
