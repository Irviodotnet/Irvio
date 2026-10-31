# M4b Linux network-namespace laboratory

M4b validates the completed M0–M4a libraries against isolated Linux network
stacks. It is deliberately test-only. None of the namespace builders, nftables
policies, responder code, or helper commands is linked into `hydrad`.

## Privilege and ownership

All Go compilation runs as the invoking non-root user. `make test-m4b-build`
creates four helper executables, focused STUN and socketmux regression test
binaries, and the privileged runner in `/tmp`. The runner checks that every
binary, including itself, is owned by a non-root UID before creating a lab.
Only those already built artifacts are invoked by the runner passed to
`sudo -n`; Go tooling never runs as root.

Each run gets an eight-hex-digit ID. Namespace names begin with
`hm4b-<runid>-`; transient veth names begin with `hy` and remain below Linux's
15-character interface-name limit. A lab registers each namespace, process, and
host-visible veth immediately. Cleanup stops process groups, deletes namespaces
in reverse order, and removes only exact registered links. Setup failures use
the same idempotent path.

The runner captures normalized host state before any lab and after every lab is
gone:

- namespace names;
- link names and configuration, excluding volatile statistics;
- IPv4 and IPv6 addresses and routes;
- nftables structure, excluding handles and counters;
- host IPv4 and IPv6 forwarding sysctls.

Any difference fails the runner. Forwarding and nftables changes occur only in
disposable router namespaces. The host's default routes, physical interfaces,
DNS, firewall, forwarding settings, and network manager are never touched.

## Topologies

Direct dual-stack and discovery:

```text
 A / client ns ---- veth or namespace-local bridge ---- C / server ns
                         |
                         +---- B ns (discovery topology)
```

The RFC 5780 server owns four real sockets per family: `A1:P1`, `A1:P2`,
`A2:P1`, and `A2:P2`. Direct tests assign both IPv4 addresses and both IPv6
addresses to the server namespace; the second IPv6 address is not simulated.

Single NAT44:

```text
 client ns -- 10.241.10.0/24 -- router ns -- 198.19.10.0/24 -- server ns
                                  |
                           nftables + conntrack
```

Double NAT44:

```text
 client -- 10.242/24 -- router 1 -- 100.64/24 -- router 2 -- 198.20/24 -- server
                            NAT                      NAT
```

Routed IPv6 filtering:

```text
 client -- 2001:db8:41::/64 -- router -- 2001:db8:42::/64 -- server A1/A2
                                         nftables filter, no NAT66
```

The routed IPv6 builder installs exact permanent neighbor entries inside the
three disposable namespaces. This makes link readiness deterministic without
sleeps or enlarged STUN deadlines; no neighbor state is added to the host.

Every behavior-matrix case uses fresh namespaces. Filtering therefore starts
without mapping-test state. Changed-source filtering responses use a narrow
stateless `notrack` destination rewrite before the stateful outbound SNAT rules;
this prevents their reverse conntrack tuples from contaminating later ADM/APDM
mapping measurements. All ordinary requests and replies still use Linux
conntrack and stateful NAT.

## Independent evidence

The production profiler does not compute its own expected result. The runner
compares four evidence sources:

1. nftables counters on the router's client-facing interface prove that Hydra
   traffic used `Mux.Port()` internally and that no Hydra packet used another
   source port;
2. the four-socket responder records request destination, observed source,
   Transaction ID, requested source change, and actual response socket;
3. named gateway counters prove whether A2:P2 and A1:P2 changed-source responses
   were accepted or dropped;
4. `natprobe.Profile` supplies the result under test.

The baseline XOR-MAPPED-ADDRESS must equal the responder observation. Across
NAT, the external port is allowed—and in controlled tests required—to differ
from `Mux.Port()`. On direct paths they must match.

Responder and client readiness use atomic JSON files. Baseline-sensitive route
and server failures use a baseline-ready/barrier pair. No scenario waits for
startup, DHT readiness, profile phase changes, or fault injection with an
arbitrary sleep.

## Artifacts

Successful and failed privileged runs retain `/tmp/hydra-m4b-<runid>/` with:

- `environment.txt`, `commands.log`, `results.json`;
- normalized `host-before.json` and `host-after.json`;
- topology and namespace address/route snapshots;
- router nftables rules and counters;
- conntrack output when the optional tool is installed;
- helper stdout/stderr, responder observations, and profiler results.

Payloads, identity private keys, and credentials are not copied into reports.
Runtime `hydrad` data uses separate, strictly named
`/tmp/hydra-m4b-runtime-<runid>-...` directories. Each lab removes those
directories during automatic cleanup, while the evidence directory remains;
the manual cleanup command also removes interrupted runtime directories after
validating their full names.

## Commands

```bash
make test-regression       # all unprivileged M0–M4a regressions
make test-m4b-build        # normal-user helper and runner build
make test-m4b              # full run, matrix x2, focused system tests x10
make test-m4b-soak         # five direct/NAT/discovery iterations
scripts/netns/inspect_m4b.sh
make clean-m4b
```

Ordinary `go test ./...` reports the privileged tests as skipped. This protects
developer machines. It is not an M4b pass: `make test-m4b` is the acceptance
entry point and fails if `sudo -n`, CAP_NET_ADMIN, nftables, namespace IPv6, or
the normal-user-built helpers are unavailable.

## Limits

M4b is a bounded laboratory observation. NAT behavior can change with kernel,
ruleset, conntrack pressure, port conflicts, and path changes. The lab does not
measure mapping lifetime, hairpinning, port prediction, or public-Internet
middleboxes. It does not persist profiles or implement NetworkGeneration.

Most importantly, M4b is not NAT traversal and does not predict peer-to-peer
success. ICE remains deferred to M5, and AmneziaWG/HydraBind remains deferred to
M6.
