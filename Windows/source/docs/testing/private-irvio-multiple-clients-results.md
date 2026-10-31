# Private IRVIO — multiple-client acceptance scenarios

Status: prepared and queued; no network PASS is claimed yet.

Added `TestPrivateRuntimeMultipleClientsSelectiveRevocation` and `TestPrivateRuntimeMultipleClientsFullLeave`. Both use the existing real production lab plus a second independent desktop client in a ninth namespace. Each client imports a distinct signed invitation, performs routed TLS enrollment, requires explicit host approval, and connects to the exact same private host identity. The enrollment helper now selects the approval by authenticated client identity rather than assuming the owner has only one invitation.

Both clients must simultaneously be CONNECTED, produce actual HTTP forwarding through the private host's egress and appear as two downstream clients. Each has its own scoped physical-WAN counter. Selective revocation must finish within the existing bounded cleanup allowance, reduce the host's active count to one, block the revoked client's traffic, and preserve repeated successful traffic from the other client without changing either pinned target or falling back to a public country.

The full-leave variant skips selective revocation and disconnects the host while both downstream sessions remain active. Once the host reports DISCONNECTED, its active count must be zero and neither client may pass user traffic. Both variants explicitly disconnect all peers, require cleared runtime intent and exact namespace network baselines, and verify restored ordinary-WAN egress. No product timers or production code were changed.

Mini-PC compilation and `go vet ./test/netns/m4b` passed. Runner `build/private-netns/private-multiple.test` SHA-256: `3f858776f6fdd0a43fe0058c9da32742235bc15e5ab815257cc5930cb816e205`. Uses the extracted preview `01cfb88` daemon SHA-256 `99e0ebe41e39834810fed72d9dc26e629b421230259b307a46222774a93cd0a3`.

Queued selection `^TestPrivateRuntimeMultipleClients`, count=3, test timeout=8m. Mini-PC unit `irvio-private-multiple-3b0c84b.service`, invocation `38ea796e47054d44b5a300607aaf9105`, initial PID 285928, runs `/home/ubuntu/irvio-private-artifacts/01cfb88/multiple-after-lifecycle.sh`. At launch the prerequisite 100-cycle unit was still active/PID284295, so this queue process was waiting, not running another topology.

The script waits locally for `irvio-private-lifecycle-01cfb88-r1.service` to stop, then requires `/tmp/hydra-m4b-2410079c/results.json` to have exit_code=0 and host_integrity=true. Otherwise it exits without creating a lab. Do not reboot or start another privileged topology while either unit remains active. Final artifact ID, outcome and cleanup must be recorded after the actual execution; prepared assertions are not evidence of a successful network run.
# Fresh-snapshot correction (2026-10-06)

## Completed corrected campaign

`/tmp/hydra-m4b-bd70b0b4`: started `2026-10-06T21:33:32.464702495Z`, completed `21:38:35.969230532Z`, exit 0, `host_integrity=true`. All six cases passed with the unchanged packaged daemon. Selective revocation: 51.97 / 54.41 / 54.58 seconds. Full leave with both clients active: 47.66 / 47.63 / 47.20 seconds. Durations include setup, enrollment, traffic and teardown, not only revocation latency.

Both clients transferred real HTTP through the pinned host before assertions. Selective revocation denied the first client's traffic while preserving the second, then full leave denied both. All scoped WAN counters stayed zero; explicit disconnect restored ordinary WAN and saved network baselines on all three desktops. This confirms the initial immediate-count failures were a stale-snapshot assertion artifact for these repetitions. No product counter/lifecycle change was needed.

Initial six cases in `/tmp/hydra-m4b-f9c3705a` failed at the immediate `Clients == 2` assertion; terminal exit 1, host integrity true (21:30:15–21:32:55 UTC). The assertion treated `GetTrafficStats` as a synchronous lease read. In fact `internal/trafficstats/session.go` samples every two seconds and the Local API serves that cached snapshot. Therefore an old count immediately after CONNECTED is not evidence that one lease disappeared.

The corrected harness first verifies actual forwarding and exact pinned target on both clients, then waits at most five seconds for a snapshot generated after the check began and containing the required count. Full-leave zero remains immediately acceptable because a stopped sampler retains its final zero snapshot. This changes no product timeout or behavior. The initial failures remain preserved; successful corrected repetitions are still required to classify them conclusively.

Mini-PC compile and `go vet ./test/netns/m4b` passed. New runner SHA-256: `f21e7eca57f87a37b3ee05bcc3c5cbc58cdf8f70ac8bda81b7f7aa263e080bd5`. Unit `irvio-private-multiple-fresh.service`, invocation `2b0d75380ded4e2985871c871abd748c`, launched with PID 291723 and confirmed active. Both cases run three times using the unchanged packaged daemon `99e0ebe41e39834810fed72d9dc26e629b421230259b307a46222774a93cd0a3`. No terminal result is claimed at launch.
