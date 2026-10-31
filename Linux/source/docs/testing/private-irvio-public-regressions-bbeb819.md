# Private IRVIO — public compatibility after the replay fix

Tests execute only on the mini-PC. Canonical source at `5026724` has the same production implementation as `bbeb819`; subsequent changes are tests and documentation. This report is not final private-mode acceptance.

## Component regressions

`go test -race -shuffle=on -count=5 -timeout=180s` passed for:

| Package | Duration |
| --- | ---: |
| `internal/transportgen` | 2.012s |
| `internal/exitselector` | 2.225s |
| `internal/runtime` | 47.901s |
| `internal/productbootstrap` | 1.736s |
| `internal/discovery` | 56.545s |

Coverage includes repeated local promotion failure releasing leases, bounded selector retries, COLD eligibility after correlated loss, per-Prepare SRFLX refresh, late STUN/data-plane fencing, bootstrap bundle round-trip/deduplication, and addressless Rendezvous becoming dialable. These are component checks, not a repeat of the 50-client bootstrap campaign.

## Privileged public selection

The runner uses extracted preview daemon SHA-256 `a6ebc8837e286ec101ca9f74e631940cfe36884649bb17c11d4464b1054b5fd0`. Runner SHA-256: `5def51de2f15dcc13720200b76a95ce1c4b50215cfcbaccbb602d42158b047f7`. Lab wrapper SHA-256: `95b48eb7509f8fe0b27254c8b3e35aca0a175b5de8da275179960026ce3d7d57`; it restores the cgroup2 mount inside an isolated mount namespace before exec, without replacing DNS commands.

Selection: eight `TestM8_*` cases; M7 Safety Policy and lifecycle/crash adoption; M9 dead RESERVE/COLD/fresh batch; M10 full production lifecycle and automatic failover; M11 dual-role/anti-multihop; M12 client NAT remap, Exit NAT remap and Exit failover; P1 API control and core failover observation.

Not every case runs two production daemons: the existing M7/M8 fixtures also use dedicated signaling/traffic/killswitch helpers. Do not describe helper-only cases as a new full production deployment.

All 19 selected cases passed. `/tmp/hydra-m4b-242fe692/results.json` records start `2026-10-06T22:29:32.090421015Z`, completion `22:35:51.683658623Z`, exit 0 and host integrity true. Post-run inspection found only the pre-existing `hydra-mgmt` namespace, no runner/extracted daemon processes and no hydrad cgroups.

Client NAT-remap recovery took 69.830s to CONNECTED and 69.857s to the first successful user probe; FD baseline 29, final 28. Exit NAT-remap case passed in 72.66s total. These are bounded recovery observations, not a low-latency claim. M9 dead RESERVE/COLD/fresh-batch case passed in 7.36s, with four provider requests.

## NativeTun stress: unresolved intermittent close error

Follow-up diagnosis and the narrowly scoped correction are recorded in `private-irvio-native-tun-close-results.md`. The results below describe the original, uncorrected binary.

A separate freshly compiled `internal/tunnel` runner (SHA-256 `8e6973ad086ddbb66739c7593507e40a449246e5053488e09c19c514d9907650`) executed `TestLinuxNativeTunLifecycleResources` as root inside a new unnamed network namespace, with `HYDRA_TUNNEL_PRIVILEGED=1`.

The first execution failed after 0.44s at `linux_privileged_test.go:46`: `write |1: file already closed`. Three subsequent repetitions passed (6.61s / 6.62s / 6.64s); each completed 500 cycles with baseline and final `{total:6 netlink:0 pipes:0 tun:0}`. The first failure remains a failure, not replaced by the retries.

Initial source inspection: IRVIO `Linux.Close` propagates the underlying device close error. The pinned AWG `NativeTun.Close` closes `statusListenersShutdown` before calling `netlinkCancel.Cancel`; `RWCancel.Cancel` writes to a pipe whose handles can be closed by `RWCancel.Close`. This is a candidate shutdown race, not yet a demonstrated causal trace. No leak was demonstrated by the successful repetitions. Do not mask arbitrary close errors or claim this gate passed until the intermittent failure is resolved.
