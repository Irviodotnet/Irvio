# Private IRVIO — actual Linux host DNS lifecycle

The private client runs in the mini-PC's PID1 network and mount namespaces with the real systemd-resolved service and unmodified resolvectl. Other production peers remain in isolated network namespaces. No test was run on the workstation, and no production distributor was modified.

## Test contract

`TestPrivateRuntimeActualHostDNS` additionally requires `IRVIO_PRIVATE_HOST_DNS=1` and the normal privileged-lab opt-in. It refuses an active installed daemon, existing runtime intent, hydra0 or prior IRVIO/lab firewall protection. It temporarily stops socket activation and starts a separate production desktop daemon with a fresh test profile and Unix API socket. Its runtime intent is in the real `/run/irvio`, not the usual isolated lab runtime directory.

A disposable veth connects this host to the lab. A metric-1 default route makes the lab the selected underlay; existing physical default routes are not removed or rewritten. Removal of the lab interface removes its routes. This test temporarily changes the mini-PC's host networking and must not run on a user's active workstation. SSH management remains in a separate namespace.

The product preset retains its normal DNS address `1.1.1.1`. Only the isolated lab router maps TCP/UDP port 53 for that address to the controlled DNS responder. This is a synthetic DNS server behind a real private tunnel, not a fake resolvectl or a claim about Cloudflare availability. Other lab nodes still use the existing isolated-DNS launcher.

After real routed enrollment, approval and pinned CONNECT:

- Actual `resolvectl dns hydra0` must show `1.1.1.1`.
- Actual `resolvectl domain hydra0` must show `~.`.
- Actual `resolvectl default-route hydra0` must show `yes`.
- An uncached system-resolver A query for the unique lab name must return `198.51.100.77` and identify hydra0.
- The common test verifies HTTP/UDP/DNS/ICMP traffic, pinned identity, private/public registration isolation, membership revocation, scoped WAN-leak counters and explicit full leave.

After DISCONNECT, the actual host's complete resolver DNS/domain/default-route output and resolv.conf contents, mode and symlink target must match the pre-Connect snapshot before lab cleanup. After deleting the lab, the original pre-lab DNS snapshot must also match. Socket activation is restored only after successful cleanup checks. Failure leaves evidence rather than indiscriminately deleting protection. TestMain independently compares host state with baseline.

## Execution evidence

Production lab daemon SHA-256: `2890864581e94fa55809615fc6323c3a6021bb84eb6f556f3b930e58a4b56ed6`, including the RELEASE empty-read fix. Runner SHA-256: `2b8125fe8e4b607dce7abcc38d8a29a06f7a63acbcb4fef5b3ce0bc2398f9c8f`. Compilation and netns package vet passed on the mini-PC.

Successful run `/tmp/hydra-m4b-ddc65c4c`: `2026-10-06T21:05:18.307736314Z`–`2026-10-06T21:05:38.760912204Z`, PASS 20.40s, exit_code=0, host_integrity=true. Unit `irvio-private-host-dns-ae898ce-r3.service`, invocation `37cebc5cd95744e6a0f5e50039576340`.

An independent repetition with fresh lab identities/profiles, `/tmp/hydra-m4b-c6386ba4`, also passed in 20.30s, exit_code=0, host_integrity=true, UTC `2026-10-06T21:06:28.451487172Z`–`2026-10-06T21:06:48.808901761Z`. Unit `irvio-private-host-dns-ae898ce-r4.service`, invocation `a728f15d84fe40df937b5ad7fbeb378e`. The installed daemon remained inactive and socket activation was restored.

Post-run manual checks confirmed the original physical default route and connected routes, DNS `192.168.88.1` on enp4s0, no nft tables, installed daemon inactive/MainPID=0, socket active, and only the management namespace remaining. An actual ordinary HTTPS request to example.com from PID1's namespaces with `--noproxy '*'` returned HTTP 200 after cleanup.

## Preserved unsuccessful setup attempts

1. `/tmp/hydra-m4b-ea4b6f6b`: the initial harness supplied `--tunnel-dns` alongside the product desktop role. Product validation correctly rejected that unsupported override before readiness. Removed that harness argument and retained the actual product DNS default. exit_code=1, host_integrity=true.
2. `/tmp/hydra-m4b-56b9de9a`: DHT/enrollment succeeded through explicit bootstrap routes, but transport initialization did not complete and the client reached NO_EXIT. The setup retained the physical default while only routing lab endpoints through veth. `PreferredBindPlanner` selects its source from the normalized default-route interface, whereas product bootstrap underlay resolution follows the destination route. Making the disposable lab path the preferred default enabled the unchanged product binary to complete. exit_code=1, host_integrity=true. This is an observed mixed-route limitation, not evidence that all multi-homed routing combinations work; it remains separate from the DNS lifecycle gate.

No reboot, packaged socket-FD adoption, universal network-manager compatibility or 100-cycle actual-DNS claim is made by this focused test.
