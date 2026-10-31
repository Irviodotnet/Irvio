# Private IRVIO — renewal/revocation replay boundary

Baseline: `b693188`, branch `private-irvio`. Tests ran only on the mini-PC in `/home/ubuntu/irvio-private-validation`; no production distributor or workstation runtime was changed.

## Confirmed defect

`TestPrivateRevocationFencesInFlightNoiseRenewal` failed 10/10 repetitions under the race detector before the fix. A real TCP/Noise session renewed an authorized private lease while its resource renewal callback was held. Revocation waited for that operation, then removed the lease and invoked the resource revoker exactly once. However, repeating the original RENEW RequestID on the existing authenticated connection returned the cached successful response after revocation had completed.

This proves stale control-plane success, not restored forwarding or an ACL bypass in the dataplane. The lifecycle resources were already gone. The shared replay cache also served lease-bearing STATE and AWG rotation responses without checking current authority.

## Correction

Before replaying a lease-bearing success, `internal/sessioncontrol/server.go` now looks up the cached result's lease generation with the authenticated client and original session token. Using the resulting generation, rather than the pre-renewal request generation, preserves idempotent retries without renewing twice. Revoked, expired or replaced leases cannot receive cached success; remaining lifetime is recalculated instead of replaying an obsolete duration. Error responses and RELEASE acknowledgments retain their existing idempotent semantics. No wire format changed.

## Checks

- Full `privateaccess`, `sessioncontrol` and `tunnellease` packages: `go test -race -shuffle=on -count=10 -timeout=180s` PASS; final package times 6.066s / 17.951s / 1.065s.
- `TestCachedLeaseSuccessRequiresCurrentAuthority`: RENEWED, STATE and AWG_ROTATED results keep valid live replay, reject replaced generations and revoked leases, and do not repeat resource mutation.
- Existing session-control retry, rotation and RELEASE tests passed in the full package run.
- The composed private test requires an explicit remote protocol error after revocation, not merely a timeout or broken connection.
- After strengthening that assertion: focused `-race -count=20 -timeout=90s` PASS (2.264s); `go vet` for all three affected packages PASS.

## Evidence limits / follow-up

The private test uses production identities, trust policy, lifecycle, TCP and Noise, with controlled resource callbacks. It does not itself exercise AWG/TUN forwarding or every dataplane renewal interleaving. Existing real ICE/revocation and traffic gates remain separate evidence.

This is a new production change after preview `01cfb88`: that archive and installed mini-PC binary do **not** contain this correction. Refresh the local preview and run affected runtime/dataplane gates before final acceptance. No GitHub publication is authorized.
