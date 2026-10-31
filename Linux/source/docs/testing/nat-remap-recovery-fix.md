# NAT-remap recovery fix

## Scope and baseline

This change closes the independently reproducible pre-promotion NAT-remap
failure described in `nat-remap-recovery-follow-up.md`.

- baseline branch: `fix/dp-promotion-lifecycle`
- baseline HEAD: `d7e899fc5c92d775216a876c6af6bee4741e88e8`
- baseline production binary SHA-256:
  `baa3652061bfa465c3709113f3944ff4b7a6f4e47781564b89a962f46585763a`
- focused red-test commit: `4ae4b0e`
- production fix commit: `e1c9e82`
- validated production/netns implementation HEAD:
  `1b718522c31b70550bebbbb9746eee2d419c3a9b`
- implementation hydrad SHA-256:
  `bdc5962ece2dd3291a182503162a54cd5069411d7636a8136c52730c301b86c0`
- implementation trimpath hydrad SHA-256:
  `8027785359562073a7c3214ebd3b35b5963c6f577f17087da097e5d02825dcf5`

The fix does not add TURN, relay, port prediction, periodic STUN polling, a
new generation type, or another physical UDP listener. `HydraSocketMux`
remains the sole owner of one UDP4 and one IPv6-only socket.

## Reproduction and independent oracle

The historical production reproducer failed 20/20 times. In the first five
consecutive cases the initial STUN oracle observed the client mapping
`198.18.90.10:51011`; the lab then installed and independently observed the
new `:53011` NAT mapping, but each recovery continued signaling `:51011`.
The first representative artifact is:

```text
/tmp/hydra-m4b-8725169c/001-m12-brokered-nat-prflx
```

The baseline packet/STUN evidence answers the principal forensic question:
**Case B occurred. No new STUN Binding Request was sent after the remap.** The
STUN server recorded only the initial client request from `:51011` (and the
Exit's independent request). Nevertheless, new Pion sessions were prepared
and their offers repeatedly contained the old SRFLX candidate.

The focused unit regression made the same ownership error deterministic. A
mutable STUN server returned `:51011` while the `TransportGeneration` was
built, changed to `:53011`, and then a new prepared ICE session still returned
`:51011`. Only one Binding request had occurred. This test failed on the
baseline before the production change.

## Exact stale-state owner

The stale endpoint was owned by the immutable configuration snapshot at:

```text
transportgen.Builder initial probe
  -> iceengine.Config.Session.SrflxObservations
  -> iceengine.Engine.config.Session.SrflxObservations
  -> each icesession.PrepareWithSharedAdapter call
  -> detached SRFLX candidate appended to the new Pion Agent
```

`transportgen.Builder.Build` probed STUN once per `TransportGeneration`.
`iceengine.New` copied those observations into the long-lived Engine config.
Every recovery did create a new Pion Agent and a new `SessionGeneration`, but
Pion gathered only the HOST candidate from the shared adapter; HYDRA then
re-appended the copied SRFLX observation. No code asked STUN for a current
mapping during session preparation.

A NAT mapping can change without a local address, route, or netlink event, so
the client correctly remained at `NetworkGeneration=1`,
`TransportGeneration=1`, and mux port `41000`. Rebuilding either generation
was therefore not triggered. That was the condition exposing the bug, not a
reason to redefine `NetworkGeneration`.

The investigated hypotheses resolve as follows:

- H1, H2, and H5: confirmed. The Engine configuration cached SRFLX at
  transport lifetime, no new Binding happened, and transport generation was
  unchanged.
- H3: excluded. There was no fresh `:53011` response to lose after STUN.
- H4: excluded as root cause. Signaling faithfully serialized the already
  stale local description.
- H6: excluded. The stale value came from the new Agent's newly built local
  description, not a selected-endpoint or fast-connect hint.
- H7: excluded as an upstream Pion defect. Pion Agent instances were new;
  HYDRA supplied the stale detached candidate to each instance.

## Baseline causal timeline

Representative first cycle, local time:

| Time | Event |
| --- | --- |
| 07:42:48.113 | Client starts at `NetworkGeneration=1`; one initial STUN Binding observes `198.18.90.10:51011`. |
| 07:42:48.114 | `TransportGeneration=1`, mux port `41000`, is created. |
| 07:42:48.121 | Initial Pion session is prepared with HOST `10.51.1.2:41000` and cached SRFLX `198.18.90.10:51011`. |
| 07:42:49.128 | Exit selects client endpoint `:51011`; authenticated ICE succeeds. |
| 07:42:49.129 | TunnelLease/Data Plane activates with the same endpoint. |
| before 07:42:55.330 | Lab replaces the NAT rule with `:53011` and destroys/moves conntrack state. |
| 07:42:55.330 | Old path failure is observed; runtime enters recovery while network/transport generations remain `1/1`. |
| 07:42:55.331 | A new Pion session is prepared, but no STUN request occurs and its offer again contains `:51011`. |
| 07:42:57.0 | ICE fails with `context deadline exceeded` / caller cancellation. |
| 07:43:58.117 | A later new Pion session again signals `:51011`, still without a new Binding request. |

The active session generation was `1`. Failed staged attempts never promoted,
so the public runtime snapshot returned session generation `0` after teardown;
the distinct Pion session instances are proven by repeated
`ICE_SESSION_PREPARED` events. This failure happened before AWG/Data Plane
promotion on recovery and is independent of DNS promotion and NativeTun
lifecycle fixes.

## Minimal fix

`transportgen` now owns an external-dispatch STUN client for the lifetime of
the generation. Immediately before every `iceengine.Engine.Prepare`, it runs a
fresh Binding transaction through the existing `HydraSocketMux` Control
endpoint. The resulting observations are copied into a per-attempt session
configuration and only then used to create that Pion Agent.

The ICE adapter remains the sole reader of the logical Control endpoint. It
first offers incoming Binding responses to the active STUN transaction
registry and otherwise follows the existing ICE dispatch path. Closing the
generation closes Engine sessions, the refresh client, phase manager, and mux
in ownership order.

The invalidation scope is deliberately one ICE attempt:

```text
new recovery attempt
  -> fresh STUN Binding on existing mux/socket
  -> fresh per-attempt SRFLX snapshot
  -> new Pion Agent/local description
```

It does not force a `NetworkGeneration` or `TransportGeneration` change and
does not disturb the long-lived TUN. A bounded, non-secret
`ICE_SRFLX_REFRESHED` log records transport generation, ICE-session instance,
address family, local endpoint, and mapped endpoint.

## Late callback fencing

STUN responses are correlated by transaction ID in the existing registry.
Canceling an attempt removes its transaction. A controlled regression delays
the old `:51011` response, completes a newer attempt with `:53011`, and then
delivers the old response. The late response cannot complete a transaction or
mutate the new local description. This passed 100/100 repetitions, including
race-detector coverage.

## Post-fix production timeline

One representative same-process remap shows:

```text
07:43:32  ICE session 1: Binding -> SRFLX :51011 -> CONNECTED
07:43:39  mapping changed; ICE session 2: fresh Binding -> SRFLX :53011
07:44:42  recovery session 3 signals :53011
07:44:43  Exit selects remote endpoint :53011
07:44:43  new TunnelLease/Data Plane ACTIVE -> CONNECTED
```

The runtime kept `NetworkGeneration=1`, `TransportGeneration=1`, and mux port
`41000`. Only the session generation increased. For each subsequent mapping
`:53012` through `:53020`, the STUN oracle, production refresh telemetry,
brokered offer, Exit-selected endpoint, and TunnelLease activation all agreed
on the current mapping.

Exit-side remap also passed. Its APDM mapping intentionally produced SRFLX
`:54011` for STUN and PRFLX `:54012` for the peer-specific ICE path. The client
selected `:54012`, and AWG received that selected PRFLX endpoint. This is the
expected distinction between SRFLX and PRFLX, not stale state.

## Repetition, traffic, and recovery time

Five independent production-process runs each completed ten consecutive
client-side remaps: **50/50 recovered**. The deterministic sequence was
`:53011` through `:53020` in each process. A separate enhanced 10-cycle run
captured these remap-to-`CONNECTED` durations:

```text
69.826s 24.887s 69.874s 69.894s 14.981s
34.770s 69.901s 14.827s 34.937s 69.867s
```

- minimum: `14.827s`
- median: `52.382s`
- maximum (and observed p95): `69.901s`
- first successful HTTP probe followed `CONNECTED` by approximately
  `14–25ms`

The spread reflects existing failure detection/retry timing; no timeout was
changed. Recovery succeeded both while otherwise idle and while continuous M8
ordinary-traffic probes crossed each fault. Across ten faults, 640/640 direct
physical-interface attempts were blocked and the simulated Internet observed
zero new direct packets. HTTP through hydra0/AWG/TunnelLease passed after every
recovery.

The full post-fix production tests covered:

- client-side remap;
- ten remaps without process/profile restart;
- Exit-side remap with valid PRFLX selection;
- EIM/EIF to APDM/APDF NAT-to-NAT operation (two NAT domains);
- idle/retry detection and ordinary traffic during recovery;
- unchanged socket, TUN, network generation, and transport generation;
- full brokered signaling, authenticated ICE, AWG, lease, and Internet path.

## Resource and security proof

Across the five 10-cycle production runs the client FD counts were:

```text
29 -> 28
29 -> 26
29 -> 27
29 -> 29
29 -> 29
```

The enhanced representative run was `29 -> 29`. Every cycle asserted exactly
one physical UDP4 socket. The 100-session in-process stress observed 101 STUN
Binding transactions (one build probe plus one per session),
`SessionsCreated=100`, `SessionsClosed=100`, and `ActiveSessions=0`. The refresh
client uses no private dispatcher goroutine, while all tree and focused race
tests passed.

The previously fixed real-kernel owner NativeTun regression was repeated for
500 cycles and remained exactly bounded:

```text
total FD       6 -> 6
AF_NETLINK     0 -> 0
pipe endpoints 3 -> 3
TUN FD         0 -> 0
```

For NAT-remap tests:

- physical-WAN user leak: `0`;
- false ACTIVE: `0`;
- selected stale endpoint after successful recovery: `0`;
- stale active TunnelLease accumulation: `0`;
- extra UDP listener: `0`;
- anti-multihop: `0` in the full M11 dual-role regression.

ProtectedMode remained fail-closed during recovery. Application-byte counters
on `ice.Conn` are **NOT OBSERVABLE** in the existing production telemetry; the
tested production construction still uses ICE to select the AWG endpoint and
all application probes traversed the AWG/TunnelLease path.

## Regression results

All of the following passed on the implementation HEAD:

- `gofmt` and `git diff --check`;
- `go vet ./...`;
- `go test ./...`;
- `go test -shuffle=on -count=10 ./...`;
- `go test -race ./...`;
- `go test -race -shuffle=on -count=5 ./...`;
- normal and `-trimpath` hydrad builds;
- M5a, M5b, M5c1, M5c2, M5c3, M6, M7, M8, M9, M10, M11, M12, and P1 unit and applicable privileged netns suites;
- M5c2 PRFLX and expected bounded APDM/APDF-to-APDM/APDF failure;
- M5c3 same-socket migration/reconnect and real netlink changes;
- M6 AWG endpoint and IPv4/IPv6 controls;
- M7 routing, encrypted Internet, IPv6, migration, and client isolation;
- M8 fail-closed, reconnect, migration, and crash cases;
- M9 same-peer/RESERVE/COLD/fresh and underlay recovery;
- M10 generation/runtime lifecycle;
- M11 public reachability, IPv6 migration, and dual-role no-multihop;
- M12 brokered/direct session control, failover, NAT/PRFLX, client remap,
  repeated remap, Exit remap, and runtime lifecycle;
- addressless-provider resolution, 10 repetitions;
- COLD-route failover focused set, 10 repetitions;
- DP promotion/local-failure/resource regressions;
- NativeTun real-kernel lifecycle, 500 cycles.

Relevant post-fix artifacts include:

```text
/tmp/hydra-m4b-fccfa53e  # five independent 10-remap runs, 50/50
/tmp/hydra-m4b-0ff56332  # enhanced 10-remap leak/timing proof
/tmp/hydra-m4b-15459e71  # enhanced Exit-side remap proof
/tmp/hydra-m4b-c67493e1  # complete M12 suite
/tmp/hydra-m4b-8bc8d763  # P1 privileged suite
```

## Result

Before:

```text
:51011 -> NAT remap :53011 -> no new Binding -> cached :51011
-> brokered ICE timeout -> recovery fails
```

After:

```text
:51011 -> NAT remap :53011 -> fresh Binding through the same HydraSocketMux
-> per-session SRFLX :53011 -> signaling/ICE select :53011
-> AWG + TunnelLease -> CONNECTED
```

The defect is closed by a narrow endpoint-lifetime correction. No Core
topology, generation contract, protocol, timeout, or NAT capability was
expanded.
