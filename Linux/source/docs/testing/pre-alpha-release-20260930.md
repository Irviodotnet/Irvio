# pre_alpha_release — 2026-09-30

Baseline: `main`, `124a8a1db72ccf4d2ff7ff48c24d15799419cc47`.
This report accompanies the `pre_alpha_release` commit. Package `BUILD_INFO.json`
records its exact source HEAD and binary hashes after compilation.

## Changes included

- Linux WAN route detection rejects local/loopback routes rather than accepting
  them as a usable physical egress. See `wan-route-autodetection-regression.md`.
- Country queries exclude the authenticated requester's PeerID, not its IP.
  A country remains selectable when another eligible Exit exists, including
  another peer behind the same public IP. Global advertisements are unchanged.
  See `requester-country-filter-20260930.md`.
- UI immediately indicates a pending CONNECT with a calm blue ring and halo;
  this does not change network selection or acquisition timing. QR export,
  larger initial window and Settings layout changes are also included.
- Ubuntu package date and source provenance match the current build; production
  payload continues to use an explicit allowlist.

## Deleted bootstrap / Rendezvous server regression

Deleting the New York server exposed a discovery adapter defect:
`FindRendezvous` returned authenticated live peers together with a bounded
  lookup timeout, but `DiscoveryServerProvider.lookup` discarded those peers.
Cold country queries could therefore report UNAVAILABLE despite reachable
Rendezvous servers and valid Exit supply.

The adapter now preserves both the verified peers and the error. Existing
aggregation can report PARTIAL and use these peers; successful peers enter the
existing cache. Lookup budgets, wire formats, identity verification, M11 quorum
and ExitSelector semantics are not changed.

Focused regressions cover a cold country query and country-to-general lookup
fallback returning peers together with `context.DeadlineExceeded`.

## Delete Profile / socket activation regression

The socket-activated daemon consumed systemd's activation descriptor/environment,
then attempted `exec` after deleting a profile. The replacement no longer had
the inherited listener and tried manual socket creation in systemd's directory;
the existing directory permission check correctly rejected this operation.

Socket activation is now recorded before adoption. After a completed profile
reset an activated daemon exits successfully, leaving the systemd socket
listening. The next UI API request activates a fresh process. Manual launches
retain their existing exec behavior. No directory permissions or API access
checks are relaxed.

Tests cover intentional activated exit, manual exec and three successive child
processes adopting a real inherited Unix socket while its directory permissions
remain unchanged.

## Verification

| Check | Result |
| --- | --- |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `go test -shuffle=on -count=10 ./...` | PASS |
| `go test -race ./...` | PASS |
| `go test -race -shuffle=on -count=5 ./...` | PASS |
| Flutter analyze | PASS |
| Flutter test | PASS, 76 tests |
| `git diff --check` | PASS |
| Mini-PC cold country query, New York removed | PARTIAL with CZ, DE, NL |
| Mini-PC Delete Profile / reactivation / reimport | PASS, two real cycles |
| Mini-PC systemd failure restart count | 0 |
| Mini-PC CONNECT to NL, HTTPS egress | PASS, Amsterdam egress |
| Mini-PC DISCONNECT | DISCONNECTED, contribution false |

Only the mini-PC was used for live client networking. No workstation HYDRA
connection or host routing change was made. Final mini-PC state is disconnected.
This is focused validation, not a new soak or a claim that every privileged
release-acceptance gate has been repeated.
