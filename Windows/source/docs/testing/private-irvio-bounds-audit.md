# Private IRVIO — bounded-state audit

Inspected after `7aa4f9b`; canonical contract is `docs/product/private-irvio-v1.md`. Tests run only on the mini-PC. This is a requirement-to-implementation check, not an exhaustive security verdict.

| Contract boundary | Enforcement | Inspected regression / evidence |
| --- | --- | --- |
| 1 MiB trust file, schemas v1/v2 | `privateaccess/store_linux.go`: LimitReader(MaxBytes+1), duplicate-key/depth/schema validation; `state.go`: version validation | `TestStrictDecode`, corruption tests, imported enrollment version tests |
| Owner-only files, exclusive writer, identity binding | `Open`, `secureFile`: no-follow directory/file access, regular file, mode 0600, current UID, single link, flock, host match | `TestUnsafeStorageAndIdentityBinding`; not every filesystem fault is injected |
| Atomic durable trust update | Clone/validate, exclusive temp file, fsync, renameat, directory fsync; failed persistence poisons store | `TestFailedWritePoisonsStore`, reopen/rollback tests |
| 128 targets / 256 member records | `State.validate`; changes validated before persistence | New `TestTargetAndMemberCapacityRollback` fills both limits, rejects additional target/approval without consuming pending invitation or changing durable state |
| Display name ≤80 UTF-8 bytes, no controls | `validName` | New test accepts 40 Cyrillic characters (80 bytes), rejects one extra byte, newline and invalid UTF-8 |
| Revision never wraps | `State.validate` rejects zero/MaxUint64; rejected clone is not committed | New `TestRevisionExhaustionNeverWrapsOrPersistsMutation` starts at MaxUint64−1, refuses mutation and verifies equality after reopen |
| 128 invitations; 4 pending per client; expiry ≤24h | `State.validate`, `CreateInvitation`, `Reserve`, `Approve` | `TestExpiryAndCapacity`, approval-expiry and single-use race tests |
| Signed invitation ≤2048 bytes, canonical, identity-bound | `invitation.go` and existing signed record validation | Authenticated roundtrip/bounds/malformed payload tests, including zero locator/secret/ID and future creation |
| Enrollment bounded carrier | Relay/endpoint 30s contexts; 8-slot gates; 64 KiB per direction; authenticated owner/target | Existing registered-owner, owner-fencing, byte-bound and cancellation tests; no saturation-load claim from constants alone |
| Private frames 16 KiB / descriptors 8 KiB | `rendezvous/types.go`, private codec and descriptor verification | `TestPrivateFrameBoundsAndVersion`; descriptor enforcement inspected separately |
| Discovery 8 provider hints / 3 workers / 10s | `discovery/private_owners.go` | `TestPrivateOwnerVerificationBoundAndCancellation`: three blocked workers, cancellation joins all; real DHT/addressless tests separate |
| Cached owner/candidate ≤30s and receipt expiry | `rendezvous/private_adapter.go` caps descriptor/receipt expiry and rejects stale resolve | `TestPrivateCandidateProviderFencesCache`: cap, exact expiry, mutation and replacement fences |
| Publication 4 workers, bounded live set | `private_publication.go`: four running slots, 1024 live locators, coalesced deadlines, cancellation and worker join | Publication withdrawal/shutdown and duplicate/refresh tests; not a 1024-record load experiment |
| Private profile removed, never reused | Store close before `productprofile.DeleteContents`; allowlisted trust/temporary files | `TestProfileDeletion` removes trust, lock and interrupted write; closed store remains unavailable; active-network deletion is a separate gate |

## New boundary regression result

Mini-PC `go test -race -shuffle=on -count=5 -timeout=120s ./internal/privateaccess ./internal/productprofile`: PASS (4.576s / 1.029s). New target/member/name/revision checks use production persistence and reopen; bulk capacity setup is an internal test fixture, not a claim of 256 end-to-end enrollments. No new production defect or production change was required.

The two new tests strengthen previously indirect boundary evidence. This document does not supersede integrated traffic, fail-closed, native UI, host DNS, reboot or package validation reports, and does not mark P00–P11 complete.

Private discovery/Rendezvous regressions were also rerun: `go test -race -shuffle=on -count=3 -timeout=180s ./internal/discovery ./internal/rendezvous -run Private` PASS (5.995s / 9.193s). This filter includes the named private publication/cache/owner tests above, not the full public package suites.
