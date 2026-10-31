# Desktop connection startup optimization — 2026-10-14

## Scope

Baseline: `windows-port`, `557d0a2`. Tests ran on IRVIO-Win11-Peer and the Linux mini-PC, not on the workstation. No server deployment or installer changes were made.

The existing UI already requests available countries in the background. This change reuses the resulting verified Rendezvous hints when CONNECT creates the active runtime; it does not start an Exit, user tunnel, or ProtectedMode while idle.

## Changes and invariants

- Windows: set `DadTransmits=0` only on the newly owned Wintun LUID, for IPv4 and IPv6, and verify the setting. Address readiness, rollback, ownership checks and MTU remain unchanged. Physical interfaces are not modified.
- Linux and Windows: an in-memory discovery handoff is accepted for at most 30 seconds and only for the same local PeerID and identical bootstrap address list. It is exported only after successful idle discovery and retained only after successful probe cleanup.
- The handoff contains verified Rendezvous identities and bounded address hints, not Exit candidates, leases, STUN observations or ICE sessions. It does not persist to disk.
- A fresh provider uses the hints with its existing background refresh and failure-suppression logic. Expired hints, a changed profile and an empty cache fall back to normal discovery. An import cannot overwrite an active provider or bypass its failure suppression.
- Candidate RPCs, authentication, ExitSelector, ACTIVE/RESERVE/COLD behavior, fresh STUN, AWG, BIND and protection ordering are unchanged.
- Disconnect still closes network participation; cached metadata owns no sockets or background workers.

## Measurements

Elapsed times use a local monotonic stopwatch from immediately before the CONNECT API call until the first observed CONNECTED status. API process startup and status polling are included. The preliminary country query is outside this interval, matching UI background preparation. This is not a cold-boot-to-traffic metric.

| Final build / mode | Attempts | CONNECTED | HTTPS success | Min | Median | p95 / max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Windows Global, CZ/DE/FI | 9 | 9 | 9 | 2.866 s | 3.146 s | 4.164 s |
| Windows Private, Linux friend | 5 | 5 | 5 | 2.759 s | 2.922 s | 3.512 s |
| Linux Global, CZ/DE/FI | 6 | 6 | 4 | 1.479 s | 1.797 s | 2.362 s |

p95 uses nearest rank; with these small samples it equals the maximum. Windows median completion of the external HTTPS request, measured from CONNECT, was 4.139 s for Global and 3.803 s for Private. Private testing used a home-LAN Linux host and a NATed Windows VM, not a distant/mobile peer.

The preceding Windows baseline measured Global CONNECTED median 13.804 s and Private median 6.434 s among successful attempts. That baseline used the diagnostic daemon from the previous investigation, so this is not a fully controlled benchmark of every variable. The intermediate DAD-only campaign measured Global median about 4.7 s; its later attempts overlapped build work and are not the final performance result.

The isolated native TUN measurement directly located the removed delay:

| Operation | Before, 3 samples | After, 3 samples |
| --- | --- | --- |
| Initial address readiness | 3.126–3.226 s | below millisecond timer resolution |
| Entire new TUN creation | 3.403–3.435 s | 0.170–0.287 s |
| Additional session address | 3.482–3.508 s readiness | 0–8.3 ms entire operation |

`M12_DISCOVERY_WARM_REUSED` appeared in all 14 final Windows attempts and all 6 Linux attempts. No extra optimization was added to chase a fixed 3.5-second SLA; approximately five seconds was accepted as sufficient for Windows.

## Validation

- Linux: `go test` for `internal/rendezvous`, `internal/runtime`, `internal/tunnel`, `cmd/irviod`; `go test -race` for rendezvous/runtime/daemon; `go vet` for all four packages — PASS.
- Windows: ordinary tests and vet for the same four packages — PASS.
- Windows privileged native tunnel suite — PASS, including IPv4/IPv6 encrypted AWG round trips, generation replacement, source selection, dynamic address rollback, 100 borrow cycles and owner cleanup.
- Windows native Exit device ownership — PASS for both cycles, including listener readiness, source ownership, exclusive journal, rollback and teardown.
- Non-IRVIO interface index/family/DAD/MTU snapshot remained unchanged in the dedicated DAD validation.
- Linux CONNECT/DISCONNECT used the real host network namespace as the ordinary `ubuntu` user; SSH itself is in a separate management namespace. Final route and system DNS snapshots exactly matched the pre-connection baseline.
- Warm-cache regressions cover expiry, future timestamps, wrong identity, oversized input, failed peers, fallback, background country refresh, bootstrap replacement, query/cleanup failure and absence of active idle resources.

## Observations and limits

Two Linux HTTPS requests to `www.microsoft.com` timed out after eight seconds despite CONNECTED. The subsequent connection to each of those countries completed with successful HTTPS. This does not establish the cause of the two timeouts; they are retained as failed probes, not counted as PASS. All 14 final Windows HTTPS requests succeeded.

The separate `TestWindowsNativeClientDataPlane` fixture could not run because its required `IRVIO-W05-Owner` recovery service is absent. It failed before dataplane setup; this gate is not claimed as passed. Actual product sessions and the native Exit/TUN suites above did run. A full WFP leak matrix, whole-tree release gates and a long soak were not rerun.

Harness-only aborted preparations were preserved: missing `test/support` in the first Windows source archive, a competing GUI country query, and Linux DNS inspection from the SSH management namespace. They were corrected before the final measurements. No production changes were made to hide these harness issues.

The earlier unrelated edits to `internal/runtime/client_dataplane.go` and untracked files were not included in these builds or commits.

## Installed builds and evidence

Windows daemon: `C:\Program Files\IRVIO\daemon\irviod.exe`

SHA-256: `812001f32b097a7c34eff93dc802e6497daf63130ea783ee25f1c2e80ddda2ce`

Linux daemon: `/opt/irvio/bin/irviod`

SHA-256: `74e2d95557b7d550983b40c5ef578acd53bf49df453a213d04394e892d15eec0`

Both are trimpath builds from clean baseline sources plus the scoped changes above. The UI binaries are unchanged; they already initiate background country discovery. UI was reopened on both test machines. Windows ended disconnected with no IRVIO adapter; the mini-PC was restored to Private hosting.

Local evidence: `artifacts/connect-fast-20261014/` (not committed). Original baseline: `artifacts/windows-connection-timing-20261014/`.

Windows full final campaign: `C:\IRVIO-Lab\Connection-fast-final-02-20261014`. Build/native logs: `C:\IRVIO-Lab\Connect-fast-20261014`.

Linux source/build/tests: `/home/ubuntu/irvio-connect-fast-20261014`; host-namespace campaign: `measurements-03/` beneath that directory.
