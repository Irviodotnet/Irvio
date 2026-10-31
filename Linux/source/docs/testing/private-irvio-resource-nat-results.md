# Private IRVIO — current-preview resource and NAT results

Both autonomous campaigns have terminated successfully on the mini-PC. Production daemon SHA-256: `c43a377bec00017f7c3dde2ea3609a7996ba9dd3dab19ae6852cfe01ce234667`, from preview `90f8431`. These are bounded acceptance results, not completion of the entire private-mode plan. Real host DNS, fresh boot and other outstanding gates remain in the acceptance audit.

## 100 lifecycle cycles

Artifacts: `/tmp/hydra-m4b-3a70e777`. UTC start `2026-10-06T20:24:25.573811831Z`, completion `2026-10-06T20:43:04.10934477Z`. `results.json`: exit_code=0, host_integrity=true. Unit `irvio-private-lifecycle-15c65d8-r1.service`; runner SHA-256 `bafb687ed547f5e16ba031512ab20c00b73f60a8a98008637ca32940484c5604`.

`private-lifecycle-resources.json` contains 101 idle samples, including baseline. Every sample for each process has FD=9, pipes=2, sockets=2, AF_NETLINK=0. Host threads range 12–14; client threads remain 12. Each cycle verifies real HTTP egress through the pinned host, zero downstream clients while the host is still HOSTING, and exact normalized namespace network baseline after both peers disconnect.

| RSS KiB | Initial | Median | Maximum | Last | Median cycles 20–40 | Median cycles 80–100 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Host | 40476 | 117440 | 123912 | 87996 | 117028 | 117456 |
| Client | 41152 | 91896 | 119528 | 115740 | 91556 | 93660 |

The last window's median exceeds the earlier warmed-up window by approximately 0.37% / 2.30%. This run does not show the old monotonic FD/netlink/pipe leak signature. RSS alone is not proof of either a heap leak or its universal absence; preserve the child GC logs for further heap analysis. No real resolvectl operation is exercised by this isolated-DNS harness.

### RELEASE acknowledgment observation — still open

Five client warnings report `TUNNEL_RELEASE_UNCONFIRMED error_class=direct_control_unavailable`. Times in the logs are UTC+03:00: 23:24:41, 23:31:21, 23:32:08, 23:41:38 and 23:43:03. Host dataplane revoke events precede these warnings at 23:24:36.958, 23:31:16.025, 23:32:03.560, 23:41:33.922 and 23:42:53.617 respectively. The final event follows the explicit membership-revocation scenario; the others occur during normal disconnects.

The still-hosting lease-count oracle passed, so this evidence does not show retained downstream leases. It does show that client acknowledgment success cannot be claimed for every disconnect. Current source waits for client closure/deadline after RELEASE_ACCEPTED before revoking; the client preserves a received acknowledgment even if transport Close fails. The coarse warning does not identify the failing exchange stage. A remaining acknowledgment/transport race is a hypothesis, not a confirmed root cause; do not classify the four normal-disconnect warnings as expected merely because cleanup passed.

## Endpoint-dependent NAT repetitions

Artifacts: `/tmp/hydra-m4b-67ec1f7a`. UTC start `2026-10-06T20:43:08.746221681Z`, completion `2026-10-06T20:46:05.342270974Z`. `results.json`: exit_code=0, host_integrity=true. Unit `irvio-private-dependent-5b030bb.service` is inactive, MainPID=0, ExecMainStatus=0.

Runner SHA-256: `38fe80cfe430cf0eb618cf3d168068aaea8a3b30e521e85acd9957678923dc47`. Selection: `^TestPrivateRuntime(Host|Client)EndpointDependentNAT$`, count=3. Host durations: 37.20s, 37.78s, 37.17s. Client durations: 21.51s, 21.43s, 21.44s. All six passed.

The controlled NAT uses STUN destination-class mapping port 42000 and peer mapping port 43000 with conntrack-dependent inbound filtering. Oracles require the STUN endpoint in the offer, the distinct peer mapping in the selected remote endpoint, actual HTTP/UDP/DNS/ICMP forwarding, membership revocation, scoped WAN-leak checks and final baseline restoration. No hidden peer endpoint is injected. These results cover the defined host-side and client-side mapping models separately, not all commercial NATs or arbitrary simultaneous two-sided APDM.

No production code or deployed service was changed to obtain these results. GitHub and production distributors were not touched.

## Follow-up: confirmed RELEASE wait defect

Source inspection found that the server waited for closure with a single `secure.Read`. The pinned libp2p Noise implementation can return `(0, nil)` after draining its decrypted-frame buffer. That result is neither EOF nor a deadline, yet the server immediately revoked the lease. The acknowledgment could therefore lose its carrying route before reaching the client.

Added `TestReleaseKeepsRouteUntilNoiseClientClosesOrDeadline`, using real TCP and Noise. It sends RELEASE manually, receives RELEASE_ACCEPTED without closing the client, and requires the lease to survive the pre-deadline observation window. Separate cases then close the client or leave it open until the server deadline; both require bounded revocation. On the unchanged server both cases failed with premature revocation in 0.015s. Replacing the single read with `io.ReadFull` makes the wait tolerate empty reads without changing protocol or timeout values.

Mini-PC `go test -race -shuffle=on -count=10 ./internal/sessioncontrol` passed in 17.975s; package vet passed. Affected `go test -race -shuffle=on -count=3 ./internal/tunnellease ./internal/runtime ./internal/sessionestablish` passed in 1.046s / 28.577s / 1.304s. The lifecycle harness now explicitly rejects any unconfirmed RELEASE before its intentional revocation phase. Real-dataplane repetition results are pending at this entry; component reproduction proves the premature-revoke defect but does not alone prove the cause of every historical warning.

The fixed test binary SHA-256 is `2890864581e94fa55809615fc6323c3a6021bb84eb6f556f3b930e58a4b56ed6`. It is a separate mini-PC lab build, not the previously packaged preview. Production deployments and GitHub remain unchanged.

### Fixed real-dataplane verification

`TestPrivateRuntimeDisconnectRelease`, count=3, passed in 65.71s / 65.25s / 65.73s. Each repetition contains three reconnect/disconnect cycles in addition to its initial connection and final intentional membership revocation. The new assertion found no unconfirmed RELEASE before intentional revocation, and the still-hosting remote lease-count oracle passed throughout. Each repetition has exactly one remaining warning, after intentional membership revocation has already removed the carrying route; these are distinct from the prior normal-disconnect failures.

Artifacts `/tmp/hydra-m4b-44558366`, UTC `2026-10-06T20:55:28.304355065Z`–`2026-10-06T20:58:45.041506365Z`; exit_code=0, host_integrity=true. Runner SHA-256 `6745af1b7e28706f0021e71ee1e5990202c744773760658e79f37c994e181db6`. Unit `irvio-private-release-wait-538a159.service`, invocation `af43b08a50734d5e8d42d8e2a302f704`, ended inactive/MainPID=0/ExecMainStatus=0. Only the management namespace remains; installed daemon remains inactive.

The route wait remains bounded by the original server timeout even if client closure does not arrive before local tunnel teardown. No production timers were increased. This short regression does not replace the 100-cycle final-binary resource gate: the earlier 100-cycle record belongs to the old preview and a final build needs its own rerun/package hash.
