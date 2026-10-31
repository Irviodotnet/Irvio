# Data-plane promotion lifecycle fix

## Scope and baseline

This fix addresses the production defects exposed by `BUG-DP-PROMOTION-LOOP`:

- owner `NativeTun` monitor resources could outlive a closed TUN;
- a local post-connect activation failure was treated as a peer/path failure;
- descriptor refresh churn changed connectivity identity for an unchanged path;
- `resolvectl` stderr and the promotion error were discarded.

It does not add support for running `hydrad` in an arbitrary network namespace
while sharing host D-Bus and host `systemd-resolved`. The Linux alpha contract
remains an ordinary host service in the host network namespace.

The production baseline was `284e7b058118857a35561b408cfccb19ab68b233`.
The historical focused reproducer and immutable evidence remain under
`pre_alpha_final_test/bug_dp_promotion_loop/`; this fix does not alter them.

## Root cause

The first original Phase-2 C04 occurrence completed candidate acquisition,
authenticated ICE, AWG/data-plane staging, TunnelLease activation, and session
control BIND. DNS promotion then failed at `20:26:11.646`; the selector retried
the same S4 path at `20:27:04`. C05 failed at `20:26:12.756` and first retried
at `20:27:04`. The exact local command was:

```text
/usr/bin/resolvectl dns hydra0 198.18.250.20
  -> org.freedesktop.resolve1.Manager.SetLinkDNSEx(link=4, ...)
  -> org.freedesktop.resolve1.NoSuchLink: "Link 4 not known"
  -> resolvectl exit status 1
```

There was no causal kernel syscall errno. The D-Bus `sendmsg` and `recvmsg`
succeeded and `systemd-resolved` returned a semantic D-Bus error. The focused
netns topology supplied a namespace-local ifindex to host `systemd-resolved`;
that trigger is out of product scope, but it exposed the production bugs below.

The healthy C09 control used the same S4 peer and endpoint, promoted at
`05:06:41`, kept one lease and session generation, and did not enter
reselection. The deterministic historical reproduction produced 18 promotion
failures on C04, 17 on C05, and zero on C09.

## Resource lifecycle

Each monitored AmneziaWG `NativeTun` owns its TUN descriptor, one
`AF_NETLINK/NETLINK_ROUTE` socket, and one `RWCancel`. `RWCancel` owns the read
and write ends of an `os.Pipe`.

There are two independent `NativeTun` objects for an installed client path:

- `internal/tunnel.Linux` owns the long-lived owner object and its TUN,
  AF_NETLINK socket, RWCancel reader, and RWCancel writer;
- `Linux.Borrow` duplicates the TUN descriptor and creates a generation-local
  borrowed object. The AWG transport owns and closes it, while AWG consumes its
  `Events()` channel.

The pinned dependency closes AF_NETLINK and both RWCancel pipe ends in deferred
cleanup in `routineNetlinkListener`. The owner object's five-entry event channel
had no consumer, so a listener blocked while publishing an event could never
observe cancellation or reach that cleanup. The borrowed object did not leak
because AWG consumed its events.

`internal/tunnel.Linux` now starts a dedicated drain for the owner event stream.
`Close` remains idempotent, closes the active borrower before the owner, and
waits at most five seconds for the owner monitor to finish. This preserves the
long-lived `hydra0` and generation-borrowing design.

Historical pre-fix counts were:

| Node | Promotion failures | AF_NETLINK | RWCancel pairs | TUN |
| --- | ---: | ---: | ---: | ---: |
| C04 | 18 | 23 | 20 | 0 |
| C05 | 17 | 22 | 19 | 0 |
| C09 | 0 | 5 | 2 | 2 |

The real-kernel post-fix regression completed 500 owner create/close cycles:

```text
total FD       6 -> 6
AF_NETLINK     0 -> 0
pipe endpoints 3 -> 3
TUN FD         0 -> 0
```

The pipe count includes the test process's unrelated pipes; equality proves no
RWCancel pair remains per closed owner. The controlled 100-failure promotion
stress also measured `FD 6 -> 6`, `goroutines 2 -> 2`, and `100/100` lease
releases.

## Failure classification

Local route, TUN, AWG staging/commit, and DNS activation errors are explicitly
wrapped as `OutcomeLocalPromotionFailed`. Plain, unclassified session-control
or remote/path failures retain their existing classification.

On a local promotion failure the selector now:

1. closes the staged connection, AWG generation, borrowed TUN, and lease
   renewer/release path;
2. publishes `EventLocalPromotionFailed` and enters
   `StateLocalPromotionFailed` instead of `NO_EXIT`;
3. keeps ProtectedMode fail-closed;
4. does not add peer/path suppression and does not consume reserve/COLD peers;
5. schedules a bounded 30-second local retry instead of immediately repeating
   ICE, AWG, and lease acquisition.

An unchanged-underlay update and `RefreshNow` cannot bypass the local backoff.
When the timer expires, the same healthy peer may be selected again. A real
client `NetworkGeneration` increase cancels the stale opportunity, clears path
suppression, and permits immediate reevaluation without restarting the daemon.

The Local API maps this condition to `ERROR`, not `NO_EXIT`. The failure remains
fail-closed: DNS activation is not ignored and the session is never reported as
connected with partially promoted local state.

## Network epoch

The old brokered epoch hashed descriptor ID, incarnation, and sequence. Those
fields are still used for registration ordering and fencing, but they no longer
define connectivity identity.

`protocol.NetworkPathEpoch` now hashes the authenticated remote process session
epoch plus a sorted, deduplicated set of advertised locators. Therefore:

- descriptor ID/sequence refresh with the same process and locators preserves
  the path epoch and suppression;
- a changed remote endpoint changes the path epoch;
- a changed client `NetworkGeneration` explicitly clears old suppression and
  creates a new connectivity opportunity.

Direct and brokered candidate paths use the same contract. Unit and selector
regressions cover descriptor refresh, endpoint change, and underlay generation
change.

## Telemetry

The DNS command runner now discards stdout, captures at most 4 KiB of stderr,
normalizes invalid UTF-8 and control characters, flattens whitespace, and marks
truncation. Errors identify the `resolvectl` operation, interface, exit error,
and sanitized stderr.

Before conversion to selector state, runtime emits
`CLIENT_DATA_PLANE_PROMOTION_FAILED` with component, operation, failure domain,
outcome, PeerID, session generation, transport/network generation, selected
endpoint, and the wrapped reason. It does not log keys, ICE credentials, auth
tokens, or command stdout.

## Security

All controlled local-promotion tests remain fail-closed. There is no false
ACTIVE state, stale client lease, or direct-WAN fallback. Existing M8
ProtectedMode, M11 public reachability/no-multihop, and M12 dual-role/failover
network suites pass. The fix does not weaken the hydra0-to-hydra0 prohibition.

## Regression results

The following gates passed on the fix branch:

- formatting, `git diff --check`, and `go vet ./...`;
- `go test -count=1 ./...` and `go test -shuffle=on -count=1 ./...`;
- `go test -race -count=1 ./...` and race plus shuffle;
- saturated owner-event close, 500 fake cycles, 500 real-kernel cycles, and an
  explicit Close-versus-Borrow race repeated 50,000 times under the race detector;
- 100 repeated local promotion failures and full lease cleanup;
- local failure recovery without peer poisoning or an immediate retry storm;
- direct and brokered path-epoch regressions;
- bounded/sanitized DNS telemetry and normal host-namespace DNS promotion;
- M6, M7, M8, M9, M10, M11, M12, and P1;
- addressless-provider and COLD-route failover regressions.

One initial full M11 run hit a static-PAT verifier wait timeout after the subject
had already logged five successful public-reachability verifications. The exact
case and the complete M11 suite both passed immediately on clean rerun; this was
classified as transient inter-scenario lab timing, not a product regression.

## Old suspect rechecks

| Suspect | Result | Classification |
| --- | --- | --- |
| restart post-answer/pre-connect | PASS 20/20 | No longer reproduces on clean processes; downstream effect of the promotion/lifecycle defect |
| community Exit during S1 loss | PASS 20/20 | No longer reproduces on clean processes; downstream effect of the promotion/lifecycle defect |
| NAT-remap recovery | STILL REPRODUCES 20/20 | Independent pre-promotion bug; separate follow-up required |

The NAT-remap failure remains outside this fix and is described in
`docs/testing/nat-remap-recovery-follow-up.md`.
