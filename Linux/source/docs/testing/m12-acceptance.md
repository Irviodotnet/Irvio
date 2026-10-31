# M12 acceptance and test map

M12 has three evidence layers. Package tests prove protocol invariants and
state fencing. The compact model explores large logical populations. The
privileged process test launches the actual `hydrad` binary in namespaces and
uses kernel/remote observations. A skipped or privilege-blocked process test is
not a pass.

## Commands

Focused development gates:

```bash
make test-m12
make test-m12-netns
make test-m12-soak
make test-m12-scale
make demo-m12
```

`test-m12-netns` and `demo-m12` first build all helpers as the normal user and
then require non-interactive `sudo -n` only for the prebuilt namespace runner.
No Go command or repository mutation is run as root. The runner uses only its
owned namespace, veth, nftables, route, TUN, cgroup, and child-process names.

Final source gates are:

```bash
test -z "$(gofmt -l .)"
git diff --check
go vet ./...
go test ./...
go test -shuffle=on -count=10 ./...
go test -race ./...
go test -race -shuffle=on -count=5 ./...
go build ./cmd/irviod
go build -trimpath ./cmd/irviod
```

All M0-M11 regression, namespace, soak, and demo targets remain mandatory.

## Process-level topology

`TestM12_HydradBrokeredInternetAndRendezvousIndependentRenewal` launches real
production processes for bootstrap, three M11 verifiers, ingress R1, owner R2,
an Exit, a protected Client, and deterministic Internet on separate routed
underlays. R1 intentionally does not own the Exit registration. The Client is
configured only with R1; the Exit registers only with R2. R1
discovers/authenticates R2 and uses remote `SAMPLE_LOCAL`. Namespace firewall
rules prohibit external Client-to-Exit TCP in both directions. The mandatory
NAT-to-NAT/PRFLX variant is tested separately; this routed topology must not be
described as that proof.

`TestM12_HydradBrokeredNATToNATPRFLXInternet` supplies that separate proof.
Both production peers are behind independently owned nftables NATs. The Client
has EIM+EIF behavior; the Exit has APDM+APDF behavior, so its STUN observation
is `:52011` while the peer-specific mapping is `:52012`. Direct external TCP
between Client and Exit is blocked, the Exit registers only with owner R2, and
the Client knows only ingress R1. The actual runtime-selected AWG endpoint must
be the learned PRFLX `:52012`. Independent STUN observations, router NAT
counters, one-UDP-socket checks, and the Internet-observed Exit NAT source are
required before the scenario passes.

`TestM12_HydradBrokeredExitFailoverRemainsProtected` extends the production
path to two independently registered Exits. The Client knows only ingress R1;
both Exits register only with owner R2. R1 obtains both candidates through a
bounded remote `SAMPLE_LOCAL`, and M9 retains distinct ACTIVE and RESERVE
PeerIDs. The test sends a real Internet flow, kills the ACTIVE `hydrad`, runs
continuous M8 leak probes across the fault, and requires promotion of the
cached brokered RESERVE with a higher SessionGeneration. The deterministic
Internet oracle must observe the source change from the old Exit WAN to the
new Exit WAN while the direct physical-WAN observation count remains zero.

`TestM12_HydradDualRoleMaintainsIndependentTunnelControl` launches Node A as
both a protected Client of Exit C and an Exit for Client B. Both relationships
use brokered establishment and independent in-tunnel Noise maintenance over
one shared `hydra0`/AWG transport. The deterministic Internet oracle requires
A's local flow to have C's WAN source and B's flow to have A's WAN source;
C's NAT counter must not advance for B. Exact table-51822 routes own both
lease directions, both short leases renew, and closing B removes only B's
route while A's upstream lease and Internet path remain usable.

The main process test installs nftables drops for TCP and UDP destination port
53 in every Client, Router, Exit/Rendezvous, and Internet namespace before any
Hydra process starts. The complete lifecycle, owner restart, and all-R-down
renewal phase must pass, after which independent counters for both DNS
transports in every namespace must still be zero. No telemetry service or
telemetry configuration exists in the production runtime.

The independent oracles are:

- matching R1 `local=0, remote>=1` and R2 `SAMPLE_LOCAL returned>=1`
  production events, with the Exit registration independently restricted to
  R2;
- authenticated runtime PeerID and AWG key epoch;
- runtime Network/Transport/Session generations and lease ID/expiry;
- selected ICE-to-AWG endpoint through existing M5-M6 status;
- deterministic Internet server observing the Exit WAN source and Client
  leased inner source;
- table 51822 exact route, unreachable default, and mark rule;
- nft counters proving `0x48595443` session-control packets use `hydra0` and
  zero use a non-Hydra interface;
- a privileged marked-TCP connect oracle that succeeds with the exact inner
  route, fails after route removal, and succeeds after restoration while the
  physical-interface marked counter remains zero;
- removal of `hydra0` after clean shutdown.

After promotion, the test first kills owner R2. The Exit observes zero active
registrations, R2 restarts with the same PeerID but a new ServerIncarnation and
an explicitly empty in-memory registry, and the Exit establishes a fresh
registration without disturbing the ACTIVE Internet path. The test then kills
every Rendezvous process and keeps the legacy external Client-to-Exit TCP
prohibition. With a three-second lease TTL it requires the same lease ID,
SessionGeneration, and AWG epoch to renew beyond the initial expiry plus
twelve seconds, then proves Internet again. This is at least five lease
validity periods including the initial grant. The process fixture sets both
descriptor and receipt lifetime to six seconds, so the post-loss barrier also
proves that both discovery objects have expired before the final Internet
probe.

## Acceptance matrix

The following maps each required scenario to its primary executable evidence.
Some rows combine an M12 test with a retained earlier gate; the final report
must record the actual result of every cited command on the final tree.

| ID | Primary automated evidence |
|---|---|
| A | `TestM12_HydradBrokeredInternetAndRendezvousIndependentRenewal` plus `TestM12_HydradBrokeredNATToNATPRFLXInternet`: two brokers, no direct external TCP signaling, real NAT-to-NAT ICE/AWG/Internet, and actual PRFLX `:52012` rather than signaled SRFLX `:52011` |
| B | `TestThirteenRendezvousDiscoverFourCountryPoolsWithoutFullMembership` uses 13 real libp2p/Rendezvous instances, four explicit country fixtures, and ingress-to-owner sampling without full membership; `test-m12-scale` adds common/rare logical pools |
| C | `TestRegistrationManagerUsesMinThreeAndPreservesDistinctOwners` for S=1/2/3 |
| D | M9 batch cardinality tests plus `TestCandidateProviderProgressesFiveToTenWithinFourGETs`; no automatic country fallback remains an M9 invariant |
| E | `TestCandidateProviderProgressesFiveToTenWithinFourGETs`, frame-size tests, PeerID merge/dedupe tests |
| F | `TestTwoBrokerSamplingAndSignalingDoesNotRecurse`; ordinary clients are denied SAMPLE and the SAMPLE handler has no remote sampler path |
| G | M12 process test kills owner R2, observes registration loss, restarts the same PeerID with a new ServerIncarnation and empty live registry, proves fresh re-registration and uninterrupted ACTIVE Internet, then kills all Rendezvous while direct renewal continues |
| H | `TestRegistrationReplacementFencesLateOwnerEvents` |
| I | `TestSignedTranscriptAuthenticatesExactBytesAndChain`, `TestReplayGuardCoalescesExactDuplicateAndRejectsMutation`, provisional lifecycle tests |
| J | signed transcript generation checks, M12 pending-owner cancellation/replay tests, runtime coordinator disconnect/NetworkGeneration fencing, and retained real-process M10 disconnect/migration/rotation gates; the M12 process paths additionally fence Client crash/restart and late registration-owner events |
| K | all-R-down phase of the M12 process test with direct in-tunnel renewal across at least five short TTLs |
| L | all-R-down phase uses six-second signed descriptor/receipt lifetimes and waits more than twelve seconds through direct lease-renewal events; the same promoted session and Internet path survive expired discovery metadata |
| M | session-control idempotency, lost RELEASE response, and altered two-phase rotation COMMIT tests plus M10/M12 renew-vs-expiry/release race tests |
| N | `TestReconnectingClientRepairsBrokenNoiseChannelWithoutBroker` |
| O | linuxroute/exitfirewall/controlsocket tests and M12 process mark/interface counters |
| P | M12 process test removes the exact table-51822 `/32`, requires the same marked TCP connect to fail with zero physical-WAN packets, restores the route, and requires the connect and Internet path to succeed again |
| Q | `TestM12_HydradDualRoleMaintainsIndependentTunnelControl`: production dual-role A→C and B→A, two independently renewing inner Noise channels, distinct leases/routes, independent Internet source/NAT oracles, no B→A→C, and downstream teardown isolation |
| R | Main M12 process test kills all Rendezvous, sends the production Exit a planned SIGHUP rotation request, completes the old-key READY/COMMIT carrier barrier and fresh new-key handshake, verifies the signed binding/epoch and renewed lease over the authenticated inner carrier, retains SessionGeneration/Mux/TunnelLease, and proves Internet afterward |
| S | admission tests, bounded registry/service tests, and `TestM12AuthenticatedRendezvousLoad` |
| T | `TestMMDBResolverUsesCountryAndHotReloadsAtomically`, unknown/conflict registry tests, production documentation-address rejection |
| U | main M12 process test blocks TCP/UDP 53 in every namespace before startup, completes the full lifecycle and recovery, and requires all DNS-attempt counters to remain zero; production runtime has no telemetry dependency |
| V | `TestM12_HydradBrokeredExitFailoverRemainsProtected`: real hydrad ACTIVE death, cached brokered RESERVE promotion, higher SessionGeneration, changed Internet-observed Exit source, and zero direct-WAN leak across the fault |
| W | Main M12 process test SIGKILLs/restarts the protected Client and owner Rendezvous: Client PeerID and kernel protection are adopted before brokered supersession, direct WAN remains blocked, R restarts with the same PeerID but a new incarnation and empty registry, and the Exit repairs only its missing registration |

Component architecture alone is not process evidence. M12 must not be reported
complete until the cited privileged variants run successfully on the final
tree; an unavailable privilege path remains BLOCKED rather than PASS.

## Soak map

`make test-m12-soak` runs package state machines under `-race` for the required
100 registration, transcript/replay, and session-control cycles; 50 progressive
sampling/two-broker cycles; and 25 full process broker/Internet/renewal cycles.
Earlier M9-M11 soaks remain the current executable evidence for
NetworkGeneration and key rotation. They are not misreported as brokered M12
compositions. The M12 soak includes ten real brokered ACTIVE-to-RESERVE
process failovers and ten production dual-role in-tunnel-maintenance
compositions in addition to retained coverage.

The 25-cycle main process soak includes a broker-independent direct-control
AWG rotation after all Rendezvous processes are down. Each cycle requires a
new verified key epoch while retaining SessionGeneration, Mux port, lease, and
Internet connectivity.

## Production defects found during final acceptance

Final process-level repetition exposed five lifecycle defects that focused
component tests did not model: dual-role TUNNEL_CONTROL routes lacked an
explicit source address; the privileged M12 NAT target omitted the prebuilt
RFC5780 helper; a valid older descriptor could not be used as a locator to
obtain a freshly signed READY binding; and one-phase AWG rotation could destroy
the old cryptokey route before the Exit read the rotation request. The soak then
found an external initiation retry racing the pinned AmneziaWG retransmission
timer; the final implementation delegates retries exclusively to the device's
configured bounded timer. The final tree fixes each defect with a regression
and repeats the affected real-process suite. A separate short-TTL shutdown race
established the documented best-effort RELEASE response behavior while
retaining expiry/quarantine safety.

The unfiltered historical privileged gate also exposed an independent M11
test-oracle defect. Accepted TCP replies can take the earlier cgroup socket
match, conntrack mark, and route/output restore path, in which case the later
direct cgroup-output fallback counter correctly remains zero. The corrected
oracle requires either marked output path and, for restoration, the matching
cgroup-owned inbound counter; static-PAT ingress/egress, M8 bypass, and the
same-destination ordinary-process denial remain independent requirements.

Long M10 repetition exposed an independent production race in the pre-M12
lease lifecycle. Immediate allocation and same-PeerID supersession released
the lifecycle lock while their resource activator installed AWG and route
state, but still appeared as an `ALLOCATED` entry with a zero provisional
deadline. A concurrent expiry sweep could therefore revoke the entry after
resource installation and make the otherwise successful acquire return an
internal error. Both immediate activation paths now enter the same in-flight
mutation fence used by explicit activation. Deterministic
reconcile-during-activation regressions pass 100 race-detector iterations,
followed by real `hydrad` lifecycle, failover, and dual-role repetition.

## Cleanup

The runner snapshots and compares host routes, rules, addresses, nftables,
forwarding, cgroup state, ownership, and namespace/TUN/veth residue through the
existing M4b-M11 integrity framework. Cleanup must pass twice. Failed runs keep
bounded redacted logs; successful scratch artifacts follow the existing lab
cleanup policy. No secret key, ICE password, or full transcript is an allowed
diagnostic.
