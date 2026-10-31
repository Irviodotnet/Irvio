# Follow-up: brokered NAT-remap recovery

## Status

**Closed on `fix/nat-remap-recovery`.** The original independent failure and
its validation remain described below. The root cause, minimal fix, and
post-fix evidence are recorded in
[`nat-remap-recovery-fix.md`](nat-remap-recovery-fix.md).

The focused recheck reproduced the failure in 20 of 20 clean iterations. A
positive-control reconnect test with an unchanged external mapping passed 20
of 20 iterations.

## Deterministic scenario

1. Establish a brokered production connection from an EIM/APDF client through
   real authenticated ICE, AWG, TunnelLease, and the user-data path.
2. Keep the client process, identity, local socket, and local underlay
   generation unchanged.
3. Change the client's effective external UDP mapping from port 51011 to 53011
   by replacing the lab NAT rule and moving new conntrack state to a clean zone.
4. Wait for the existing path to fail and the runtime to enter recovery.
5. Observe fresh brokered ICE attempts for at least 90 seconds.

The workstation did not have the `conntrack` CLI, so the focused lab used an
nftables NAT-rule replacement plus a new conntrack zone. This changes the real
external mapping without modifying HYDRA production code.

## Actual result

The original connection and user-data path were healthy. After remap, the old
lease was revoked and runtime entered `RECOVERING`. Subsequent brokered ICE
attempts continued to advertise/use the stale client server-reflexive address
`198.18.90.10:51011` instead of the new external mapping on port 53011. Each
attempt ended with bounded `context deadline exceeded` or caller cancellation.
No attempt reached data-plane promotion, so this failure is independent of the
fixed DNS promotion classification and NativeTun owner leak.

Representative temporary artifacts from the recheck are:

```text
/tmp/hydra-m4b-8725169c  # 20/20 failing focused iterations
/tmp/hydra-m4b-d783ea27  # first representative failure
/tmp/hydra-m4b-a31a8f48  # 20/20 unchanged-mapping positive control
```

## Expected result

After a real external mapping change, recovery must refresh the client's
server-reflexive candidate and brokered signaling state, attempt ICE with the
new mapping, and reconnect without a daemon restart. ProtectedMode must remain
fail-closed while recovery is in progress.

## Likely investigation boundary

Start before data-plane promotion, at the ownership and invalidation boundary
between:

- network/NAT generation detection;
- STUN-derived local candidate refresh;
- brokered offer/answer candidate snapshots;
- ICE-session recreation on a reused UDP socket;
- publication of refreshed reachable endpoints.

Do not attribute this to DNS activation, TunnelLease promotion, or owner
NativeTun cleanup unless new evidence contradicts the focused trace.

## Acceptance criteria for the separate task

- The same deterministic remap scenario reconnects at least 20/20 times.
- The post-remap ICE session contains a refreshed server-reflexive candidate.
- Stale candidates cannot survive a real mapping-generation change.
- The original identity and daemon process remain in use.
- No direct-WAN user leak, false ACTIVE state, stale lease, FD growth, or
  unbounded retry loop occurs.
- The unchanged-mapping reconnect control continues to pass.
