# Private IRVIO v1 — acceptance audit

## Final local disposition

P00–P11 evidence mapping and the final local verdict are in `private-irvio-v1-handoff.md`: PASS WITH OBSERVATIONS for the stated v1 scope. Final packaged 100-cycle repetition `d753e940` passed; `private-irvio-final-package-resources.md` records all 101 samples, normal RELEASE checks, preserved failed-attempt evidence and independent host-DNS equality. No production source changed after the packaged UI fix. Test-helper namespace affinity was corrected separately. All sections below are chronological audit history; their pending/open statements are superseded only by the specific later evidence, not erased. No public release or production deployment is claimed.

## Current package — `f5f816b`

The initial final-package resource attempt `855a8439` failed because lab DNS substitution was unreliable; generic host integrity did not cover actual-host resolver state. Host DNS was restored. Test launcher thread affinity was corrected, short real lifecycle repetition `ae8c3a20` passed, and a 100-cycle repeat with explicit host-DNS comparison is running as `irvio-private-lifecycle-f5f816b-r2.service`. See the package report for exact evidence and the remaining uncertainty about the original scheduling mechanism. This supersedes the initial launch state below, not its preserved failed evidence.

`private-irvio-preview-f5f816b.md` records the clean audited package containing the native dialog fix and its installation on the mini-PC. Profile file contents were preserved; ordinary-user socket activation, idle Shutdown and ordinary WAN passed. A new autonomous 100-cycle resource run on this exact packaged daemon has started; its result is pending. Do not interpret the historical pending statements below as the current installed version, or this launch as a completed gate.

## Native UI follow-up

The previously unclassified rapid Cancel/reclose black frame was reproduced and corrected without Core changes. `private-irvio-native-dialog-fix.md` records the red widget regression, explicit route-completion fence, ten successful native repetitions and combined Shutdown with clean host state. Refresh the package/installed UI after this correction; the `5095442` archive remains the earlier build.

## Current delta — `5095442`

`private-irvio-native-tun-close-results.md` records the narrow cancellation-pipe correction, deterministic error-preservation tests, 10,000 real TUN cycles, affected race suites and whole-tree vet/test. `private-irvio-preview-5095442.md` records a clean audited tarball, normal/trimpath hashes, full 130-test Flutter suite, eight passing packaged private lifecycle cases and refreshed installation on the mini-PC. Installed idle socket activation/Shutdown, preserved profile content and ordinary WAN passed. Earlier statements that the installed application lacks the replay fix are now historical.

The prior 100-cycle full-network result retains its original binary hash; the new NativeTun test is not a replacement for that evidence. Remaining final audit/native UI disposition and handoff requirements are not waived by this update. No GitHub/public release or production distributor changes occurred.

## Current delta — `bbeb819`

The older baseline table below is historical; its pending-run statements are superseded by these completed checks. This remains an incomplete acceptance audit, not a release verdict.

| Requirement | New evidence | Scope / remaining limitation |
| --- | --- | --- |
| P02 renewal/revoke fence | `private-irvio-renewal-replay-results.md`: real TCP/Noise/private policy/lifecycle regression exposed cached success after completed revoke; fixed in `bbeb819`, red 10/10 then green | Resource callbacks are controlled, not an AWG renewal race; installed preview still predates the fix |
| P03 publication isolation / P06 country metadata | `private-irvio-public-isolation-audit.md`, country-change selector regression and public-path source audit | Country metadata changes cannot change pinned identity; this is not physical host relocation |
| P07 inherited socket crash | `private-irvio-socket-crash-results.md`, artifact `14fdb048`, PASS, recovery 2.286s | Actual systemd inherited listener with packaged binary in isolated topology, not connected installed zero-argument host service |
| P07 new OS boot | `private-irvio-real-reboot-procedure.md`: different actual boot IDs, pre-reboot active private traffic, postboot no intent/protection/autoconnect, preserved trust reopened idle | Normal reboot, not power-cut; installed idle startup and preserved private profile checked separately |
| P07 multiple downstream clients | `private-irvio-multiple-clients-results.md`, `bd70b0b4`: three selective-revoke and three full-leave cases PASS | Initial stale counter assertion was corrected in the harness; no product counter change |
| P09 rapid close dialog / native tray | Widget shutdown and full 130-test UI suite PASS; `private-irvio-native-tray-results.md`: real AppIndicator D-Bus menu action and combined daemon-owned shutdown PASS (`0c03e0db`) | Native menu protocol exercised, not panel icon/Wayland; earlier rapid Cancel/reclose black frame remains unresolved |
| P10 100-cycle resources | `private-irvio-final-lifecycle-results.md`, `2410079c`: 100 cycles PASS, 101 samples FD=9/pipes=2/sockets=2/netlink=0 for both processes, baseline cleanup and remote RELEASE PASS | On `01cfb88`, before the replay fix; synthetic DNS in this run, actual host DNS separately proven; bounded RSS, not universal heap-leak proof |
| P11 refreshed local installation | `private-irvio-final-preview-01cfb88.md`: installer/reinstall and idle socket checks PASS; `private-irvio-preview-bbeb819.md`: new clean audited archive and four packaged traffic/revocation/RELEASE cases PASS | Installed application still lacks `bbeb819`; new preview is extracted and tested, not yet installed |

After `bbeb819`, mini-PC `go test -race -shuffle=on -count=3 -timeout=180s` passed for `internal/runtime`, `internal/productruntime`, `internal/sessionestablish`, `internal/tunnelsignal`, and `internal/exitselector` (27.616s / 1.068s / 1.316s / 1.988s / 1.730s). These component runs do not replace privileged traffic gates.

Earlier `/tmp/hydra-m4b-*` evidence is now archive-relative: the real reboot cleared tmpfs. The protected archive path and verified checksum are recorded in `private-irvio-real-reboot-procedure.md`; do not interpret missing old `/tmp` directories as failed tests or expose the private evidence in Git.

Remaining work includes native UI/tray disposition, requirement-by-requirement bounds/negative-case/API/profile-deletion audit, exact affected privileged regression coverage, post-fix packaged traffic checks, and final local handoff. No GitHub or production distributor update is authorized.

The subsequent `private-irvio-bounds-audit.md` maps storage/invitation/discovery limits to enforcement and tests. New capacity/name/revision regressions prove rejection leaves both memory and reopened durable state unchanged; five race/shuffle repetitions passed. Three repetitions of private discovery/Rendezvous tests passed. This narrows the bounds-audit gap without claiming end-to-end capacity load or active-network profile deletion.

Active-network profile deletion subsequently passed twice in `ad179aef` with the packaged replay-fix binary: new identity/empty trust and bootstrap, no restored intent, upstream lease released, downstream denied without scoped WAN leak, clean baseline and shutdown. See `private-irvio-active-profile-deletion.md`. Whole-tree vet/test also passed (cached unchanged packages; no opt-in privileged coverage implied).

Latest public compatibility evidence is in `private-irvio-public-regressions-bbeb819.md`: 19 privileged cases passed with the packaged replay-fix daemon, clean host baseline, and five component suites passed five race/shuffle repetitions. A separate NativeTun stress execution failed intermittently with a closed-pipe error before three successful 500-cycle repetitions. That new finding remains open; the overall acceptance verdict is not PASS.

The NativeTun close finding was subsequently localized to a cancellation pipe race and narrowly corrected; `private-irvio-native-tun-close-results.md` records deterministic owner/borrower negative cases, 20 passing real 500-cycle repetitions, affected race suites and whole-tree vet/test. Refreshed packaged/installed acceptance is still outstanding after this production change.

## Historical audit baseline

Audit baseline: `15c65d8` on `private-irvio`, 2026-10-06. This is a coverage assessment, not release acceptance. Canonical requirements remain `docs/product/private-irvio-implementation-plan.md`; chronological commands, artifact IDs and limitations remain in `private-irvio-v1-validation.md`.

At audit baseline the production source matched preview `90f8431`. A subsequent real-TCP/Noise regression confirmed premature RELEASE revocation on an empty read; the fix and its distinct lab binary are recorded in `private-irvio-resource-nat-results.md`. The older preview must not be presented as containing that fix. Earlier resource runs retain their own binary hashes and evidence scope.

## Requirement coverage and remaining decisions

| Plan requirement | Concrete evidence inspected | What still needs proof / disposition |
| --- | --- | --- |
| P00: explicit private contract, bounds, threats, target binding | `docs/product/private-irvio-v1.md`; invitation, admission, private protocol and target tests listed below | Final cross-check of every contract bound against implementation, not merely existence of the document |
| P01: identity-bound persistent trust, strict/corrupt state, permissions, atomicity, deletion and one-use races | `internal/privateaccess/store_linux_test.go`, `invitation_test.go`, `import_linux_test.go`; real enrollment and preview reinstall evidence | Recheck full package, since a `Private` name filter misses `TestSingleUseRace`, strict decoding and corruption tests |
| P02: PREPARE/offer allocation, legacy paths, activation/renewal revision fences, bounded revoke | `internal/privateaccess/policy_linux_test.go`; `internal/sessionestablish/responder_test.go`; `internal/runtime/exit_server_linux.go` and `m12_exit_linux.go`; real ICE and established-traffic revoke artifacts | Real dataplane activation/renewal race coverage is weaker than the controlled resource-callback races; do not claim every integrated interleaving is exercised |
| P03: explicit negotiated private protocol and no public fallback | `TestPrivateServiceRegistrationLookupAndNoPublicFallback` uses real libp2p with a public-only service, then colocated public/private registries; `private_test.go` rejects cross-domain/expired/forged records | This is a genuine unsupported-protocol test, not an old-release binary deployment; final audit must preserve that distinction |
| P03: M11 gates, private advertisement withdrawal and public exclusion | `TestPrivateDistributorRuntimeReadinessAndWithdrawal`; `private_exit_test.go`; netns country exclusion and real private forwarding | Enumerate every public publication path rather than infer exclusion solely from one country RPC |
| P04: B1-only client, disjoint registration owners, owner loss/replacement/restart | Netns disjoint/migration run `13fe840e`; restart `8efe1ce6`; real DHT `private_dht_test.go` | Record bounded 136s owner re-registration tail as an observation; do not advertise fast restart recovery |
| P04: stale/forged hints, owner-bound receipts, expiry and cache | Real cross-owner receipt replay in `private_distributed_test.go`; registry/lookup/adapter/publication tests | Map each expiry/locator-refresh assertion to the plan; adversarial netns traffic is not implied by component transport tests |
| P04: addressless provider identity preservation | `TestPrivateAddresslessOwnerUsesFindPeerAndAuthenticates` invokes real `FindPrivateOwners` over libp2p with controlled addressless DHT replies; requires client-specific FIND_NODE, accepts the eligible owner and rejects missing private capability or a substituted endpoint | This closes private owner resolution/authentication coverage, not an addressless full ICE/dataplane campaign; M11 proofs are signed fixtures |
| P05: signed invitation, end-to-end enrollment through owners, explicit approval, cancellation, replay and durable trust | `invitation_test.go`, `enrollment_linux_test.go`, `import_linux_test.go`, `private_enrollment_linux_test.go`; native two-UI run `a6830167` | Final negative-case audit must distinguish controlled TLS/store tests from real ICE authorization; QR export remains sensitive user-chosen output |
| P06: typed exact PeerID, signed private transcript, no country/other-peer fallback | `internal/exitselector/private_target_test.go`, `internal/sessionestablish/private_target_test.go`, `internal/runtime/private_target_test.go`; real migration/crash/remap traffic | Verify host country-change and unrelated-candidate cases against existing assertions; do not replace these with generic CONNECTED checks |
| P07: hosting-only / upstream / combined lifecycle | Real basic, combined and crash runs; GUI death for client/host; combined native Shutdown `17f990e9` and `a3c25c50`; systemd client recovery `8aaf2a0a`; actual-host private client DNS `ddc65c4c` and `c6386ba4` | Packaged socket-FD adoption and fresh-boot evidence remain incomplete; actual DNS proof covers upstream client lifecycle, not every combined-mode interleaving |
| P07: fresh boot versus persistent trust | `TestPrivateDesktopEmptyRuntimeDoesNotRestorePersistentTrust` covers three intents with empty runtime and retained profile | Component test mocks protection and is not a kernel reboot after an active private session |
| P07: delete, pending-operation cancellation, idle exit | Product lifecycle and Local API tests; installed idle socket activation/Shutdown and reinstall | Audit active private profile deletion and hosting-not-idle assertions; installation smoke alone is insufficient |
| P08: negotiated/authorized/bounded asynchronous API | Local API private dispatch/operation/mapper tests; real Unix API and native GUI interactions | Retain distinction between socket authorization tests and private mutation semantics; review secret redaction coverage |
| P09: import/save QR, approval, explicit CONNECT, localized state, GUI recovery | Native `a6830167`; client/host GUI death `a85a5fa2`; EN/RU/DE widget fixtures; controller epoch/revision tests | Native tray action still untested; black-frame observation during rapid Cancel/close in Xvfb remains unclassified; normal spaced close flow passed |
| P10: NAT and actual traffic/security | Public, client/host EIM and controlled endpoint-dependent mappings, shared-NAT LAN and incompatible shared-NAT failure; real traffic and scoped security counters; actual host DNS lifecycle passed twice | Defined endpoint-dependent models do not establish arbitrary two-sided APDM traversal; DNS lifecycle evidence is described separately from synthetic DNS-server reachability |
| P10: 100-cycle resources on final implementation | Current-preview `3a70e777`: 100 cycles PASS, 101 idle samples, FD=9/pipes=2/sockets=2/netlink=0 throughout for both peers; per-cycle network baseline and remote lease removal passed | Five unconfirmed RELEASE warnings require separate acknowledgment-path triage; no retained lease was observed. Actual DNS is still substituted |
| P10: affected public compatibility | Privileged M8/M9/M10/M11/M12/P1 and Safety Policy results under `9eb7696`, plus package and UI gates | Match exact test names to bootstrap/addressless/COLD/promotion requirements; do not equate skipped privileged tests with a pass |
| P11: clean local preview, allowlist/checksums and install/update/uninstall | Clean preview `90f8431`, 567-file audit; extracted runtime; installed idle activation; persistent-state equality across reinstall | Final handoff must include migration limitations and the unresolved gates; no publication or production distributor upgrade is authorized |

## Security entry-point inspection

`NewExitServer` refuses private policy without M12. Private lifecycle receives the admission policy and requires a session token. Legacy ICE authorizer rejects private requests before prepared transport creation. M12 PREPARE/OFFER propagate the authenticated end-client and admission revision; provisional allocation uses `AllocateAuthorizedProvisional`. Policy tests exercise rejected legacy allocations, old revisions after regrant, denied activation/renewal/lookups, resource mutation, revocation failure, and bounded asynchronous drain. These checks support the design but do not substitute for the remaining integrated race matrix.

`FindPrivateOwners` obtains at most eight DHT provider hints, resolves initially addressless peers through the existing helper, verifies owners with three workers under a ten-second context, and requires signed Rendezvous/public-control capability plus the private feature. Missing or failed results are incomplete, not authoritative target absence. The routing hint alone does not authorize access. Separate candidate decoding and the pinned target provider still need to validate the actual Exit/receipt.

## Current-preview resource run

Launched on the mini-PC under transient unit `irvio-private-lifecycle-15c65d8-r1.service`, invocation `6c034d97cc1744e490eb0fac268baf82`, runner PID initially 270136, artifact root `/tmp/hydra-m4b-3a70e777`. At launch it was authoritatively `active/running`; **no result is claimed here**. Recheck unit/process and immutable final `results.json` before interpreting completion. Do not run another privileged topology or reboot the mini-PC while this run remains active.

- Production daemon: extracted preview `90f8431`, SHA-256 `c43a377bec00017f7c3dde2ea3609a7996ba9dd3dab19ae6852cfe01ce234667`.
- Runner: `private-gui-shutdown.test`, SHA-256 `bafb687ed547f5e16ba031512ab20c00b73f60a8a98008637ca32940484c5604`.
- Selection: `-test.run '^TestPrivateRuntimeLifecycle100$' -test.count=1 -test.v -test.timeout=24m`, production child GC tracing enabled; unit RuntimeMaxSec 25min.
- Existing isolated `/run` launcher and synthetic GeoIP retained. Native resolvectl is not exercised by this lab.
- New-cycle oracle waits for the client's downstream RELEASE while the host is still hosting; this was absent from the original pre-fix 100-cycle proof.
- Initial unit without `-r1` exited 32 before the Go runner: cgroup2 was already mounted in PID1's namespace. Its journal is preserved. The corrected launch mounts only when `cgroup.controllers` is absent; no product code or timeout was changed.

## Next acceptance priorities

1. Preview 100-cycle resources passed. The follow-up RELEASE empty-read defect now has a red/green real-Noise regression and three passing short real-dataplane repetitions; rerun final-binary resources and refresh the local package after this production fix (see `private-irvio-resource-nat-results.md`).
2. Actual private client DNS installation/query/restoration passed twice (`private-irvio-host-dns-results.md`); complete fresh-boot proof while preserving same-boot fail-closed semantics.
3. Client and host controlled endpoint-dependent NAT each passed three repetitions in `67ec1f7a`; integrated revocation boundary evidence remains incomplete. This does not prove arbitrary two-sided APDM traversal.
4. Resolve or bound the native UI rendering observation and exercise tray Shutdown.
5. Finish requirement-level audit and handoff; keep the goal active until those checks establish the original scope.
