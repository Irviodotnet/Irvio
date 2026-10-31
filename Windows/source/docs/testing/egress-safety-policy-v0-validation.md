# Exit Egress Safety Policy v0 validation

## Build identity

- Baseline: `e4d61b4c694c257019a51e73f07a0378770d61a9`
- Validated implementation/test HEAD: `c733f542149c3dadab942f49e621bba13406e5a7`
  (the commit carrying this final report update is documentation-only)
- Branch: `feat/egress-safety-policy-v0`
- Final binary identity is recorded in the task handoff after building the
  documentation-complete HEAD.

## Implementation

`internal/safetypolicy` is the single platform-neutral source of truth. Its
Linux compiler emits two constant `inet_service` sets:

- TCP: `19, 25, 135, 137, 138, 139, 445`
- UDP: `19, 135, 137, 138, 139, 1900, 11211`

The client compiler is integrated into `internal/killswitch`. Its two counted
REJECT rules match `oifname hydra0` and precede the generic tunnel accept. The
Control Plane mark bypass is earlier, so the rules apply only to inner user
traffic.

The Exit compiler is integrated into `internal/exitfirewall`. Its two counted
DROP rules match `iifname hydra0` and precede `accept-hydra-wan`; there is no
earlier established/related forward accept. Source/lease isolation remains
earlier and anti-multihop remains independently enforced. NAT is later in
postrouting.

Both tables are `inet`, so the same sets and rules cover IPv4 and IPv6. The
Exit table now has an ownership marker and crash adoption/reconciliation with
the same exclusive-table semantics already used by ProtectedMode.

## Data-plane result matrix

Every tuple below was exercised over a real TUN, AWG transport, Exit firewall,
and simulated Internet. A pre-policy Exit counter proved that modified-client
packets reached Exit `hydra0`; a post-policy WAN counter and target observation
file independently remained at zero for blocked traffic.

| Path | Families | TCP destination ports | UDP destination ports | Result |
| --- | --- | --- | --- | --- |
| Official client | IPv4 + IPv6 | 19, 25, 135, 137, 138, 139, 445 | 19, 135, 137, 138, 139, 1900, 11211 | PASS: local REJECT; Exit counters unchanged |
| Modified client -> official Exit | IPv4 + IPv6 | 19, 25, 135, 137, 138, 139, 445 | 19, 135, 137, 138, 139, 1900, 11211 | PASS: Exit DROP; WAN and target zero |

The modified client was simulated without changing the production binary: the
low-level full data-plane test omitted the client policy while retaining the
real TUN/AWG/lease path. The dual-role production-hydrad test additionally
removed only the two client safety rules by nft handle while preserving the
rest of ProtectedMode, then proved the independent Exit counter incremented.

| Required representative path | Expected | Actual |
| --- | --- | --- |
| Official client TCP/25 | BLOCK local | PASS |
| Modified client -> official Exit TCP/25 | BLOCK Exit | PASS |
| Official client TCP/445 | BLOCK local | PASS |
| Modified client -> official Exit TCP/445 | BLOCK Exit | PASS |
| Official client UDP/1900 | BLOCK local | PASS |
| Modified client -> official Exit UDP/1900 | BLOCK Exit | PASS |
| Official client UDP/11211 | BLOCK local | PASS |
| Modified client -> official Exit UDP/11211 | BLOCK Exit | PASS |
| Official client TCP/443 | PASS | PASS |
| Modified client -> official Exit TCP/443 | PASS | PASS |
| Client after Disconnect | normal WAN behavior | PASS (`TestM8_ProtectedModeKernelPolicy`) |

## Non-overblocking and ordinary traffic

All of these passed for IPv4 and IPv6:

- TCP neighbors: `18, 20, 24, 26, 134, 136, 140, 444, 446`;
- UDP neighbors: `18, 20, 134, 136, 140, 1899, 1901, 11210, 11212`;
- DNS UDP/TCP 53, HTTP TCP/80, TCP/443, generic high TCP and UDP.

Unit tests separately prove the asymmetric cases UDP/25, UDP/445, TCP/1900,
and TCP/11211 remain allowed by v0, and boundaries 136/137/138/139/140 are
exact.

The full matrix deliberately used physical Hydra UDP port `1900`. ICE and AWG
completed and allowed inner traffic passed, proving that an outer port which
equals an inner denied service is not filtered.

## Fragmentation

Raw out-of-order fragments were injected through the real client TUN/AWG path.
The Exit `hydra0` pcap contains two fragments for every required tuple in both
families:

- TCP/25 and TCP/445;
- UDP/19, UDP/1900, and UDP/11211;
- IPv4 and IPv6 (20 captured fragments total).

The Exit safety counters incremented after defragmentation, while the
post-policy physical-WAN counters and Internet target observations stayed at
zero. No blanket fragment drop was added. Sanitized evidence for the final
focused run is under
`/tmp/hydra-m4b-1bdd8fef/001-m7-safety-v0/safety-fragments.pcap`.

## Multi-client and dual-role

- Five simultaneous clients held independent leases on one Exit. TCP/443,
  UDP/53, and HTTP passed concurrently while TCP/25 and UDP/1900 were dropped.
  Other clients and leases were unaffected.
- A production `hydrad` simultaneously acted as CLIENT and Exit. Its own
  TCP/25 was rejected by client policy, downstream TCP/25 was dropped by its
  Exit policy, and both own/downstream TCP/443 paths passed through the correct
  egress. The upstream Exit NAT counter did not move for downstream traffic,
  preserving anti-multihop.

## Failure, crash, and lifecycle behavior

- Client install/setup failure remained fail closed in the existing M8 fault
  suite.
- An unowned conflicting `hydra_exit` table was rejected and left unmodified;
  no accept/NAT rule was installed.
- SIGKILL left an owned Exit policy. A missing mandatory rule/set plus an
  unknown residue chain was injected. Restart adopted and atomically
  reconciled the table without duplicate objects; graceful close removed it.
- The lab cleanup was called twice explicitly; the second call was a clean
  idempotent no-op, followed by the runner's normal cleanup hook.
- Explicit client Disconnect removed `hydra_protect` and restored ordinary WAN
  behavior.

Repeated lifecycle resource results:

| Scope | Cycles | FD | AF_NETLINK | Goroutines | nft residue |
| --- | ---: | ---: | ---: | ---: | --- |
| Client policy | 100 | 6 -> 6 | 0 -> 0 | 1 -> 1 | none |
| Exit policy | 100 | 6 -> 6 | 0 -> 0 | 1 -> 1 | none |

The recent real owner-TUN regression also passed 500 cycles with total FD
`6 -> 6`, AF_NETLINK `0 -> 0`, pipes `3 -> 3`, and TUN FD `0 -> 0`.

## Privacy and performance

Runtime evidence is aggregate nftables packet/byte counters. No packet address,
payload, or per-packet log was added. Policy size is bounded to two constant
sets and two transport rules at each enforcement point. Normal concurrent
traffic showed no obvious CPU or path disruption; no throughput benchmark was
needed for this change.

## Regression results

| Gate | Result |
| --- | --- |
| gofmt / git diff check / `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `go test -shuffle=on -count=10 ./...` | PASS |
| `go test -race ./...` | PASS |
| `go test -race -shuffle=on -count=5 ./...` | PASS |
| normal and trimpath hydrad builds | PASS |
| M6, M7, M8, M9, M10, M11, M12, P1 privileged suites | PASS |
| M5c2 controlled NAT/PRFLX matrix | PASS |
| M5c3 EIM/APDF reconnect and real netlink NAT migration | PASS |
| brokered client NAT remap, repeated remap x10, Exit remap | PASS |
| addressless-provider regressions x10 | PASS |
| COLD-route regressions x10 | PASS |
| DP promotion cleanup and owner NativeTun resource regressions | PASS |

The complete privileged suite artifacts are under
`/tmp/hydra-m4b-6fd11732`; final focused policy and fragment evidence is under
`/tmp/hydra-m4b-1bdd8fef`.

## Cleanup and host integrity

The lifecycle lab performed two explicit cleanup calls successfully. Every
privileged runner additionally compared its own before/after host snapshot and
reported PASS. The final task-level comparison against the pre-test snapshot
gave these results:

| Host state | Result |
| --- | --- |
| links | PASS, exact SHA-256 match |
| routes (all tables) | PASS, exact SHA-256 match |
| policy rules | PASS, exact SHA-256 match |
| network namespaces | PASS, exact empty-state SHA-256 match |
| nftables ruleset | PASS, exact SHA-256 match |
| IPv4/IPv6 forwarding sysctls | PASS, both restored to `0` |
| addresses | PASS after excluding DHCP lifetime counters |
| test processes and listening sockets | PASS, none left |

The sole raw address-snapshot difference was the normal renewal of the
existing Wi-Fi DHCP lifetime; interface names, MAC addresses, IP addresses,
prefixes, flags, and all other fields were identical. The tracked repository
was clean after the logical commits. Existing untracked `key.txt`,
`Hydra_UI.png`, and `pre_alpha_final_test/` were not modified.

## Known limitations

- Modified client plus modified Exit cannot be enforced by official software.
- Counters are available from the owned nftables tables; v0 does not add a new
  Local API or wire-protocol surface.
- This is a deterministic TCP/UDP destination-port deny list, not DPI, IDS,
  rate limiting, scan detection, or a centrally distributed policy.
