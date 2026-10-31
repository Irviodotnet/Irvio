# Private IRVIO — final packaged 100-cycle result

Mini-PC only; production source and hashes are those in `private-irvio-preview-f5f816b.md`. The daemon was not rebuilt or modified during the test. Test-only namespace launcher correction: `f66bfeb`.

Artifact `/tmp/hydra-m4b-d753e940`, unit `irvio-private-lifecycle-f5f816b-r2.service`, invocation `c0f26e3aa1634c21b7a7d4dc87094800`. Terminal result: start `2026-10-06T23:17:21.432806212Z`, completion `2026-10-06T23:35:43.681172713Z`, exit 0, host integrity true. `TestPrivateRuntimeLifecycle100` passed in 1102.20s; the enclosing DNS-audit wrapper also exited 0.

## Resource result

All 101 samples (baseline plus 100 full cycles) were inspected, not only ten-cycle console summaries. Both daemons remain alive across cycles. Each cycle establishes pinned private HTTP forwarding, disconnects client and host, waits for remote RELEASE, checks inactive traffic/leases, and compares namespace links, addresses, IPv4/IPv6 routes, policy rules and nft state with baseline.

| Resource | Host | Client |
| --- | --- | --- |
| FD, every sample | 9 | 9 |
| Pipe FDs, every sample | 2 | 2 |
| Socket FDs, every sample | 2 | 2 |
| AF_NETLINK socket FDs, every sample | 0 | 0 |
| Threads, first / median / max / last | 12 / 13 / 14 / 14 | 12 / 13 / 13 / 13 |
| RSS KiB, first / median / max / last | 39772 / 118560 / 123052 / 120088 | 40540 / 92676 / 120040 / 95068 |
| RSS KiB, cycles 1–25 median | 104596 | 91760 |
| RSS KiB, cycles 76–100 median | 119632 | 101736 |

RSS warms up and fluctuates; final client RSS falls below its late-window median and maximum. These observations support bounded resources in this campaign, not a proof against every possible heap leak. No generation-style FD/netlink/pipe/socket accumulation occurred.

No unconfirmed RELEASE occurred during the 100 normal cycles: the test explicitly scans for it before its final reconnection. One later `TUNNEL_RELEASE_UNCONFIRMED` at 23:35:43Z followed the deliberate final membership revocation and dataplane removal at 23:35:33Z. It is not a normal-disconnect failure. Final HTTP/UDP/DNS/ICMP traffic, revocation denial, scoped user-WAN leak counter and full leave assertions passed.

## Host integrity

The wrapper captured real PID1-namespace `resolvectl dns`, `domain`, and `default-route` before/after in root-only `/var/tmp/irvio-private-dns-audit.Bpc0wj`. All three compare byte-for-byte. This supplements the generic netns runner's integrity check, which missed DNS damage in the preserved earlier failed attempt `855a8439`.

After completion only pre-existing `hydra-mgmt` remained. No extracted daemon/test process matched the lab paths. Actual-host nft table inventory was empty; installed `irviod.service` was inactive, MainPID=0, NRestarts=0. Ordinary HTTPS to example.com with proxies disabled returned 200. No reboot or concurrent privileged lab occurred during this run.

This lab substitutes DNS commands inside isolated children; it does not replace the separately completed actual-host private DNS lifecycle tests. Failure evidence from the previous helper remains preserved and classified as a lab isolation issue. The corrected helper's small read-only stress tests did not independently reproduce the precise original scheduler interleaving.

Result: PASS for the final packaged resource/lifecycle gate, with the stated RSS, DNS-fixture and scoped-oracle boundaries.
