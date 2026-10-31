# Stale bootstrap / country discovery follow-up — 2026-09-30

Baseline: `838190e13fc4cb2be5c84e7f25e90fe370465780` (`pre_alpha_release`).

The preceding fix preserved partial verified DHT results, but did not prevent
a dead Rendezvous provider hint from consuming the entire discovery deadline.
Cold idle queries also imposed a two-second discovery cutoff inside their
five-second total budget. The mini-PC reproduced country-list instability:
NL was missing from two of four cold queries despite a healthy Amsterdam Exit.
Candidate selection and union adapters also discarded partial verified peers.

## Changes

- Rendezvous provider authentication uses four bounded workers, each with a
  1500 ms deadline. Dead hints cannot serialize all live-peer verification.
  All workers are joined before returning, including cancellation/error paths.
- The same signed announcement, capabilities, public-control and feature checks
  remain mandatory. Timeout of an individual verification is not a global
  storage failure. Actual persistence errors continue to propagate.
- Country discovery uses the existing total budget minus the reserved peer RPC
  budget, rather than prematurely stopping at two seconds. Total timeout stays
  five seconds; there is no timeout increase or new readiness state.
- Candidate selection and provider union retain verified partial results.
  A canceled parent request cannot start candidate acquisition. Existing
  selection shuffle, ACTIVE/RESERVE/COLD semantics, registration eligibility,
  wire formats and M11 quorum are unchanged.
- No static Rendezvous endpoints are introduced. All bootstrap entries remain
  persisted; working servers are still discovered through production DHT.

## Regression evidence

The dead-provider regression first failed with an empty result on sequential
verification. Partial candidate selection and partial provider union tests also
failed before their fixes. Focused tests now cover two dead hints preceding
live peers, deduplication, self exclusion, bounded concurrency, worker joining,
cancellation, actual persistence failure, partial acquisition and RPC budget
reservation.

Whole-tree vet and tests passed. Affected discovery/Rendezvous/daemon suites
passed race plus randomized execution, including repeated runs. No new soak
or complete privileged release acceptance campaign was performed.

## Mini-PC production checks

The actual Desktop QR decoded to three bootstrap entries. A four-entry bundle
was then constructed using the original New York PeerID recovered from the
existing Amsterdam service's public bootstrap argument. No hidden Rendezvous
or Exit endpoints were passed to the client.

| Case | Repeated country queries | CONNECT / user traffic |
| --- | --- | --- |
| Three live bootstrap entries | 6/6 CZ, DE, NL; READY | Not needed for this case |
| Four-entry bundle, New York deleted | 5/5 CZ, DE, NL; READY | Covered below |
| Same bundle; New York deleted and Helsinki temporarily unreachable from mini-PC | 5/5 CZ, DE, NL; READY | CONNECTED to NL; HTTPS egress Amsterdam |

The additional failure was injected by an isolated temporary nftables table
inside the mini-PC's physical network namespace. Its drop counter incremented
34 times, confirming the impairment was exercised. The script's exit trap
disconnected the client and removed the table. Servers were not stopped or
modified; the workstation did not run an IRVIO network session.

This tests partial bootstrap loss, not loss of all entry points. If no usable
bootstrap/Rendezvous/Exit path remains, readiness cannot be guaranteed and
the existing fail-closed behavior must remain intact.
