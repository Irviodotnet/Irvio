# Private IRVIO v1 — local handoff and evidence map

## 2026-10-09 — consolidated Linux baseline before Windows planning

The current UI uses **Choose connection type** and a single **Share my Internet** action: start hosting and display a one-hour invitation. There is no separate Start sharing button. Repeated selection while hosting only opens the existing sharing view. Global VPN keeps its country selector while private participation is active and offers navigation to Private VPN, never an implicit disconnect through a HOSTING ring (commit `0aa274e`).

Mini-PC validation: all 156 Flutter tests PASS, analyzer PASS, Linux UI release build PASS. Affected Go vet PASS; race tests for privateaccess/localapi/runtime/cmd/irviod PASS on the final invocation (some packages cached). The first race invocation hit `TestSlowSubscriberIsDisconnectedWithoutBlockingController: subscriber was not registered`; the isolated test then passed 30 repetitions and the affected suites passed. This scheduling-sensitive observation remains recorded, not a claim that the initial run passed. No new full regression or network soak was run for consolidation.

Windows cross-build probe on the mini-PC currently FAILS on Linux-bound networkstate, killswitch, identity, protocol incarnation, socketmux and productprofile implementations. This is expected porting inventory, not Windows support. The Windows plan will reference the consolidated source commit, not claim that the currently installed tarball contains it.

## 2026-10-08 — simplified invitation UX

This section supersedes the manual preview workflow below for the updated UI/daemon pair (Local API 1.7). Global VPN and the distributed private wire protocols are unchanged. Production distributors already support private routing; this change needs the updated desktop host and UI, not another distributor deployment.

- First opening shows **Share my Internet** and **Connect to a friend**.
- Sharing: **Share my Internet** starts hosting and creates a one-hour, single-redeemer invitation. The link and QR are displayed together. **Invite another friend** creates a separate invitation.
- Joining: paste the private invitation or import its QR, then explicitly press **CONNECT**. This click authorizes import, bounded redemption retries and connection to that exact host. Background approval polling never initiates CONNECT.
- New invitations preauthorize the first TLS-authenticated redeemer. Consumption and durable membership are committed atomically; the secret is cleared. Expiration bounds first use, not the lifetime of granted membership. Existing invitations retain manual approval semantics.
- Owner cards show waiting invitations and allowed members. Connected status comes from installed, unexpired, non-revoking leases; unavailable samples are not presented as disconnected. Revoke closes access and active leases; deleting an invitation also revokes a concurrently completed automatic grant.
- Saved connections reconnect without another invitation. Full Disconnect stops participation. Both peers still need the ordinary network bootstrap, distinct from the invitation.

Mini-PC validation: 142 Flutter tests, analyzer, Linux release build; privateaccess/localapi/daemon suites with race detector count=3; runtime race suite and affected vet. Final real netns run `280b01e2`: legacy enrollment/traffic/revoke PASS (20.03s), automatic invitation plus three reconnect cycles/traffic/deletion-revoke PASS (58.64s). HTTP/UDP/DNS/ICMP and scoped fail-closed oracle passed; independent actual-host DNS/domain/default-route comparison passed. These are real protocol/network tests with lab DNS isolation, not a new mobile-network manual test.

The stored invitation adds optional `auto_approve`; old readers reject that unknown field rather than silently weakening policy. Preserve the root-only profile backup when downgrading. No keys or invitation links belong in public artifacts.

Installed on the mini-PC with profile preserved and VPN disconnected. Backup: `/var/tmp/irvio-simple-ux-backup.6b6aRU` (private, never publish). Daemon SHA-256: `d9a9dba106e2def44cb4c963ecdc4e8e77b9c5f6334b10ae876680e05eeb85f8`; UI libapp SHA-256: `49293d5c64acb85c4a8d56824a75930f00014d2673347469f988187f2ffb299a`. No server, GitHub release, tarball or workstation installation was updated for this UX change.

## Historical initial handoff

Status: P00–P11 local implementation/validation complete, **PASS WITH OBSERVATIONS** within the documented v1 contract. Final packaged 100-cycle repetition passed in `d753e940`, including independent actual-host DNS comparison and cleanup; see `private-irvio-final-package-resources.md`. The earlier lab failure remains preserved, not converted to PASS. This is not a public release or authorization to update production distributors. Source baseline: `27851e57b2f856e3134377143f8240734056d07f`; implementation branch: `private-irvio`; packaged source: `f5f816b5341f24ea61e9993ec62f26a43a1c5b7f`. Later commits change test helpers and documentation only.

## Product behavior

The Private Network screen separates saved private connections from sharing this machine's connection. Both peers first import their normal bootstrap. The host enables private hosting and creates an invitation; the client imports its link or QR, requests enrollment, and waits for explicit host approval. Import/approval alone does not connect the client. CONNECT selects that exact saved PeerID; recovery cannot silently choose a different friend or a public country.

Hosting alone leaves the owner's traffic on ordinary WAN. Private upstream plus private hosting is supported: downstream traffic exits physical WAN, not the owner's upstream tunnel. Public upstream plus private hosting is deliberately rejected. Full Disconnect/Shutdown stops both private intents. Closing/crashing only the GUI does not stop networking; explicit GUI Shutdown is daemon-owned safe teardown. Same-boot daemon recovery restores intent; a new boot does not.

## Deployment boundary

Private-capable registration owners are required. Public-only distributors reject the separately negotiated private protocol; they do not receive a public fallback registration. Existing public bootstrap format is unchanged, but a bootstrap link alone does not upgrade a distributor. The production distributors have **not** been upgraded in this workstream. Therefore the installed mini-PC preview is not proof that private mode will work through the currently deployed public network.

Only the mini-PC installation was refreshed. Archive, executable hashes, protected update backup and installation checks: `private-irvio-preview-f5f816b.md`. No GitHub push, public release, website update or production server deployment has been performed. The archive is a local development preview.

## Manual preview workflow

Use a private-capable test network, not an assumption that existing production distributors already support the feature. Both desktop installations retain the ordinary zero-argument launcher, `irvio`; no private engineering flags are needed in the UI workflow.

1. Import the normal network bootstrap on both devices. A private invitation is not a bootstrap code.
2. On the host, open **Private VPN → Share my connection → Start private sharing only**. Its own Internet remains on ordinary WAN.
3. Choose **Create invitation · 1 hour**. Share the invitation link or use **Save invitation QR**; keep the link/image private.
4. On the client, enter a connection name and import the invitation link or QR. The UI automatically sends the request and checks pending approval every five seconds after each completed request. Retry is shown only for errors or paused requests. Approval saves the connection; **CONNECT** remains explicit and never happens automatically.
5. On the host, compare the request's PeerID with the friend's **My PeerID** over a trusted channel. Choose **Approve this PeerID** only after verification.
6. On the client, wait for the saved connection to appear, then explicitly CONNECT it. The selected identity, not a country or a friendly display name, determines the destination. Approval persists across reconnects until the owner revokes access.
7. To revoke one client, use **Revoke access** on the host. To stop all participation, use Disconnect; to unload the application, use Shutdown IRVIO.

**Also share my connection privately** enables the combined private upstream/hosting mode. It does not enable public Exit advertising. If the pinned host is offline or NAT traversal is incompatible, there is no public fallback; Disconnect is the explicit way to return an upstream client to ordinary WAN. A revoked client needs a new invitation and approval, not repeated use of the old QR.

Normal installed daemon logs remain in `journalctl -u irviod.service`; raw logs and screenshots may contain PeerIDs or private invitation material. Do not include them in public bug reports without review. The development netns tests use their separate artifact directories and do not write their evidence into the installed user's profile.

## Requirement map

The plan remains authoritative. Rows below group its numbered tasks without replacing their scope. Component tests, real transport tests, native GUI tests and privileged network tests are distinguished deliberately.

| Plan | Implementation / reviewed boundaries | Evidence and disposition |
| --- | --- | --- |
| P00, tasks 1–6 | `docs/product/private-irvio-v1.md`: intents, threat model, protocol domains, bounds, errors, identity-bound transcripts | Contract inspected together with bounds and isolation audits; no anonymity or universal NAT claim |
| P01, storage and gate | `internal/privateaccess/{state,store_linux,invitation,import_linux}.go`, product profile deletion | Unfiltered storage/race suite includes strict/corrupt state, identity/permissions, atomic failure, one-use/approval races; `private-irvio-bounds-audit.md`; active profile deletion twice in `ad179aef` |
| P02, tasks 1–5 | PREPARE end-client policy, revision-bound OFFER/allocation, lifecycle activation/renewal fence, legacy rejection, asynchronous drain | `policy_linux_test.go`, `responder_test.go`, `private-irvio-renewal-replay-results.md`; real ICE revoke `5f7e5782` and established forwarding revocation. Activation resource callback is controlled, not a paused physical AWG operation |
| P03, tasks 1–6 | Separate private stream/registry/receipt domains; M11 owner gate; no public country, sampling, encoder, provider or selector path | `private-irvio-public-isolation-audit.md`, private runtime readiness/withdrawal tests; unsupported-protocol peer uses current public-only library, not an archived old binary |
| P04, tasks 1–6 | Locator-derived provider CID, bounded fan-out, owner authentication, receipt/descriptor/cache expiry, replacement/withdrawal, addressless FindPeer | `private_adapter_test.go`, `private_test.go`, `private_lookup_test.go`, `private_distributed_test.go`, private DHT/addressless tests; disjoint owner migration `13fe840e`, owner restart `8efe1ce6`. Restart tail 136.166s is an observation, not fast recovery |
| P05, tasks 1–7 | Signed bounded invitation; bootstrap separate; identity-authenticated end-to-end TLS; pending approval; atomic consumption, retry/expiry/cancel/restart | Invitation/store/import/enrollment tests and native two-UI onboarding `a6830167`. Relay capture excludes plaintext secret; no custom cryptography. QR exports are sensitive user-selected files |
| P06, tasks 1–7 | Typed pinned target, exact identity/capability, private signed transcript, unchanged ICE/AWG/lease wire, owner requery without other-peer fallback | Private target selector/transcript/runtime tests; DE→NL→empty metadata test; real owner migration, crash and NAT-remap traffic. Country-change proof is metadata-level, not geographic relocation |
| P07, tasks 1–8 | Separate upstream/hosting intents; full leave; idle guard; persisted trust versus boot-scoped intent; active profile deletion | Combined/multiple-client tests, `private-irvio-multiple-clients-results.md`, real host DNS twice, GUI SIGKILL, combined daemon crash, inherited-socket crash `14fdb048`, actual reboot and active profile deletion. Normal reboot, not power failure |
| P08, API and gate | Negotiated private capability, kernel credentials, strict bounded commands/pages, async cancellation, sanitized errors | `private_linux_test.go`, `private_operation_linux_test.go`, Unix product API and kernel-credential tests. Installed ordinary-user activation/Shutdown passed; no new GUI privileges |
| P09, UI and gate | Saved connections, QR import/export, hosting, approval/revoke, explicit CONNECT, localized state, global Shutdown | Native two-UI onboarding, GUI death/restart, native AppIndicator action, full 131-test suite; rapid Cancel/reclose fixed and exercised ten times (`85edd3b2`). Native menu delivery tested, not every desktop-panel/Wayland combination |
| P10.1–5 | Distributed discovery, hostile/stale routing, authorization races, public isolation | Component transport/fencing tests plus referenced real netns scenarios. Do not describe all attacks as packet-injected live-network tests |
| P10.6–8 | NAT, real traffic/egress, leak/anti-multihop counters, lifecycle/crash/boot | Public/EIM, controlled endpoint-dependent client and host mappings `67ec1f7a`, shared-NAT `7fffb206`, incompatible NAT `080d2336`, actual DNS and reboot reports. No universal two-sided symmetric-NAT guarantee |
| P10.9 | At least 100 lifecycle cycles with per-cycle resources, lease and network cleanup | Final-package `d753e940`: 100 cycles/101 samples PASS, constant FD/pipe/socket/netlink, host DNS unchanged; `private-irvio-final-package-resources.md`. NativeTun correction separately passed 10,000 real TUN cycles |
| P10.10 | Affected public/discovery/lifecycle/security/UI regressions | `private-irvio-public-regressions-bbeb819.md`: 19 privileged cases; NativeTun fix affected race suites plus whole-tree vet/test; final UI analyze/131 tests. No skipped privileged test counted as PASS |
| P11, local package and handoff | Distinct clean-source preview, exact archive inventory, checksums, installation/removal with persistent profile preserved | `private-irvio-preview-f5f816b.md`: 567-file audit, installed byte equality, idle activation/Shutdown/WAN checks; final packaged resource result above; migration requirements and manual workflow in this report |

## Evidence preservation

Historical pre-reboot terminal results were re-read from `/var/tmp/irvio-private-evidence-before-reboot/netns-artifacts.tar.gz` on the mini-PC; archive SHA-256 `5bfe1fc2b05f20c4f0517269fab3aa79cbd79bdc86c8181056701d8c34e07fc1`. Selected results `14fdb048`, `bd70b0b4`, `2410079c`, `c6386ba4`, `ddc65c4c`, `67ec1f7a`, `a6830167`, `5f7e5782`, `8efe1ce6`, `13fe840e` each retain exit 0 and host integrity true. The archive also preserves failed attempts; no original checkpoint was rewritten. Root-only raw evidence/backups can contain identities and must not be committed or published.

## Security limits

- Access restriction is not anonymity: registration owners and DHT observers can observe routing metadata; an authorized Exit sees egress traffic.
- A copied invitation can reserve enrollment before its intended recipient, but cannot bypass explicit host approval. Revocation denies access, not knowledge of an old locator; rotation requires reinvitation.
- Root compromise and deliberate durable-state rollback by root are outside the guarantee.
- Scoped zero leak/anti-multihop counters support the exercised traffic, not a proof about every possible packet/application.
- Controlled activation/renewal interleavings and real network revocation complement each other; neither alone proves every possible scheduler interleaving.
- Existing registration cooldown can cause a roughly 136-second owner restart tail. Private mode never compensates by silently selecting a public Exit.
