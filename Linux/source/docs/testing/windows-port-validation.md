# Windows port — incremental validation

This is a chronological journal: the initial status below describes 2026-10-09,
not today's implementation. See [the current report](windows-port-report-2026-10-13.md)
and [acceptance map](windows-port-current-status.md) before interpreting historical
FAIL/open/not-implemented statements as current. Original evidence is retained.

Date: 2026-10-09. Branch: `windows-port`.
Starting HEAD: `44258c1710f776363d15bd2f506824e7f0ab4d8d`.

**The Windows port is not complete.** No Windows client, Exit, Private hosting,
headless server, or installer acceptance is claimed by these feasibility tests.
The full native daemon build still reports Linux-specific compiler errors.

## Lab

- Windows 11 Pro x64, build 26300.9457; 8 vCPU, 16 GiB RAM, 150 GiB virtual disk.
- KVM/Q35, UEFI Secure Boot and TPM 2.0; Defender and firewall left enabled.
- Go 1.27.1; Flutter 3.47.4 / Dart 3.13.3; Visual Studio 2022 C++ desktop tools.
- Windows SSH and QEMU Guest Agent give independent management paths.
- Linux mini-PC is the remote traffic oracle and Linux regression runner.
- The Ubuntu workstation is not a VPN test host; its proxy was not changed.
- Cold snapshots `clean-os` and `tools-ready` are outside Git, alongside private
  lab credentials. Neither credentials nor Windows media belong in the repository.

Local evidence root: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/`.
`w00-clean-pro/` preserves the first environment; `w00-hyperv-services/` records
the environment after adding Hyper-V Services and rebooting.

## W00 evidence

| Check | Result | Scope / limitation |
| --- | --- | --- |
| Native Go build/run | PASS | Toolchain smoke only |
| Flutter Windows release build/run | PASS | Standard counter app, not IRVIO UI |
| Guest NIC loss and recovery through QGA | PASS | Out-of-band management works |
| Pinned AWG dependency creates Wintun | PASS | Signed Wintun 0.14.1 amd64 |
| Wintun IPv4 UDP read/write | PASS | Kernel-to-userspace and reverse, MTU 1280 |
| Wintun adapter / test rule cleanup | PASS | Owned objects removed |
| Dynamic WFP filter after process kill | Removed | Unsuitable alone for crash-safe protection |
| Static WFP filter after process kill | Retained | BFE lifecycle not covered |
| Persistent WFP filter after process kill | Retained | New IPv4 TCP flows to lab oracle only |
| Persistent WFP filter after real reboot | Retained | Explicit removal restores oracle connectivity |
| WinNAT on clean Pro | UNAVAILABLE | `Get-NetNat` / `New-NetNat`: `Invalid class` |
| WinNAT after Hyper-V enablement + reboot | AVAILABLE | Optional OS component dependency |
| Native NAT through Wintun | PASS | Five synthetic IPv4 UDP source addresses; replies restored to each |
| Full IRVIO Windows daemon build | NOT YET | Remaining Unix/platform compiler seams |

The NAT probe injected packets from `10.253.240.2` through `.6` into Wintun,
using distinct source ports. The independent mini-PC echoed their payloads;
responses arrived back on Wintun with the original inner destinations/ports.
This is a real native UDP forwarding/NAT proof, **not** five encrypted IRVIO
sessions, TCP acceptance, lease enforcement, or anti-multihop acceptance.

WFP probes cover only new IPv4 TCP connections to a fixed lab destination.
Existing flows, UDP, IPv6, forwarded traffic, BFE restart, and complete M8
adoption remain untested. Persistent filtering after reboot means boot recovery
must deliberately remove previous-boot owned state; it cannot rely on OS cleanup.

Microsoft's documented NAT setup assumes Hyper-V and describes one internal NAT
prefix per host. Home edition, conflicts with WSL/Docker/other NAT users, and
CLIENT+EXIT physical-egress binding remain open gates, not silent fallbacks.
[Microsoft NAT setup](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/setup-nat-network).

The lab command `Enable-WindowsOptionalFeature -Online -FeatureName
Microsoft-Hyper-V-Services -All -NoRestart` enabled the broader Hyper-V feature
set on this image; a default virtual switch appeared after reboot. This is not
a minimal-dependency finding or authorization to make a production installer
silently enable Hyper-V. The exact feature inventory is saved with the evidence.

### Harness issues retained separately

- First Wintun attempt configured an address but did not wait for Windows DAD
  readiness. Waiting for `AddressState=Preferred` fixed the harness; retest PASS.
- The mini-PC SSH management namespace cannot reach its host's loopback DNS
  resolver. Linux Go gates ran with the existing offline module cache instead.
- A mini-PC user service ran outside the SSH management namespace and could not
  bind the oracle address. Direct SSH-owned oracle processes fixed that setup.
- First NAT run transferred all five payloads but failed during evidence
  collection: `Get-NetNatSession` has no `-NatName` parameter on this system.
  The corrected collector and complete rerun passed. Initial result preserved in
  `w00-nat-first-run/`.

## W01 — first platform-neutral extraction

`normalizePolicy` moved unchanged from `nft_linux.go` to `policy.go`; no marks,
validation limits, firewall rules, or controller semantics changed. New tests
exercise defaults, explicit values, invalid names, missing fields and mark
collisions.

- Windows: `go vet ./internal/killswitch` PASS.
- Windows: `go test -shuffle=on -count=10 ./internal/killswitch` PASS.
- Linux mini-PC: `go vet ./internal/killswitch` PASS.
- Linux mini-PC: `go test -race -shuffle=on -count=10 ./internal/killswitch` PASS.
- Windows race and full-tree gates: NOT RUN yet.

## W01/W02 — secure identity and publisher state

Added native Windows private-state operations in `internal/winfile`: protected
DACL at creation, trusted owner checks, no reparse traversal, hard-link
rejection, handle-relative file access and rename, write-through/flushing,
exclusive byte-range locks, and owned temporary cleanup.

`identity` and publisher incarnation now use platform files. Linux retains its
0600 checks, `renameat2(RENAME_NOREPLACE)`, `flock` and file/directory fsync.
Windows uses the native backend. Key serialization, PeerID derivation,
publisher magic and incarnation semantics are unchanged. Publisher size is now
read after ownership acquisition rather than before the lock.

Results on the Windows VM:

- Vet and ten shuffled repetitions of `winfile`, `identity`, `protocol`: PASS.
- Native `-gcflags=all=-d=checkptr=2` run: PASS.
- Three shuffled repetitions as `NT AUTHORITY\SYSTEM`: PASS.
- Sixteen concurrent identity creators: one creation, identical PeerID: PASS.
- Forced publisher process kill followed by reacquisition: incarnation 1 → 2:
  PASS. Concurrent second publisher rejected on Windows and Linux.
- Public/null/inherited ACL rejection, no silent ACL repair, corrupt-key
  preservation, reparse file/directory/ancestor and hard-link rejection: PASS.
- Atomic no-replace collision, replacement, retained private ACL, temporary
  cleanup: PASS.
- One hundred directory/file/lock cycles: handles 119 → 119 under IrvioLab;
  121 → 121 for each of three SYSTEM repetitions.
- Real separate standard-user token denied both direct file read and write
  against a SYSTEM-owned fixture: PASS. The temporary account was removed.

Linux mini-PC: affected vet and ten shuffled race repetitions for `identity`
and `protocol` PASS; native `go build ./cmd/irviod` PASS. No Linux workstation
tests or VPN sessions were started.

Evidence: `w01-storage/` under the external lab evidence root. Reproduction
notes: `tools/windows/w01/README.md`. An initial secondary-user process-launch
attempt failed with Windows error 87; the successful ACL test uses a real
non-admin logon token and direct access checks instead, not an assumed PASS.

A later shuffled run failed the initial process-wide handle check (120 → 126).
That result is preserved, not discarded. The test now disables GC while counting
and constrains the scheduler; finalizers cannot hide a missing explicit close.
Fifty repetitions of 100 cycles and the subsequent full affected shuffled run
PASS; every measured 100-cycle delta was zero. Between repetitions the overall
test-process baseline changed from 112 to 138, so this is not a claim of a flat
whole-process lifetime graph. Final logs are in `w01-storage-final/`. The process-wide counter alone does
not identify the six additional handles from the earlier run; it is not evidence
of a confirmed file-handle leak or a completed runtime resource-lifecycle gate.

This is not complete W02 acceptance. Abrupt host power loss, Windows profile
deletion/recovery of abandoned `.irvio-write-*` files, all remaining persistent
stores, and the Local API/service ownership contract remain to be implemented
or tested. Directories must already have a private ACL or be newly created by
the backend; existing broadly accessible directories are not silently adopted.

Remaining compiler seams include socketmux physical binding/truncation, profile
file identity, and discovery's control-socket dialer. These require real Windows
implementations, not no-op substitutes.

## W03 — native network snapshots and notifications

The Windows `networkstate` backend now reads native adapter/default-route state,
joins by LUID and interface index, uses route plus IP-interface metrics, and
preserves per-family IP MTU. Preferred source is deliberately not invented;
destination-specific source selection belongs to the pending underlay backend.
Linux keeps its netlink implementation and existing generation semantics.

Route/address/interface subscriptions share one process-wide callback trampoline.
Caller context is pinned until successful cancellation; cancellation does not
hold the callback mutex. If native cancellation fails, the error is returned and
the inert pinned context is retained rather than freed unsafely. This exceptional
retention is not claimed as successful resource cleanup. Initial-scan failure
now preserves subscription cleanup errors as well as the scan error.

Native development checks found two Windows-specific issues before acceptance:

- The pinned x/sys IP-interface row is 176 bytes; the installed SDK defines 168.
  Metric/MTU offsets agree (148/152), but array iteration with the Go stride loses
  rows. The implementation uses individual `GetIpInterfaceEntry` queries and
  reads only fields preceding the differing tail. Route row size is 104 in both.
  `netio-layout.c` and native test logs preserve this ABI comparison.
- An isolated IP MTU change left the notification-only snapshot stale for the
  full 12-second observation window. Native callbacks are now supplemented by
  five-second Windows-only reconciliation. Unchanged state and metric magnitudes
  alone do not advance NetworkGeneration. This is bounded eventual observation,
  not a promise of immediate notification for every Windows parameter.

Results on Windows 11 Pro:

- Native vet, ten shuffled package repetitions and checkptr=2: PASS.
- Three shuffled repetitions as SYSTEM: PASS.
- Effective metric ordering, IPv4/IPv6 decoding, DAD/address eligibility,
  LUID/index reuse, bounded inconsistent-table retries and cancellation: PASS.
- Repeated native subscribe/cancel: 100 measured cycles per repetition and zero
  retained registry contexts. Final shuffled admin batches had handle deltas
  0 or +2 (test tolerance +4); SYSTEM batches had zero delta. Earlier batches
  were also zero. Whole-process baselines vary between shuffled tests; the
  process-wide counter does not attribute those extra handles and is not a
  complete daemon lifetime resource proof.
- Three complete live dual-stack adapter scenarios as IrvioLab and one as
  SYSTEM: PASS. These cover default-route addition, metric-only no-migration,
  IPv4/IPv6 MTU, address/gateway changes, adapter rename and removal.
- Each live scenario returns to the exact normalized baseline. The test uses
  isolated Wintun state/high-metric routes, not a VPN connection.

Linux mini-PC: affected vet and ten shuffled race repetitions for `networkstate`,
`underlay`, `transportgen`, plus full native daemon build: PASS. No workstation
tests or VPN session were started, its default route is unchanged, and the Codex
proxy was not modified.

Evidence: `w03-networkstate/` under the external Windows lab evidence root.
Earlier failed native JSON runs are retained alongside successful repeats.
Reproduction: `tools/windows/w03/README.md`. The new backend does not yet prove
physical socket binding, route-to-own-IP rejection, fail-closed networking,
encrypted mixed-network traffic, or complete W03 readiness.

Native API contracts: [IP interface query](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getipinterfaceentry),
[notification cancellation](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-cancelmibchangenotify2).

## W03 — kernel path selection and socket pinning

Added `internal/winroute`, a Windows-only primitive using `GetBestRoute2`, source
ownership/DAD checks, LUID/index fencing, and native unicast-interface socket
options. IPv4 passes its interface index in network byte order; IPv6 uses native
byte order. The Windows getter and actual TCP/UDP traffic verify both families
on loopback. No Linux mark is silently ignored or declared equivalent to WFP.

`ResolveUnderlay` rejects own-host destinations and explicit excluded tunnel
LUIDs; it reports kernel-selected source, gateway, prefix, effective metric and
MTU. Callers must provide owned tunnel identities, retain the selected binding
for its generation, bind the local source, and install the appropriate WFP
policy. These operations are not yet wired into socketmux/production runtime.

Native VM results:

- Package vet, ten shuffled repetitions and checkptr=2 PASS; three SYSTEM
  repetitions PASS. The live-target gate was enabled, not skipped.
- IPv4/IPv6 bound TCP and UDP loopback PASS; stale LUID/index, wrong family,
  unowned source and own-address WAN rejection PASS.
- Real TCP HTTP from the Windows VM through the selected physical path to the
  mini-PC oracle PASS. This is a lab underlay target, not Internet egress proof.
- Installed a more-specific `/32` route to the oracle through an owned blackhole
  Wintun adapter. Ordinary TCP/UDP failed and their packets were captured at TUN;
  pinned TCP/UDP remained usable with zero corresponding TUN packets.
- Disabled the Windows physical NIC using independent QEMU guest-agent control.
  New pinned TCP/UDP failed before use; a UDP socket opened before the fault
  failed with WSAEADDRNOTAVAIL rather than sending through TUN. Zero test packets
  from pinned requests were observed there. Re-enable restored the same held
  UDP socket and new TCP/UDP operations. Two complete held-socket runs PASS.
- Owned adapter and `/32` route removed; ordinary oracle access restored; probe
  process exited. The workstation network/proxy was not modified.

SDK oracle and Go layout checks agree for `MIB_IF_ROW2` (1352 bytes, OperStatus
offset 1156) and `MIB_UNICASTIPADDRESS_ROW` (80 bytes, DadState offset 64) on x64.
No dependency upgrade or custom driver was introduced.

Evidence: `w03-route/` under the external lab evidence root, including raw JSON
steps, TUN packet counts, native unit logs and SDK layouts. Reproduction is in
`tools/windows/w03/README.md`; run NIC-disabling scenarios only via guest agent.
IPv6 physical-network pinning, socketmux integration, control-socket policy
translation and full user-traffic fail-closed protection remain unproven. A
successful underlay socket primitive does not close those gates.

Native contracts: [GetBestRoute2](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getbestroute2),
[IPv4 interface option](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ip-socket-options),
[IPv6 interface option](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ipv6-socket-options).

## W03 — production UDP socketmux on Windows

Added Windows UDP bind/truncation handling and per-socket source/interface
pinning. Shared SetMark transaction/rollback stays common; Linux still uses the
same SO_MARK values. `internal/socketpolicy` names those existing values without
changing their bytes. Windows accepts the physical underlay intent only for a
concrete source, captures its LUID/index before exposing readers, and rejects
unknown or tunnel-control tags. Clearing a tag does not remove its interface
fence. This is routing policy, not a claim that WFP authorization is installed.

Windows uses SO_EXCLUSIVEADDRUSE before bind, explicit IPv6-only semantics, and
treats WSAEMSGSIZE as an oversized UDP drop rather than a reader failure. Common
classification, queue bounds, physical port ownership and ICE/AWG semantics are
unchanged. The regression checks that a truncated prefix is never delivered and
the following valid datagram still arrives without a read-error increment.

Validated in the native VM:

- Socketmux, AWG and transport-generation vet, ten shuffled repetitions and
  checkptr=2 passed on the final source; no tests were skipped in that run.
- Real loopback Pion/AWG exchanges pass for IPv4, IPv6 and dual-stack; three
  encrypted peers share the Exit transport, and one peer can restart without
  disturbing the others. These suites use ChannelTUN, not native Wintun.
- Native interface getter matches the source owner; wildcard underlay and
  unsupported tags reject. SO_REUSEADDR cannot steal the bound mux endpoint;
  closing releases the port for a new mux.
- Two complete SYSTEM route/NIC experiments passed with production socketmux:
  a more-specific TUN route does not capture new/held mux traffic; physical NIC
  loss fails without sending oracle traffic into TUN; restoration recovers both.
  Ordinary traffic supplies the negative control and reaches TUN when diverted.
  Both runs remove the owned adapter/route and restore ordinary oracle access.
- SYSTEM three-repeat suites passed after isolating the resource measurement.
  Their 100-cycle dual-stack batches measured 138→140, 171→171 and 181→181 handles.
  The first, unisolated experiment failed its bound (157→167), then measured
  171→171 twice. It is retained, not erased or called a proven product leak.
  The final test controls Go scheduling/GC and permits four runtime handles;
  process-wide zero growth and full VPN lifecycle resources remain unproven.
  The final administrator run measured zero within-batch growth in each of ten
  100-cycle batches; process baseline still varied between batches (160–180).
- Mini-PC Linux race/shuffle ten-repeat socketmux/AWG/transportgen/linuxroute,
  affected vet and full daemon build passed. No tests or VPN were run on the
  Ubuntu workstation; its proxy and network settings were not modified.

Evidence: external lab `evidence/w03-socketmux/`, including both route JSON files,
initial and repeated SYSTEM logs. One final-gate invocation used SYSTEM without
the configured module-cache path and failed vet with GOPROXY=off; this is a
harness-environment error, with its log retained. No production workaround was
added for it. Reproduction is in `tools/windows/w03/README.md`.

The full native daemon build still fails at `productprofile`'s `syscall.Stat_t`
and discovery's missing Windows `controlsocket.DialerForMark`. These are the next
platform boundaries, not hidden successes; socketmux's prior compiler failures
are gone. No release binary or installer readiness is claimed.

Native contracts: [exclusive socket binding](https://learn.microsoft.com/en-us/windows/win32/winsock/using-so-reuseaddr-and-so-exclusiveaddruse),
[UDP truncation](https://learn.microsoft.com/en-us/windows/win32/api/winsock2/nf-winsock2-wsarecvfrom).

## W02 — Windows profile ownership and deletion

Split the Linux profile implementation from shared marker/error/filename rules;
Linux code and tests retain their behavior. The Windows implementation uses
`winfile` protected directories, volume plus 128-bit File ID, handle-based bounded
enumeration and exclusive handles for deletion. It checks every file before
deleting any and removes the ownership marker last. No broad recursive removal,
path-following deletion, silent ACL repair, or new profile format was introduced.

Native Windows results:

- Vet, ten shuffled repetitions and checkptr=2 passed for winfile, productprofile,
  identity and protocol. Three SYSTEM repetitions also passed.
- Fresh profile, preserved marker bytes, recognizable private legacy state,
  deletion preserving the directory, and subsequent reopen passed.
- Replaced directory, missing/invalid/oversized marker, unrecognized file,
  malformed temporary filename, directory child, symlink, hardlink, read-only
  file, inherited/permissive ACL, and busy database refusal passed. Refusals
  preserve the marker and all other sample state; external link targets remain
  unchanged. Existing reparse-ancestor tests also pass.
- Bounded/repeated enumeration and exclusive-handle rename/write rejection
  passed. The first run exposed a prior Windows atomic-write defect for a
  one-character filename: the NT rename buffer length was below the structure's
  minimum despite sufficient allocation. Corrected the supplied length; tests
  cover one-, two- and three-character names. Initial failure log is retained.
- A real temporary non-admin logon token was denied native read, write and DELETE
  access to a SYSTEM-owned private fixture. The temporary account was removed.
  The first harness invocation referenced an already-cleaned older fixture;
  recreating a new W02 fixture resolved that lab prerequisite without a product
  change. Ordinary unit runs alone do not exercise this credential-driven test.
- Each stress iteration refuses deletion while the database is held open, then
  succeeds after closure. Ten 100-cycle administrator batches changed native
  handle count by 0–1 within a batch; three SYSTEM batches measured 122→122,
  128→128 and 134→134. Warm-up, controlled GC/scheduling and tolerance four apply;
  varying process baselines do not prove global zero growth or VPN resource
  stability.
- Mini-PC Linux affected vet, ten race/shuffle repetitions for productprofile,
  privateaccess, productbootstrap, identity and protocol, plus full daemon build
  passed. No workstation VPN/test run or proxy change occurred.

Evidence: external lab `evidence/w02-profile/`; reproduction is in
`tools/windows/w02/README.md`. Native daemon compiler inventory no longer reports
`productprofile/syscall.Stat_t`; the next reported blocker is Windows discovery
TCP policy (`controlsocket.DialerForMark`).

This completes a profile primitive, not all W02: remaining persistent writers,
Named Pipe authorization, installer provisioning, service lifecycle, UI reset
and power-loss recovery require their own acceptance. The Windows path refuses
inherited/unprotected state rather than silently weakening the checks.

Native contracts: [file identity](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_id_info),
[handle-based removal](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_disposition_info).

## W03 — native control TCP and discovery

Added an explicit Windows TCP dialer factory backed by the previously tested
source/LUID/index binding. It resolves the runtime-owned binding on every dial,
checks the exact literal endpoint and address family, and propagates binding
failures without an ordinary-route fallback. DNS/relay addresses must not bypass
this check. Mark-only Windows configuration is rejected, not silently ignored.

Discovery now accepts a platform dialer, mutually exclusive with Linux
`ControlMark`. Linux mark handling remains unchanged. No DHT, ServiceCID, Private
locator, M11 quorum, ICE or wire-protocol semantics changed.

Results:

- Windows IPv4/IPv6 TCP echo and native socket-interface checks passed; binding
  resolution runs on each dial. Unsupported endpoints, argument substitution,
  cancellation and resolver failure are rejected.
- Two real Windows discovery nodes authenticated bootstrap and completed a DHT
  Ping through the explicit factory. A third node with a failing binding resolver
  could not bootstrap through the same reachable introducer.
- Native vet, ten shuffled repetitions and checkptr=2 passed for controlsocket
  and discovery; three SYSTEM repetitions passed. Existing Private and Global
  provider/recovery tests are included, not just the new TCP test.
- The SYSTEM route/NIC fault harness passed using the production TCP factory:
  a more-specific blackhole TUN route did not divert pinned TCP; physical NIC
  loss failed without TUN fallback; restoration recovered TCP. UDP/socketmux
  controls also passed, and owned adapter/route cleanup restored connectivity.
- Mini-PC Linux vet, ten race/shuffle repetitions and full daemon build passed.
  No workstation tests, VPN session or proxy changes occurred.

The first repeated Windows runs exposed a test-clock assumption, not a lost
republication: the forced-expired timestamp was replaced with the current time,
which equalled the original timestamp within one Windows clock tick. Diagnostic
failures are retained. The assertion now verifies advancement from the expired
state and freshness without demanding a clock tick; twenty focused repetitions
and the full repeated gates passed. Production reprovide logic was not changed.
Discovery fixtures use a newly created private child directory instead of
attempting to adopt the test framework's inherited-ACL temporary root.

Evidence: external lab `evidence/w03-tcp/`; reproduction in
`tools/windows/w03/README.md`. Full native daemon compilation still fails in
the remaining `productbootstrap` POSIX storage and Linux-only runtime composition
types. This is not a complete Windows daemon or VPN acceptance. Runtime binding
selection, WFP authorization, inbound listener policy, underlay DNS, M11 fresh
dialers and tunnel-only session control remain separate gates.

## W02 — Windows bootstrap persistence

Moved POSIX bootstrap storage and its POSIX permission test to Linux-specific
files without changing their behavior. Windows storage uses the existing native
private-directory/file checks, bounded reads and flushed atomic replacement.
It does not repair unsafe ACLs, follow reparse points, or change bundle format.
The shared manager, importer, merge/deduplication and discovery logic are intact.

Native Windows results:

- Vet, ten shuffled repetitions and checkptr=2 passed; three SYSTEM repetitions
  passed. Checkptr also covered the productprofile helper used by the new test.
- Missing store does not create a directory. Empty/corrupt/oversized stores are
  rejected and the manager reports degraded storage; valid reimport repairs an
  owned corrupt payload. Invalid replacement preserves the prior valid bundle.
- Public file/directory ACLs, inherited directory ACLs, symlinks, hardlinks,
  directory substitution and reparse-directory paths are refused. Read-only and
  occupied targets reject replacement without losing the original contents;
  occupied-target failure removes its temporary file, and retry after close
  succeeds. Unicode profile paths work.
- Idle import remains idle; hot import authenticates a real local discovery
  node. Two imported three-peer bundles retain all six peers and twelve IPv4/
  IPv6 endpoints across restart. This is a persistence proof, not a new claim
  about multi-bootstrap outage survivability.
- Ten 100-cycle import/profile-delete batches each had zero within-batch handle
  growth; baselines varied 126–203 as other tests ran. Three SYSTEM batches
  measured 134→134, 181→181 and 194→194. Controlled GC/scheduling, warm-up and a
  four-handle tolerance apply; process-wide zero growth is not established.
- Mini-PC Linux vet, ten race/shuffle repetitions and full daemon build passed.

The first native run exposed two incorrect new test expectations: a valid idle
bundle still reports DEGRADED/NOT_READY until peers connect, and replacing an
occupied file returned Windows ACCESS_DENIED rather than SHARING_VIOLATION.
The tests now check cleared storage error plus reread contents, and both native
refusal codes followed by successful retry. Initial logs are preserved; no
production manager behavior was changed to satisfy those expectations.

Evidence: external lab `evidence/w02-bootstrap/`; reproduction in
`tools/windows/w02/README.md`. Compiler inventory no longer reports the bootstrap
`syscall.Stat_t` errors; Linux-only runtime types remain unresolved. No full
daemon, UI, power-loss durability or installer acceptance is claimed.

## W02/W07 — private state and shared admission/enrollment

Extracted the existing strict JSON parser, state transitions, admission policy,
invitation mutations and inner enrollment TLS from Linux-only files into shared
code. Their algorithms, wire identifiers and schema are unchanged. Linux retains
its openat/flock/rename/fsync backend and its POSIX permission tests. Windows adds
a pinned private directory, protected state and lock files, native exclusive
writer ownership, bounded reading and flushed handle-relative atomic writes.

Native Windows results:

- Vet and ten shuffled repetitions passed for privateaccess, sessioncontrol and
  tunnellease; privateaccess checkptr=2 and three SYSTEM repetitions passed.
- The shared suites verify durable grants/revocation, single-use invitation
  races, automatic and legacy approval, expiry/capacity, strict malformed-state
  rejection, revision fencing, immutable snapshots, enrollment TLS identity
  binding and secret protection, persisted imports, cancellation, and revocation
  during in-flight activation/Noise renewal. These use local test connections,
  not a complete Windows VPN or a Windows tunnel-bound direct-control socket.
- Concurrent store ownership is refused; a real child process held the native
  writer lock, persisted an access invitation, and was forcibly terminated.
  Reopening retained the acknowledged invitation and prior authorization rather
  than resetting state. This is a storage crash check, not SCM/M8 adoption.
- Unsafe ACLs, inherited directory ACLs, reparse directories, state/lock symlinks,
  hardlinks and directory substitution are refused without overwriting evidence.
  Wrong host identity is rejected; closing or failed Open releases ownership.
- Forced replacement failure preserves the prior file, removes temporary write
  residue and poisons the store. Subsequent policy admission is refused and its
  cached grant context is cancelled. Active profile deletion is refused; after
  Close all recognized private state can be deleted with no residue.
- Ten 100-cycle open/mutate/refuse-delete/close/delete batches measured 0–1
  within-batch handle growth; baselines settled from 154 to 175. SYSTEM batches
  measured 153→153, 175→175 and 183→184. Warm-up, controlled GC/scheduling and the
  four-handle tolerance apply; these do not prove whole-process zero growth.
- Mini-PC Linux privateaccess vet, ten race/shuffle repetitions and full daemon
  build passed. Related privateaccess/sessioncontrol/tunnellease/rendezvous vet,
  three race/shuffle repetitions and another full daemon build also passed.

Evidence: external lab `evidence/w02-private/`; reproduction in
`tools/windows/w02/README.md`. The native compiler inventory no longer reports
missing `privateaccess.Store`; remaining failures are Linux-only runtime types
and backends. Windows protected traffic, mixed-OS Private sessions, real UI reset
and full service lifecycle remain open acceptance gates. Workstation networking
and its proxy were not modified; no workstation test/VPN run occurred.

## W03/W07 — fresh M11 sockets and owner-routed enrollment

M11's FreshDialer and FamilyRequester are now shared, with explicit TCP-factory
constructors for Windows native underlay bindings. Each operation retains a
fresh non-listening authenticated host, exact PeerID/nonce checks and per-family
request selection. Existing Linux marked constructors keep their behavior.
Quorum, attestation schema, protocol IDs and observed-IP rules did not change.

The libp2p underlay challenge and PrivateEnrollmentDialer's existing owner lookup
algorithm were made platform-neutral. Their wire formats and route validation
are unchanged. Private enrollment and distributed-owner test fixtures now create
protected child profiles instead of adopting inherited temporary-directory ACLs.

Results:

- Windows vet, ten shuffled repetitions and checkptr=2 passed for reachability,
  underlay and rendezvous. Three SYSTEM repetitions also passed.
- Three real verifier identities issued valid attestations through production
  FreshDialer/FamilyRequester and native Windows TCP bindings for IPv4 and IPv6.
  Repeated acquisition used fresh dials despite already-open IPv4 connections.
  One verifier or a duplicated verifier did not satisfy the unchanged quorum.
- Request-side and fresh-challenge-side binding failure could not fall back to
  an existing unbound connection or produce an attestation. This is a loopback
  transport proof, not public-network readiness, NAT reachability or WFP proof.
- The existing M11 tests also passed: family isolation, identity/nonce/replay,
  proof expiry, mixed-generation refusal, insufficient quorum, dead/replaced
  verifier behavior and production observed-address restrictions.
- Rendezvous's native suite includes registered-owner Private enrollment with
  authenticated inner TLS, owner discovery, persisted approved target, incorrect
  locator/stale route refusal and bounded enrollment carrier/deadline behavior.
  Distributed-owner/addressless and other existing registry/discovery tests ran;
  this does not establish a complete Windows Private data-plane session.
- Mini-PC Linux affected vet, three race/shuffle repetitions for all three
  packages and full daemon build passed. Workstation networking/proxy unchanged.

The initial native run could not set up Rendezvous tests because the offline
module cache lacked pinned mmdbwriter; its netipx dependency was also missing.
Those exact modules were transferred from the mini-PC cache and validated by Go
against the existing module metadata. No dependency version changed. Initial,
vet-cache and SYSTEM-cache failure logs are retained separately from successful
gates in external lab `evidence/w03-m11/`.

Reproduction: `tools/windows/w03/README.md`. Full native daemon compilation still
fails on Linux-only runtime composition and backends. Raw marked underlay probe,
WFP authorization, listeners, tunnel-only session control, production resolver
wiring, resource lifecycle and complete Windows server readiness remain open.

## W04 — native TUN ownership and AWG generation lifetime

The Windows DeviceOwner now owns one Wintun adapter and ring for the logical
tunnel lifetime. Generation-local borrowed devices have independent cancellation
handles; Close stops reads and joins admitted I/O without ending the owner ring.
Final owner Close releases the borrower, ring and adapter in that order. No AWG,
ICE, TunnelLease, generation or wire semantics changed; default MTU remains 1280.
Linux configuration validation was extracted unchanged into shared code.

Native IP Helper APIs install IPv4/IPv6 MTU and addresses and wait boundedly for
DAD. Dynamic host-address changes retain ownership on rollback failure. Existing
adapter names are refused. The SDK-compatible 168-byte IP-interface row avoids
round-tripping the pinned x/sys row's incorrect trailing offload-field layout.

Verified so far:

- Windows vet and ten shuffled unit repetitions passed, including partial
  startup failure, exclusive borrower ownership, cancellation/close races,
  close during an admitted write, dynamic-address rollback and layout checks.
- Native IPv4/IPv6 UDP round trips passed across three borrowed generations;
  closing each borrower preserved LUID/index, preferred addresses and MTU.
  Dynamic address deletion and final adapter removal passed.
- Real production AWG/socketmux passed encrypted IPv4/IPv6 replies through the
  native Wintun client over three device/key/mux generations. The remote fixture
  is a channel TUN and endpoints are preselected, not full ICE/discovery proof.
  AWG Close returned boundedly without destroying the long-lived adapter.
- Three initial 100-borrower batches showed unchanged handle counts within
  each batch: 245→245, 261→261, 273→273. Independent adapter repetition measured
  217→221, 223→225, 225→225 over three ten-cycle batches, with OS threads settling
  at ten. These bounded measurements are not a process-wide zero-growth claim.
- Linux mini-PC vet, ten race/shuffle repetitions for tunnel/AWG/transportgen,
  and full daemon build passed. No tests or VPN were run on the workstation.
- SYSTEM vet, ten shuffled unit repetitions and three complete checkptr=2
  repetitions passed, including real Wintun/AWG IPv4/IPv6 generation replacement.
  Native borrower batches were 223→223, 272→272 and 278→278 handles. Ten-adapter
  batches were 203→207, 262→264 and 270→270. Within each measured adapter batch,
  sampled File/Section handle counts stayed fixed; Thread/Event changes explain
  part of the process-wide warm-up. These are bounded lab observations, not a
  claim that every retained runtime/library handle has been attributed.
- Final native default-route/DNS snapshots matched baseline and no owned test
  adapter remained. Tests did not require changing the Windows host firewall.

Initial failures are preserved: a one-read cancellation oracle received valid
Windows background traffic instead of cancellation; it now reads until closed.
The first AWG fixture double-closed its non-idempotent upstream channel TUN;
AWG alone now owns that fixture. An expanded adapter batch exceeded the four-
handle tolerance by one (271→276); subsequent resource diagnostics were added
instead of relaxing the threshold. Independent repeated and SYSTEM gates passed
with the original four-handle tolerance. The early failure remains an explicit
measurement observation. Production networking/proxy on the workstation was
not changed.

Reproduction: `tools/windows/w04/README.md`; external evidence in
`IRVIO_WINDOWS_LAB/evidence/w04-tun/`. Native Wintun is not yet wired into full
daemon composition. Full route/DNS policy, WFP, Exit forwarding, leases, crash
adoption and real Global/Private sessions remain open gates.

## W03/W06 — dual-role routing exposes an unsafe native combination

The single-role W00 NAT proof was insufficient for CLIENT+EXIT. A new bounded
probe uses the production Wintun owner with one shared adapter, separate own
client/Exit addresses, and five synthetic downstream IPv4 UDP sources. A more
specific oracle route models the own-upstream tunnel. The mini-PC is the
independent physical-egress echo oracle; matching packets read at Wintun show
the wrong egress rather than merely a timeout.

Baseline production code: `ee1efed`. All rows below completed with cleanup;
these are feasibility observations, **not** Windows product acceptance.

| Forwarding configuration | WinNAT | Bound physical UDP / mux under upstream route | Downstream result |
| --- | --- | --- | --- |
| Neither interface | Off | PASS, no TUN capture | Not exercised |
| Physical only | Off | FAIL, packets captured at TUN | Not exercised |
| TUN only | Off | PASS, no TUN capture | Not exercised |
| Physical + TUN | On | FAIL, packets captured at TUN | 5/5 entered upstream TUN |
| TUN only | On | PASS, no TUN capture | 5/5 entered upstream TUN |
| Neither interface | On | PASS, no TUN capture | No verified replies; no matching upstream UDP captures |

The same failure occurred for connected UDP, re-applying the interface option
after connect, unconnected UDP, and the production socketmux. Winsock still
reported the physical index (6), while packets were captured on the owned TUN.
The physical-only/no-NAT case isolates the relevant trigger to forwarding on
the **bound physical interface**, not NAT or Go's connected-UDP API. Recorded
`WeakHostSend` and `WeakHostReceive` remained disabled. Enabling only TUN
forwarding retains underlay pinning but does not fix downstream anti-multihop.

For the NAT cases, all five baseline replies arrived correctly before the
upstream route and all five returned after its removal. This distinguishes the
wrong route from an unavailable oracle. An on-link next-hop variant reproduced
the same five downstream misroutes. Trying explicit external `/32` prefix
with `New-NetNat` failed with Windows error 87; no success is inferred from that
unsupported configuration. The first aborted probe and this setup failure are
preserved separately from completed experiments.

### Defensive correction, not an Exit backend

`winroute.Binding` now validates the native IP-interface identity/readiness and
requires forwarding and weak-host send disabled on that bound interface. An
unsafe configuration gets an actionable error instead of a successful-looking
socket binding. The code does not change another application's network settings.
Both IPv4 and IPv6 get conservative validation; the negative live experiment
above covers IPv4 only.

Native regression results:

- Vet of winroute/controlsocket/socketmux and the probe PASS.
- Ten shuffled repetitions with `checkptr=2` of those three packages PASS.
- Five guarded live matrix runs PASS: unsafe physical forwarding rejected,
  normal/TUN-only physical sockets still work, zero underlay TUN captures.
- Guarded NAT cases still reproduce downstream misrouting: these remain FAIL
  for anti-multihop, not a result repaired by the socket guard.
- Three shuffled `checkptr=2` repetitions of winroute/discovery/reachability PASS,
  including the live mini-PC HTTP target and native M11 transports.
- Rebuilt W03 route-diversion/physical-NIC-loss regression PASS, including held
  UDP/mux sockets, recovery and cleanup with forwarding disabled.
- Linux daemon build on the mini-PC PASS; production changes are Windows-only.

The guard checks at binding creation/use, not every packet. Already-open sockets
can outlive a routing-policy change, so this does **not** replace WFP enforcement,
NetworkGeneration invalidation, crash protection, or held-flow fault testing.
Do not claim the W03 single-role proof covers physical forwarding.

Decision: **do not compose the simple shared-TUN/WinNAT/global-route design into
the production Windows runtime.** Do not solve this with destination-wide WAN
bypass routes, automatic Hyper-V enablement, weakened anti-multihop, or a
client-only completion claim. The Exit backend remains an explicit open design
gate; isolated routing/native alternatives need new feasibility proof before
integration. WFP drop rules alone could block wrong traffic, but would not prove
usable downstream forwarding.

Reproduction: `tools/windows/w06/README.md`. Evidence: external
`IRVIO_WINDOWS_LAB/evidence/w06-natdual/`, including pre/post guard binaries,
hashes, JSON matrices, native logs and original failures. Workstation networking
and proxy were not modified.

Native API references: [IP_UNICAST_IF](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ip-socket-options),
[IP-interface forwarding and host modes](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ns-netioapi-mib_ipinterface_row).
The misrouting conclusion is from the experiments above, not a claim that those
documents promise identical behavior in every forwarding configuration.

## W06 — explicit forward-injection feasibility, not production acceptance

Baseline: `fc7a4db`. The new lab-only probe uses official WinDivert 2.2.2-A
with WinNAT absent and physical/TUN forwarding disabled. Secure Boot, Defender
and Windows Firewall stayed enabled. The SYS signature validates; the unsigned
vendor DLL was checked against the frozen official archive, not described as
Authenticode-signed. No production dependency or Core code was added.

Three completed repetitions each proved:

- The ordinary host socket follows the deliberately installed upstream TUN route.
- Existing physical-bound UDP/mux and explicit forward injection retain physical WAN.
- Five synthetic UDP clients with the same inner port receive correct per-flow replies.
- Packet-level TCP translation preserves sequence/payload and receives a real HTTP
  response from the mini-PC; its intentional empty-directory response is HTTP 404.
- An invalid interface produces no physical success or TUN fallback; explicitly
  selecting the TUN captures all five packets there and produces no physical replies.
- Temporary adapter, route, firewall rule and driver are removed afterward.

### Native resource lifetime and a separate lab limit

An initial 100-cycle prototype completed traffic but leaked native handles
(242 to 425). Repeated DLL unloading lost other Go threads' cached native TLS
events. The lab fix retains the DLL for the process lifetime and uses terminal
locked OS threads for synchronous receive workers. This finding matters for a
future native wrapper; it is not an existing IRVIO production resource defect.

Subsequent UDP losses had a separate cause. QEMU's actual soft `NOFILE` limit was
1024 despite a hard limit of 1048576; its FD count reached 1021. Guest-NIC capture
showed all five requests leaving Windows while only one reached the mini-PC.
SLIRP allocates a host socket per distinct guest tuple. Raising only this QEMU
process's soft limit to 65536 removed the failure with the same traffic,
deadlines, resource thresholds and no extra retries. The lab startup script now
sets that soft limit; workstation routes and proxy were not changed.

Final native vet and ten shuffled `checkptr=2` unit repetitions PASS. The live
series completed three independent 100-cycle runs, five UDP flows per cycle:

| Run | Cycles | Handles baseline / last / sampled max | Goroutines baseline / last | Go heap baseline / last |
| --- | ---: | --- | --- | --- |
| 1 | 100 | 251 / 243 / 257 | 2 / 2 | 328216 / 341232 B |
| 2 | 100 | 252 / 244 / 259 | 2 / 2 | 327752 / 342208 B |
| 3 | 100 | 251 / 251 / 258 | 2 / 2 | 328040 / 346432 B |

No monotonic handle growth remains in this bounded probe. These are NAT-probe
owner lifetimes, **not 300 full VPN connects**, encrypted downstream sessions,
throughput measurements, Safety Policy or fail-closed acceptance. The lab still
has Hyper-V installed; clean Home/Pro without it remains a separate gate.

The first cleanup-script error (service already removed, `sc` error 1060), DLL
handle leak, QEMU-limited failures and successful rerun are preserved separately
under external `IRVIO_WINDOWS_LAB/evidence/w06-forward-injection/`. Final driver
count is zero and the oracle firewall rule is absent. See
`docs/product/windows-exit-backend-feasibility.md` for integration risks, notably
mandatory authorization/Safety checks before WFP forward injection.

## W05 — native persistent WFP object store

Baseline: `b9392fa`. `internal/wfp` adds an isolated native object store, not a
complete VPN policy and not a replacement for the existing Linux kill switch.
Provider/sublayer/filter ownership, persistent lifetime, service association,
administrator/SYSTEM DACLs and atomic replacement use Windows APIs directly.
The initial rule subset covers IPv4/IPv6 ALE connect and outbound transport,
remote prefix, TCP/UDP port and protocol. Actual native conditions are read back;
unsupported conditions, disabled filters and priority reassignment are refused.

Final VM gates: native vet PASS; ten shuffled `checkptr=2` unit repetitions
PASS; temporary-user ACL check PASS; three shuffled complete native repetitions
PASS (including another 300 replacements). This is not a native race gate.

An independent Windows SDK C++ oracle verified struct sizes, offsets and GUIDs.
Native tests cover rollback after a partially added colliding ruleset, cancelled
commit, foreign ownership refusal, idempotent cleanup, 130-filter pagination,
IPv4/IPv6 layers, sublayer-priority collision and aggressive Go GC.

The independent mini-PC TCP/UDP oracle proved baseline traffic, blocked new and
already-open flows, retained blocking after a real installer-process kill, and
traffic restoration after explicit removal. A temporary real non-administrator
identity could not remove the installed policy. A **real Windows VM reboot**
changed the boot timestamp; all four scoped persistent filters remained active,
the oracle was blocked, and explicit cleanup restored traffic. These tests never
installed a machine-wide kill switch or altered workstation routes/proxy.

### Initial findings and resource qualification

The first implementation incorrectly supplied a provider enumeration template
with an unspecified layer; Windows rejected it. Enumeration now uses the native
all-filter snapshot inside a transaction, then checks exact owner keys/markers,
with bounded pages and counts. Windows automatically adds `INDEXED` to some
filters and can reassign a colliding sublayer weight. Native readback accepts
only the known non-semantic indexed bit; priority changes abort replacement.
Unused union bytes are not interpreted as active block/permit policy fields.
Failed experiments and ownership-checked cleanup remain in external evidence.

The original four-total-handle tolerance failed intermittently, including
173→179 and 176→182. A five-batch diagnostic showed flat within-batch handles and
six-handle jumps comprising Thread +1, Event +2, IRTimer +2 and
WaitCompletionPacket +1. The installed Go 1.27.1 `runtime/os_windows.go`
(`semacreate`, `minit`, `mdestroy`) accounts for these per-M allocations.

The revised resource oracle samples every ten replacements, bounds thread
growth to two and unexplained positive per-type growth to four; synthetic
Event/File/thread-leak negative tests protect that distinction. Five further
100-replacement batches passed, with raw baseline→last totals 173→179,
173→180, 180→182, 176→182 and 176→180. File/Key/ETW counts remained unchanged;
goroutines remained two. Raw totals are **not** claimed constant or zero-growth.
Type scanning is bounded and non-atomic against runtime thread changes, so this
is limited lifecycle evidence, not a universal resource-leak proof or VPN soak.

Reproduction: `tools/windows/w05/README.md`. Policy management uses terminal
locked native threads to release thread-local RPC resources; no per-packet path
uses this facility. Context checks and transaction rollback are implemented,
but a hung Windows RPC/BFE service is not claimed to have a hard response bound.

Still open: complete interface/application-scoped policy, real IPv6/forwarded
traffic enforcement, BFE/Fast Startup behavior, route/DNS integration, and the
product crash/adoption/boot lifecycle. Persisting rules over reboot is only a
primitive proof: **new boot → owned cleanup → IDLE** remains a separate gate.
The dummy auto-start owner service is not the product recovery service.

Final independent `netsh wfp` inventory contains no test owner markers; native
readback is absent. The temporary service and user were removed, and the mini-PC
echo process/listeners stopped. Secure Boot, Defender and Windows Firewall
remain enabled. Evidence, including earlier failures, binaries, reboot results,
raw/normalized resource logs and cleanup inventory, is stored outside Git in
`IRVIO_WINDOWS_LAB/evidence/w05-wfp/` with a SHA-256 manifest.

## W05 — application/interface/socket restrictions

On baseline `a8ba567`, WFP rules now support a native application path identity,
interface LUID, exact local IPv4/IPv6 address and local TCP/UDP port. Native
readback verifies those conditions; unsupported App ID conditions at transport
layers are rejected. Canonicalization uses `FwpmGetAppIdFromFileName0`, not a
hand-written path transform. The identity is **not** a binary signature/hash:
protecting executable paths with installer ACLs remains mandatory.

The new scoped mini-PC oracle has two executables with identical bytes but
different native application identities. A bounded policy to only its TCP/UDP
ports permits one executable and blocks the other. The first live experiment
also blocked already-open foreign TCP/UDP flows while keeping transport-layer
permits independent of local port, demonstrating ALE reauthorization in this
tested case rather than inferring it from new connections alone.

An expanded experiment adds local-source and UDP local-port restrictions:
baseline both executables PASS, foreign held/new flows blocked, selected
executable PASS, wrong local address/port/LUID blocked, reconciliation restored
the intended executable, removal restored the other. The expanded experiment
passed once plus three further repetitions. No workstation VPN or proxy state
was changed. These eight scoped rules are **not** a machine-wide kill switch.

Native vet and ten shuffled `checkptr=2` unit repetitions passed. Three native
IPv4/IPv6 round-trip repetitions passed with all new conditions, including real
Windows canonical application IDs. This is IPv6 policy-storage evidence, not an
IPv6 traffic-enforcement claim. The Windows SDK oracle independently checked the
additional condition GUIDs.

One full native regression repetition exceeded the resource oracle's thread
allowance at cycle 90: Thread 11→14, total handles 176→190. The following batch
returned to 12 threads / 178 handles and remained flat at the sampled cycles.
Functional native tests passed. Live scope tests were running concurrently with
that resource suite; causation is not established. The failure is preserved,
and resource limits are not relaxed for this change.

Repeating all native tests three times with the same recorded shuffle seed,
without parallel live probes, passed with unchanged code and limits. The first
failure remains an explicit resource observation, not a confirmed WFP leak or
a claim that concurrency caused it. Final native and independent `netsh wfp`
inspection found no fixture objects; the owner service and mini-PC oracle were
stopped/removed. Defender, Windows Firewall and Secure Boot stayed enabled.
Both live experiments, repeats, the failed/passing native runs and cleanup are
preserved under `IRVIO_WINDOWS_LAB/evidence/w05-scoped-policy/`, with SHA-256
verification. No Linux production files or wire protocols changed.

Reproduction: `tools/windows/w05/scopeprobe` and the W05 README. The production
ProtectedMode policy compiler, dynamic underlay grant ownership/revocation,
route/DNS integration and full daemon composition still require implementation.

## W05 — persistent ProtectedMode backend and privileged grants

On baseline `1b498d7`, `internal/killswitch/wfp_windows.go` implements the existing
Controller backend with a deterministic IPv4/IPv6 ALE/transport policy compiler.
It installs default blocks, loopback permits, canonical Safety Policy v0,
explicit Wintun LUID permissions, DHCP/neighbor maintenance and bounded
application/interface/source/endpoint control grants. Native readback verifies
the required rules; failed transactions retain the previous policy.

Network updates are fenced to the installation epoch, so an obsolete updater
cannot resurrect permissions after Disconnect or supersede a new Ensure.
Successful removal discards the previous TUN LUID and control grants before the
next protection cycle. The caller must still enforce NetworkGeneration within
each installation; full daemon orchestration is not implemented by this backend.

Review found that application identity alone is insufficient: a normal user can
launch the same executable path. New ALE permits require SYSTEM or an enabled
Administrators token as well. DHCP is restricted to system `svchost.exe` with
the `Dhcp` service SID, not merely UDP source ports 68/546. The native condition
uses a self-relative security descriptor with `FWP_ACTRL_MATCH_FILTER`; the SDK
oracle confirms its type, GUID and mask. This follows Microsoft's documented
[user/application filtering semantics](https://learn.microsoft.com/en-us/windows/win32/fwp/permitting-and-blocking-applications-and-users).
Executable ACLs remain required; this is not protection against administrators.

| Gate | Result | Scope |
| --- | --- | --- |
| Native vet + shuffled checkptr=2 unit tests ×10 | PASS | WFP and killswitch packages |
| Full policy + actual Wintun cycles ×3 | PASS | IPv4/IPv6 UDP echo; Safety UDP/11211 suppressed before TUN |
| Identical EXE, real non-admin token | PASS | Baseline TCP/UDP succeeds; protected TCP/UDP blocked |
| Privileged control traffic | PASS | Allowed grant succeeds; grant revocation blocks it |
| NO_EXIT and Disconnect | PASS | Ordinary WAN blocked in NO_EXIT; restored after teardown/removal |
| Real process kill and fresh Controller adoption | PASS | Persistent block remains after kill; adoption and Disconnect restore WAN |
| DHCP renewal with full policy active | PASS | Return value 0 and advanced lease timestamp, QEMU DHCPv4 |
| Native WFP regressions ×3 | PASS | Includes three 100-replacement resource series |
| Linux mini-PC vet + race/shuffle ×10 | PASS | killswitch and Safety Policy packages |
| Independent cleanup inventory | PASS | No owned WFP/TUN/user/service residue; security features enabled |

The independent mini-PC echo oracle recorded 116 TCP/UDP messages across the
preserved backend experiments, including baseline, permitted control and
post-cleanup traffic. It recorded no tokens from protected ordinary/same-EXE
non-admin, NO_EXIT, revoked control, or pre/post-kill blocked probes. All three
final full-policy repetitions included real non-admin process identity checks.
This is bounded IPv4 WAN traffic evidence, not a universal no-leak proof for
every Windows protocol or network topology.

The resource gate retained its existing allowances. Per-series process handles
were 173→175, 175→176 and 176→182; the last +6 coincided with one additional Go
runtime thread and its known Event/IRTimer/WaitCompletionPacket footprint.
No unexplained resource-growth threshold was exceeded. These are native store
replacement cycles, not 100 complete product VPN sessions.

An earlier repetition failed before protection installation: the foreign helper
exited without output within the original 8-second supervision window. Its
cause remains unproven. The harness now logs readiness, context and elapsed time
and allows 20 seconds for subprocess execution; TCP/UDP network deadlines remain
1.5 seconds. Subsequent final repeats passed; the failed log is preserved rather
than erased or attributed to Defender without evidence.

Evidence is outside Git at `IRVIO_WINDOWS_LAB/evidence/w05-protected-backend/`:
all four intermediate/final result directories, failed/passing logs, independent
oracle, SDK output, native test executable/source and cleanup inventory. The
SHA-256 manifest covers the artifacts. Reproduction and exact-owner emergency
cleanup are documented in `tools/windows/w05/README.md`; the `protected-*`
scripts require a disposable VM and out-of-band Guest Agent access.

Limitations: this backend is not yet composed into production `irviod`;
route/DNS lifecycle, user intent/automatic reconnect, new-boot cleanup, BFE/Fast
Startup, DHCPv6 on a real IPv6 network and forwarded Exit traffic remain open.
The crash test is Controller adoption, not full daemon recovery to CONNECTED.
No shared Linux production code, wire protocols or workstation proxy state were
changed in this step.

## W05 — journaled native DNS backend

On baseline `5829d48`, `internal/windns` adds native per-interface DNS
snapshot/apply/restore and durable crash recovery. Its target is an explicit
Wintun GUID; it never substitutes a recycled interface index. Only server lists
are changed, preserving automatic/DHCP configuration, static IPv4/IPv6 lists,
suffixes and registration settings. External server-list changes are retained
and reported as conflicts. Removed interfaces clear the owned journal without
touching another interface. Profile/encrypted interface DNS settings are refused
rather than silently overwritten.

The implementation uses existing protected `winfile` storage and native
Get/Set/FreeInterfaceDnsSettings, not PowerShell or command-output parsing.
The Windows SDK C++ oracle confirms V1/V3 sizes and offsets. On this tested
Windows 11 build, a read with flags zero returns IPv4 settings only; flag 1 is
required to read IPv6 independently. This differs from the zero-flags wording
in the [GetInterfaceDnsSettings documentation](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getinterfacednssettings)
and remains a compatibility gate on other Windows builds. V3 is used to detect
encrypted DNS properties; writes set only V1 nameserver flags.

| Gate | Result | Scope |
| --- | --- | --- |
| Native vet, shuffled checkptr=2 unit tests ×10 | PASS | windns, WFP and shared test diagnostics |
| DHCP/empty, static IPv4, static IPv6, dual-stack restore ×3 | PASS | Actual Wintun; full configured snapshot comparison |
| Real child-process kill and journal recovery ×3 | PASS | Parent retains adapter; fresh recovery restores original DNS |
| External edit and adapter removal ×3 | PASS | Foreign IPv4 edit retained; owned IPv6 restored; no recycled-index mutation |
| Native apply/restore 3×100 cycles | PASS | Final typed resource oracle; not full product VPN sessions |
| Ordinary Windows resolver via Wintun IPv4/IPv6 ×3 | PASS | No custom Go DNS dial override |
| Unresponsive tunnel DNS under full WFP ×3 | PASS | Lookup fails; no physical port-53 fallback during marked protection |
| Independent QEMU physical-NIC capture | PASS | Zero outbound TCP/UDP port-53 packets in each of three final intervals |
| Capture checker negative/positive fixtures on mini-PC | PASS | Clean, UDP/TCP leak, missing positive control/repeat, truncated capture |
| Independent cleanup and security inventory | PASS | Baseline DNS restored; no fixture adapter/WFP/service; security enabled |

The first resource experiments exceeded a raw +8-handle limit. An A/B run on
the same binary failed with an ordinary Go caller but passed all three 100-cycle
series with that caller pinned to an OS thread. Native DNS calls now use
serialized retiring locked workers, matching the existing WFP confinement
approach. This bounds native thread-local lifetime; the A/B evidence does not
attribute each handle to a particular undocumented Windows component.

One confined repeat still exceeded raw +8 solely through two additional Go
worker footprints (Thread/Event/IRTimer/WaitCompletionPacket: +12 total).
The final gate therefore uses the existing WFP typed oracle unchanged: at most
two extra threads and four unexplained handles, rejecting incomplete handle
observations. This is an explicitly recorded oracle correction, not a claim
that the earlier failed series passed. Final baseline→last handles were
241→239, 241→241 and 245→243, with two goroutines throughout the samples and
last Go heap-in-use approximately 1.20–1.30 MiB. No sustained unexplained growth
was observed within these bounded series.

An initial unmarked packet capture contained three outstanding test DNS
queries on the physical NIC. QEMU timestamps and guest wall-clock timestamps
were not comparable, so that capture alone could not classify their lifecycle
position. New captures use six in-band markers per repetition. Both quick
teardown repeats show zero protected DNS packets and one outstanding test query
per repetition **after** protection removal. A separate 15-second hold after
lookup cancellation followed by a 2-second hold after DNS restoration showed
zero protected packets and no late test queries. These holds are diagnostic,
not production timeouts or proof that arbitrary queries have drained.

This demonstrates why application context cancellation must not be treated as
termination of Windows DNS Client activity. Runtime teardown must restore DNS
while protection remains installed; ordinary WAN use after explicit Disconnect
is distinct from protected leakage. The capture covers the lab's IPv4 physical
underlay only. IPv6 DNS through the TUN was exercised, but QEMU's underlay had
no IPv6; external IPv6, multi-NIC, NRPT/GPO and browser DoH matrices remain open.

All intermediate failures, A/B logs, SDK oracles, final logs/source/binary,
packet captures and cleanup are retained outside Git under
`IRVIO_WINDOWS_LAB/evidence/w05-dns/`, with a SHA-256 manifest. Raw captures may
contain Windows background traffic and must not be published. Reproduction is
in `tools/windows/w05/README.md` and `dns-check.ps1`.

The backend is not yet composed into production `irviod`. Native APIs provide
neither an atomic compare-and-swap against concurrent administrator edits nor
a hard cancellation timeout; unresolved restoration errors require the caller
to keep protection. One canonical journal location per owner is required.
No Linux production files, wire protocols or workstation proxy were changed.

## W05 — split-route and pinned underlay feasibility

On baseline `49d3edf`, a disposable-VM experiment adds four native on-link
routes (`0.0.0.0/1`, `128.0.0.0/1`, `::/1`, `8000::/1`) to a newly owned
Wintun adapter. It does not remove or replace physical default routes. The
independent SDK oracle confirms `MIB_IPFORWARD_ROW2` size 104 and all field
offsets. Route creation explicitly sets metric, NETMGMT protocol, site-prefix
length and flags rather than relying on the initializer's sentinel values;
this follows the native [route initialization contract](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-initializeipforwardentry).

Native vet and checkptr/shuffled unit tests ×10 passed. Both the initial and
final native 3-repeat experiment passed: ordinary unbound IPv4/IPv6 UDP reaches
Wintun, while the existing physical source/interface-bound UDP socket continues
to reach the mini-PC oracle. Constrained kernel route lookup also returns the
original physical source/gateway. The final independent oracle recorded all
nine expected baseline/installed/restored messages. Full route inventory
identities, metrics and protocol values match before/after; the fixture adapter
is absent. The temporary mini-PC oracle was stopped afterward.

The first SDK compilation lacked `ws2ipdef.h`; it failed before any route
mutation. That log is preserved with both successful repeats, source, test
binary, SDK oracle and route snapshots in
`IRVIO_WINDOWS_LAB/evidence/w05-route-probe/`, covered by SHA-256. Reproduction:
`tools/windows/w05/route-check.ps1` and `route-layout.cpp`.

This confirms the current single-physical-NIC route technique, not a production
route lifecycle. Journaled ownership/rollback/crash recovery, competing routes,
NetworkGeneration transitions, multiple NICs and combined WFP/DNS/runtime
composition remain required. No production files or Linux routes changed.

## W05 — owned routes, recovery and combined DNS/protection

On baseline `2d96bc2`, `winroute.TunnelRoutes` adds a protected write-ahead
journal, process lock, native conflict detection, rollback and GUID-scoped
recovery. It installs only the IPv4/IPv6 split-default pairs on the caller's
exclusively owned virtual adapter. Physical routes and interface metrics are
not changed. Installation requires an operational interface; absence on
recovery clears the journal without targeting a recycled interface index.

Preflight rejects existing routes even when their values match. Native lookup
also detects another next hop for the same interface/prefix. Removal requires
the expected protocol, metric, lifetimes, next hop and flags; external edits
are preserved and reported. Live rollback excludes failed/unattempted additions
instead of deleting a winning competitor. Failed journal narrowing blocks further Ensure and is retried
before cleanup; failed final journal persistence is not reported as success.

| Gate | Result | Evidence scope |
| --- | --- | --- |
| Native vet + shuffled checkptr=2 unit tests ×10 | PASS | Journal ordering/failures, partial rollback, cancellation, conflicts, closed owner |
| Actual Wintun install/remove and idempotence ×3 | PASS | v4/v6 kernel route selection and same-directory ownership exclusion |
| Identical foreign route / alternate next hop ×3 | PASS | Refused before mutation; foreign route retained |
| External metric change ×3 | PASS | Changed route retained; unaffected owned routes removed; recovery record retained |
| Adapter removal ×3 | PASS | No recycled-index fallback; journal clears |
| Real owner-process kill ×3 | PASS | Routes survive while parent holds adapter; fresh journal recovery removes them |
| Final native 3×100 apply/remove cycles | PASS | Existing typed handle oracle, Go goroutines and heap samples |
| WFP → owned routes → off-link tunnel DNS ×3 | PASS | Ordinary Windows resolver, IPv4 and IPv6 DNS servers reached through TUN |
| Independent physical capture | PASS | Zero physical TCP/UDP port-53 packets in each protected interval |
| Global route/DNS cleanup + security inventory | PASS | Initial baselines match; no fixture adapter/process/service/WFP; security enabled |

Native tests first failed before mutation because the fixture adapter name
exceeded the existing tunnel configuration limit; the test name was shortened,
not the product validator relaxed. Early resource series then exceeded the
existing two-thread growth allowance, with increments matching Go worker
Thread/Event/IRTimer/WaitCompletionPacket footprints. Per-native-call worker
retirement did not eliminate those failures. The implementation now confines
each complete operation (journal plus native work plus rollback) to one retiring
worker, avoiding a new worker for every route-table read.

The resource harness also had an asymmetry: baseline was sampled without GC,
but every subsequent sample forced GC. Baseline and later samples now use the
same function. The existing typed thresholds were not changed. Two successive
complete 3×100-cycle runs then passed, including the checked-in reproduction
script. This evidence does not identify every native handle or establish that
the earlier failures were daemon leaks. All failed logs are retained.

In the final three series, Thread handles were 10→10, 12→11 and 11→11;
File handles were 19→19, 20→20 and 20→20. Goroutines remained two and final
Go heap-in-use was about 1.31–1.38 MiB. These are bounded route-only cycles,
not full VPN sessions or a complete native-heap/working-set certification.

The combined test first installs full persistent WFP protection, then owned
routes, then DNS servers `203.0.113.53` / `2001:db8::53` served by its Wintun
fixture. These off-link destinations require the installed routes. A hung DNS
server causes lookup failure without physical DNS fallback under protection.
Teardown restores DNS, removes routes, verifies WFP remains active, and only
then removes protection. Timing markers use the existing privileged physical
source/interface binding, not an unbound socket affected by split routes.
The external capture also has a physical DNS positive control. As before,
the QEMU external link is IPv4-only; this is not an external IPv6 leak proof.

Independent cleanup compares all recorded route keys/metrics/protocols against
the initial baseline and physical DNS against the earlier DNS baseline/native
SDK inventory. Secure Boot, Defender and Windows Firewall remain enabled.
Evidence, intermediate failures, source/binaries, capture and scripts are
outside Git at `IRVIO_WINDOWS_LAB/evidence/w05-route-owned/`, covered by SHA-256.
Reproduction: `route-owned-check.ps1` and `dns-check.ps1 -Mode RoutedResolver`.

Limits: callers must exclusively own the TUN and use one canonical journal
directory. Native route APIs provide no atomic compare-and-delete against a
concurrent administrator or hard cancellation; byte-identical privileged
replacement is indistinguishable. Failed cleanup requires retaining WFP.
Full daemon composition, NetworkGeneration transitions, multi-NIC/IPv6 underlay,
BFE/Fast Startup and complete product crash recovery remain open. No Linux
production code, protocol semantics or workstation proxy state changed.

## W03/W04 — tunnel-bound direct lease control

On baseline `35077c8`, Windows session-control sockets no longer use the
unsupported stub. The Windows dialer captures an existing virtual adapter's
LUID, index and GUID; each connection uses a constrained kernel route, concrete
local source and native unicast-interface pinning. It refuses DNS/wildcard
endpoints, physical NICs, wrong address families, missing routes and changed
identity instead of falling back to WAN. Listeners bind a concrete TUN address
and constrain reply egress. Existing Noise, BIND, renewal, RELEASE and lease
authority code is unchanged.

| Gate | Result | Scope |
| --- | --- | --- |
| Native vet + shuffled checkptr=2 package tests ×10 | PASS | `sessioncontrol`, `winroute`; no Windows race claim |
| Remote-target IPv4/IPv6 TCP SYN observation ×3 | PASS | Actual packets read from Wintun; cancelled dials terminate |
| Native TUN-address listener + Noise ×3 | PASS | BIND/RENEW/RELEASE in both families, local kernel delivery |
| Physical NIC/source, wildcard and no-TUN-route rejection ×3 | PASS | No unconstrained network fallback |
| Adapter replacement with reused numeric identity ×3 | PASS | Original dialer rejects the new GUID |
| Independent physical-NIC capture | PASS | START/END positive markers present; no TCP port-49194 packets |
| Linux vet + race/shuffle ×10 on mini-PC | PASS | Existing session-control protocol and RELEASE regressions |
| Route/DNS baseline and security cleanup | PASS | No fixture adapter/process; Secure Boot/Defender/Firewall enabled |

The first native series failed only at a harness assertion that required LUID
to change after recreation. Windows reused both LUID and ifindex while giving
the new adapter a different GUID. The assertion was corrected to test the full
identity and actual refusal by the old dialer; the product GUID fence was
already present. Two subsequent complete three-repeat series passed. Failed
logs are retained, not replaced by the successful runs.

Evidence is stored outside Git at
`IRVIO_WINDOWS_LAB/evidence/w03-session-control/`, including the final test binary,
source, failed and successful logs, baseline inventories and private physical
capture, with SHA-256. Reproduction: `tools/windows/w03/session-control-check.ps1`.

These are socket/transport gates, not full Windows VPN acceptance. The listener
Noise test uses local kernel delivery, not a remote AWG peer; the separate dial
test observes real remote-target SYNs, not a completed TCP conversation.
Mixed-OS encrypted renewal/release remains required. Socket egress pinning is
not inbound firewall authorization, and runtime must close listeners before
disposing their adapter. Windows route APIs are not atomic against concurrent
administrative replacement. The physical lab underlay remains IPv4-only.

Production composition must provide control-only routes and WFP authorization
before BIND, then enable user routing after activation. The existing split-route
manager alone does not supply that pre-activation control route. The compiler
inventory still fails at Linux-only production/runtime types and constructors;
no working `irviod.exe` product build is claimed. Workstation proxy/routing and
Linux production socket behavior were not changed.

## W01/W03 — shared runtime orchestration and native M11 sockets

On baseline `3545cc3`, moved the existing M11 public-role lifecycle, verifier
discovery, M12 runtime/broker connector, underlay targets, observed GeoIP country,
production config and lease-token helpers out of Linux-only files. Their network
state machines and compatible protocol values are unchanged. Linux daemon and
Data Plane constructors remain Linux-only; no Windows daemon stub was introduced.
Windows public roles require an explicit native control TCP factory before any
role resources are allocated. Both M11 request and challenge hosts use that
factory; the existing marked Linux path remains available unchanged.

Native execution found a real porting defect in TransportOwner: checking Unix
`syscall.EADDRINUSE` did not recognize Winsock bind errors, preventing the existing
fixed-port break/rebind and desktop ephemeral fallback paths. It now uses the
socketmux native bind-conflict classifier. Exclusive UDP ownership is retained;
unavailable-address/network/cancellation errors are not treated as collisions.

| Gate | Result | Evidence scope |
| --- | --- | --- |
| Windows native vet + checkptr=2/shuffle ×10 | PASS | `runtime`, `socketmux`, `transportgen` |
| Real native M11 request/challenge and quorum ×10 | PASS | Separate loopback identities and interface-bound TCP; no synthetic challenger |
| M12 readiness, private registration/lookup/withdrawal ×10 | PASS | Public country index excludes private registration |
| Missing policy and failed request/challenge binding ×10 | PASS | No fallback to established libp2p connections |
| TransportOwner port/handover tests ×100 | PASS | Fixed-port rebind, desktop new-port fallback, predecessor commit fencing |
| Failed promotion cleanup: 100 cycles ×10 | PASS | Mock installer/release client; native process handles and goroutines unchanged within each series; 100 releases each |
| Linux vet + race/shuffle ×10 | PASS | Same three packages plus `cmd/irviod`, mini-PC |
| Shared runtime Windows build / Linux daemon build | PASS | Build-only on workstation; no workstation test or VPN execution |
| Full Windows daemon build | INCOMPLETE | Compiler now reaches Linux-only Local API Server/client composition |

The first attempts also identified test portability issues: one Linux Data Plane
test needed to remain platform-specific, inherited-ACL temp roots needed private
profile children, and the promotion resource test used `/proc/self/fd`. Windows
now measures real process handles; the Linux FD oracle and test remain present.
A Linux withdrawal test raced remote UNREGISTER processing. The same assertion
failed twice in 30 repetitions of unchanged baseline `3545cc3`; it now waits at
most two seconds for an explicit `ErrNotFound`, not arbitrary RPC failure. No
registration wire or lifecycle change was made to accommodate that assertion.

Private evidence, including failed attempts, tested source and successful logs:
`IRVIO_WINDOWS_LAB/evidence/w01-runtime-shared/`, with SHA-256 manifest.
Reproduction: `tools/windows/w01/runtime-check.ps1`. Final source differs from
the tested archive only in documentation and relocation of the activation
deadline comment to its extracted helper. No test processes remained; Secure
Boot, Defender real-time protection and all Windows Firewall profiles stayed on.

These gates do not establish a full Windows daemon, real external M11 topology,
mixed-OS AWG traffic, Windows race coverage, or complete Data Plane resource
stability. Windows Exit/NAT, control-only route authorization before BIND,
Local API/SCM integration and the full product acceptance remain open. Workstation
proxy/routing and installed production nodes were not changed.

## W02 — authenticated Windows Local API transport

On baseline `affef06`, shared the existing Local API v1.7 framing, dispatch,
bounded subscriptions, bootstrap/profile/private commands and client over
`net.Conn`. Linux Unix credentials, socket permissions, stale-file checks and
systemd socket activation remain in the Linux backend. No API/wire version or
network state-machine behavior changed.

Windows uses a local byte-mode `\\.\pipe\IRVIO.Control` listener owned by SYSTEM.
An explicit client account SID is required; missing identity is an error, not
an Everyone/Users fallback. The pipe DACL rejects network logons and permits
SYSTEM/admin ownership plus the configured user's read/write rights without
`FILE_CREATE_PIPE_INSTANCE`. Accept checks the client process token; every
complete frame also checks its named-pipe impersonation token. Token inspection
runs on a locked thread, and a failed RevertToSelf retires that thread. Handle
inspection is fenced against concurrent close/reuse.

The client opens with identification-only SQOS and verifies the server before
sending HELLO. It requires a SYSTEM process and SYSTEM pipe owner. Where a
non-admin cannot query the SYSTEM process token, it instead verifies the exact
pipe-server PID against the running, own-process LocalSystem SCM service
`IRVIO`, using query-only SCM access. Session 0 alone is not authentication.
The native fixture uses a disposable service name, not a product-service stub.

| Gate | Result | Evidence scope |
| --- | --- | --- |
| Windows Local API vet + checkptr=2, shuffle ×10 | PASS | Shared Global/Private API, versions, profile/shutdown acknowledgement ordering, cancellation and events |
| Real SCM service with two non-admin identities ×3 | PASS | Allowed HELLO; different user rejected by kernel DACL; client cannot create another server pipe instance |
| Anonymous message, squatter and wrong SYSTEM PID ×3 | PASS | Explicit refusal; no HELLO sent to a user-owned substitute |
| Exclusive listener, deadlines and GUI reconnect | PASS | Real named pipes; fake network controller, not VPN traffic |
| 100 API start/connect/disconnect/close cycles ×10 | PASS | No unexplained handle growth; file/pipe counts stable; goroutines stable |
| Disposable service/account cleanup | PASS | Service stopped/deleted, both temporary users removed, no fixture process |
| Linux whole-tree vet + shuffled test | PASS | Mini-PC; source archive includes netns cleanup scripts |
| Linux affected race/shuffle ×10 | PASS | localapi, runtime, socketmux, transportgen, cmd/irviod |
| Windows broad internal vet | PASS | All currently compilable internal packages |
| Windows broad internal test | INCOMPLETE | AWG identity still uses POSIX directory sync; STUN test assumes strictly positive loopback RTT |
| Full Windows daemon | INCOMPLETE | Compiler reaches product/daemon composition and Unix-specific CLI credentials |

Native trials retained two implementation corrections: checkptr rejected the
SID subauthority helper, replaced by canonical SID parsing/shape validation;
querying SYSTEM tokens directly did not work for an ordinary UI user, requiring
the SCM/PID proof above. The first resource oracle also mistook Go worker-thread
growth for an IPC leak. Native object-type inventories matched exactly one
Thread, two Events, two IRTimers and one WaitCompletionPacket per additional Go
worker (confirmed against the pinned runtime); no File or token growth occurred.
The final oracle allows at most two such workers and rejects any other positive
object-type delta, rather than increasing a generic handle threshold.

Named-pipe I/O uses pinned `github.com/Microsoft/go-winio v0.6.3`, including its
listener close/accept-race fix. Its module graph also moves x/crypto to 0.57.0,
x/sys to 0.48.0, x/sync to 0.23.0, x/net to 0.59.0, x/text to 0.42.0, x/mod to
0.41.0, x/tools to 0.50.0 and x/telemetry to the pinned 20260908 revision. These
are dependency-forced changes, not an assertion that Linux dependencies are
unchanged. Linux whole-tree passed with this graph; the two broad Windows gaps
above remain explicitly open and are outside Local API.

Private evidence: `IRVIO_WINDOWS_LAB/evidence/w02-local-api/`, including failed
trials, tested source archives, native binaries and Linux logs, with SHA-256.
Reproduction: `tools/windows/w02/localapi-check.ps1` under the SYSTEM lab agent.
Environment-gated account/SCM tests skip outside that harness; a normal `go test`
does not prove their acceptance. No workstation test or VPN execution was used.

This is not the production Windows service, installer, UI integration or a
100-cycle VPN proof. Those composition gates, session-switching product tests,
same-boot recovery and full CLIENT+EXIT remain open.

## W02 — native AWG identity persistence

On baseline `7aeebe9`, fixed the Windows AWG identity path found by the broad
native run. The previous generic implementation used POSIX permission checks
and directory Sync, which failed on Windows and did not provide a Windows ACL
contract. Linux load/write behavior moved to its own backend unchanged; both
platforms share and test the existing 48-byte format, magic and big-endian epoch.
AWG keys, packet handling and transport epoch semantics are not redesigned.

The Windows backend pins a protected profile directory, reads through validated
native handles with a 49-byte bound, and writes with the existing flushed atomic
replacement helper. A native operation-scoped `awg-identity.lock` serializes
creators and rotations; contention returns a bounded explicit error. Rotation
checks both previous key and epoch under the lock, rejecting stale instances
without updating memory. It never repairs unsafe ACLs or substitutes a new key
for corrupt state. Delete Profile recognizes the exact Windows lock filename.

| Gate | Result | Evidence scope |
| --- | --- | --- |
| Native internal vet | PASS | Windows compilable internal packages |
| Native checkptr=2, shuffle ×10 | PASS | awgidentity, productprofile, stunprobe, localapi |
| AWG format compatibility | PASS | Same byte layout and key/epoch roundtrip on Linux and Windows |
| Concurrent first creation and stale rotation ×10 | PASS | One creator; successful loads agree; conflicting/stale writer cannot replace the winner |
| Child-process death while holding native lock ×10 | PASS | Existing identity reopens unchanged after the OS releases the dead process lock |
| Unsafe ACL/link/corrupt-state refusal ×10 | PASS | Key, parent and lock; existing bytes preserved |
| Busy/read-only rotation refusal ×10 | PASS | Durable and in-memory key/epoch unchanged; no temporary-file residue |
| Real non-admin access ×10 | PASS | Read/write/delete denied before and after rotation, including Unicode profile path |
| 100 rotations/reloads and profile deletion ×10 | PASS | File handles stable; no undeletable lock or temporary residue |
| Broad native internal checkptr test | PASS | Environment-gated driver/SCM tests remain skipped unless explicitly provisioned |
| Linux whole-tree vet/test + affected race/shuffle ×10 | PASS | Mini-PC; awgidentity, productprofile, stunprobe, runtime, cmd/irviod |
| Linux daemon build | PASS | Workstation build only, no VPN/test execution |

The STUN loopback test now accepts a non-negative RTT: native evidence showed a
valid reply with two equal monotonic-clock readings. Production STUN timing and
retry logic are unchanged. The first broad repeat also exposed scheduler noise
in the new Local API handle oracle (lazy Go Event handles). That sequential
resource test now warms twenty cycles with GOMAXPROCS=1 before its measured
hundred cycles, preserving the strict per-type growth checks; other Local API
behavior tests keep their normal scheduler. This is controlled sequential
resource evidence, not proof of arbitrary concurrent VPN resource stability.

Evidence: `IRVIO_WINDOWS_LAB/evidence/w02-awg-identity/`, including all trial
logs, exact tested source archives, Linux outputs and SHA-256 manifest.
Reproduction: `tools/windows/w02/awgidentity-check.ps1` under the SYSTEM agent.
Temporary ordinary-user accounts are removed by the harness; their passwords
and generated private keys are not archived. The earlier broad AWG/STUN gaps
are closed by this evidence, not by relabeling the failed historical run.

This does not prove power-loss durability, real daemon crash adoption, a complete
Windows profile/SQLite lifecycle, packaged installation or a working VPN client.
The full daemon still fails at product lifecycle/daemon composition and Unix CLI
credentials. No fake daemon or unsupported-success backend was added. Windows
CLIENT+EXIT, Private hosting, headless server and release acceptance remain open.

## W08 foundation — Windows boot-scoped session intent

Added Windows private persistence for the existing Global country / exact Private
target / hosting / teardown intent. Linux State validation moved unchanged into
a common file; Linux state bytes and filesystem operations did not change.
The Windows envelope is fenced by the verified LocalSystem/session-0 SCM process
instance (PID plus exact creation FILETIME). It uses no estimated boot time or
environment-variable path. Details and limitations:
`docs/product/windows-session-intent.md`.

An actual restart disproved using the native boot-environment GUID as a boot
fence: the GUID remained identical while uptime and the SCM instance changed.
Previous-session Read now returns an empty State and `ErrPreviousBoot`; writing
over old intent is refused until explicit owned cleanup/Clear. Corrupt state,
unsafe ACLs/reparse points/hardlinks and unavailable session identity fail closed.
No network state machine or protocol was changed by this primitive.

| Check | Result | Scope |
| --- | --- | --- |
| Native Windows internal vet | PASS | Compilable internal packages, not full daemon |
| Intent + identity checkptr/shuffle ×10 | PASS | Global/private/teardown, strict bounds, permissions, links, busy targets |
| 100 intent store cycles per repeat | PASS | No stale state or temporary files; not 100 VPN connections |
| Real child-process kill | PASS | CONNECT intent survives; teardown marker never becomes CONNECT |
| Actual restart / cold boot | PASS | Previous intent refused, then explicit fixture cleanup succeeds |
| Actual Hibernate resume, BootType=2 | PASS | Same SCM fingerprint and same saved intent remain readable |
| Fast Startup | NOT EXERCISED | Two `/s /hybrid` requests resulted in cold BootType=0; do not credit them as Fast Startup |
| Sleep / Modern Standby | NOT EXERCISED | VM firmware/graphics does not provide these states |
| SCM same-name decoy + 100 lookups | PASS | Wrong-image process ignored; per-batch handles unchanged (146→146 through 229→229) |
| Native broad checkptr suite, final source | PASS | Initial failure below retained, driver/env-gated tests remain separately gated |
| Linux whole-tree vet/test on mini-PC | PASS | Common State extraction preserved existing product behavior |
| Linux affected race/shuffle ×10 | PASS | productintent and cmd/irviod on mini-PC |
| Linux daemon build | PASS | Build only on workstation; no networking/tests there |

The first broad Windows run found a pre-existing identity publication race:
another reader could observe the atomically renamed key before the publisher
closed its DELETE-access handle. Independent baseline repetition reproduced
26/100 failures in `TestConcurrentLoadOrCreateKeepsOneIdentity`. The Windows-only
fix retries `ERROR_SHARING_VIOLATION` for up to roughly 250 ms while retaining the
pinned private directory and all ACL/link checks. It never retries corrupt data
or overwrites the identity. A forced transient publisher handle and a permanently
busy target have focused regressions; the final concurrent-creation ×100 passed.
No global FILE_SHARE_DELETE relaxation was made.

Evidence: external lab `evidence/w08-intent/`, including v1/v2/v3 source archives,
failed baseline, final native logs, real power transitions and Linux logs. Final
cleanup found zero test processes and no saved fixture intent; Secure Boot,
Defender and all Firewall profiles remained enabled. The workstation proxy and
routing were not changed. Reproduction: `tools/windows/w02/intent-check.ps1`.

This is not a working Windows product service: the full daemon compiler inventory
still lacks Windows runProduct/recoverPreviousBoot/Daemon composition and retains
Unix credential fields in main. Full M8 reconnect/cleanup with real WFP, routes,
DNS, CLIENT+EXIT and user traffic, SCM lifecycle, Fast Startup and UI integration
remain open. Storage component checks do not satisfy those acceptance gates.

## W05 foundation — pre-BIND tunnel control and exact host routes

Added narrow Windows TCP permissions for the authenticated lease-control endpoint
before user routing is enabled. M12 now carries its validated address/port as
`InstallRequest.TunnelControlEndpoint`; the direct control dial uses the same
value. No signaling, lease lifetime or peer-selection semantics changed.
Design and journal compatibility: `docs/product/windows-prebind-control.md`.

| Check | Result | Scope |
| --- | --- | --- |
| Windows compiler/unit policy checks ×20 | PASS | Bounded grants, invalid tuples, deterministic rules, failed promotion, revocation and epoch fencing |
| Real WFP/Wintun pre-BIND packet oracle ×3 | PASS | IPv4/IPv6 daemon TCP emitted; foreign executable, same executable under non-admin, wrong host/port and UDP blocked |
| Revocation and explicit user activation ×3 | PASS | Revoked control cannot emit; ordinary-user TCP emits only after full tunnel permission |
| Owned control route lifecycle ×3 | PASS | 20 IPv4/IPv6 host-route cycles per repeat; no split-default routes installed |
| Native route-owner process kill ×3 | PASS | Both v1 user-route and v2 control-route journals recover exact same-GUID resources |
| Existing native owned-route suite ×3 | PASS | Foreign edits/collisions retained, removed adapter handled, 100 resource cycles per repeat |
| Native internal affected vet/checkptr/shuffle ×10 | PASS | killswitch, winroute, runtime; privileged tests above separately enabled |
| Linux affected vet/test/race/shuffle ×10 | PASS | runtime and killswitch on mini-PC, not workstation |
| Linux daemon build | PASS | Workstation build only; no runtime or networking execution |
| Route baseline comparison | PASS | Native full-suite before/after route properties identical |

Native packet checks observe outbound TCP packets on Wintun; they are not an
authenticated BIND handshake, AWG traffic proof or a complete VPN connection.
The route resource gate measures route-manager cycles, not full VPN sessions.
The tested WFP suite covers newly initiated sockets, not a new exhaustive audit
of every possible already-established connection during policy replacement.

Initial trials are retained: missing disposable SCM fixture; PowerShell treating
Wintun stderr diagnostics as terminating errors; and one first foreign-executable
probe exiting before producing its marker. Two later repetitions in that run
passed, but the run remained failed. The final harness includes a positive
foreign-executable baseline before installing protection, routes stderr through
the existing cmd-style log capture, and passes all three repetitions. No Windows
security service was disabled and no failed trial was relabeled PASS.

Evidence: external lab `evidence/w05-prebind/`, frozen source archives, native
logs, route XML baselines, cleanup report and Linux terminal-result transcription.
Final full route tests use checkptr=2; all production Go changes are identical to
the native/source and Linux regression archive. The later v3 delta adds only
native control-route process-kill coverage; its native full suite passed.

The full Windows daemon still fails to build at product lifecycle/Daemon
composition and Unix credential assumptions in main. Production Windows
SessionInstaller must compose these primitives with real AWG/BIND, DNS,
fail-closed cleanup and generation ownership. W06 Exit/NAT, CLIENT+EXIT, Private
hosting, SCM/UI and installer acceptance remain open; this is not a Windows
release verdict.

## W04/W05 — shared ClientDataPlane and Windows staged installer

The Linux installer orchestration was extracted without changing its generation,
promotion, key-rotation or RELEASE-first semantics. Platform-specific adapters
provide Linux NativeTun/routes or Windows Wintun/WFP/owned routes/DNS. Windows
requires an exact validated control endpoint and a successful existing
`BeforeUserRoutes` callback before activating user traffic. Independent WFP
underlay/tunnel updates retain the other lane under the policy lock and epoch.

| Check | Result | Scope |
| --- | --- | --- |
| Windows focused adapter/policy tests ×20 | PASS | Ordered activation, each failure stage, cleanup attempts, invalid control inputs, independent WFP lane updates |
| Native Windows installer ×3 | PASS | Three generation replacements per repetition, real Wintun/AWG encrypted UDP, TUN reuse, stale closer fencing |
| Pre-BIND and teardown ordering | PASS, component scope | User UDP denied before test BIND callback; control closer runs before tunnel permission removal |
| Native route/DNS before/after | PASS | Full selected route/DNS properties match; WFP fixture, TUN and service removed |
| Windows internal vet | PASS | Internal packages; not the still-incomplete product daemon |
| Windows affected checkptr=2/shuffle ×10 | PASS | runtime, killswitch, tunnel, awg, exitselector; privileged tests separately gated |
| Windows release/resource test ×100 | PASS after oracle correction | 100 measured failed-promotion/RELEASE cycles per repetition; original +2 handle/goroutine limits retained |
| Linux whole-tree vet/test | PASS | Mini-PC, with privileged tests gated out here |
| Linux affected race/shuffle ×10 | PASS | runtime, killswitch, tunnel, awg, exitselector |
| Linux selector shuffle ×100 | PASS | Fake-clock fixture synchronization regression |
| Linux real M10 lifecycle | PASS | Actual production daemon, tunnel/lease, renewal/key rotation in isolated namespaces |
| Linux real M10 protected failover | PASS | Actual Exit loss/reselection/user traffic, not fake connector assertions |
| Linux M10 host integrity | PASS | Captured host state identical, no test namespaces left |
| Windows security/cleanup | PASS | Secure Boot, Defender and all Firewall profiles enabled; no fixture service/adapter/process |

The native test supplies a synthetic ICE result and a test BIND callback; its
remote peer uses ChannelTUN. It is **not** proof of real Windows ICE → Noise BIND,
Exit NAT, public Internet, CLIENT+EXIT, runtime crash adoption or UI integration.
The Linux M10 result cannot substitute for those Windows gates.

Initial failures are retained in evidence:

- The Windows native installer found a missing parent journal directory. The
  adapter now creates/pins it through `winfile.OpenDirectory`; ACL checks were
  not weakened and arbitrary directories are not repaired.
- The Windows fake-resource test also failed on independent baseline `e2a5c7f`:
  three of 100 repetitions observed lazy Go handle-cache growth, then stabilized.
  Its sequential resource measurement now warms the fixture and uses one Go
  scheduler thread on Windows. RELEASE counting and leak limits are unchanged;
  this is not a concurrent native VPN resource proof.
- The first Linux whole-tree run observed an unconsumed initial fast-connect
  timer in `TestSelectorLateRecoveryCannotReplaceNewActive`. The fixture now
  waits for that timer before completing its initial connection. Production
  timers are unchanged. Independent baseline isolated ×1000 and selector suite
  ×100 did not reproduce it; do not label it an independently proven Core bug.

Evidence: external lab `evidence/w04-client-dp/`, including failed trials,
independent baseline, final source archive, native logs, DNS/routes snapshots,
security check, mini-PC gate logs and M10 host-integrity JSON. Final source archive
SHA-256: `3749ef08bb9d5fee54eb19979d3aac3d2ae07ae46685190bbf35fd4e332d7280`.
The native v2 binary has the same production Go changes; final-source deltas
are test-oracle/harness changes. Reproduction harness:
`tools/windows/w04/client-dp-check.ps1` (disposable elevated VM only).

The current full Windows daemon build still fails at `runProduct`, boot recovery,
Daemon/StaticTargets composition and Unix Local API credential fields in main.
No unsupported-success stubs were added to hide these gaps. Next: compose real
Windows control/data paths and authenticate BIND, then complete Exit/NAT and
CLIENT+EXIT; this commit is not a Windows release acceptance.

## W04/W05 — real Windows-to-Linux BIND over AWG

Added a separate gated native test and a bounded Linux peer helper; production
protocol and implementation files did not change in this step. Windows uses
the staged ClientDataPlane, WFP, real Wintun and tunnel-bound Noise dialer.
The mini-PC peer uses real Linux TUN/AWG, production TunnelLease LifecycleAuthority
and the existing sessioncontrol server. Private identity keys are generated
independently in memory and never sent to the fixture coordinator.

| Check | Result | Evidence scope |
| --- | --- | --- |
| Initial mixed-OS run | PASS | Real Noise BIND, inner UDP, RENEW, RELEASE, zero remaining Linux leases |
| Final mixed-OS run | PASS | Adds explicit remote rejection of incorrect session token before valid BIND |
| Pre-BIND user-plane gate | PASS | UDP cannot complete before BIND; same probe succeeds after promotion |
| Windows cleanup | PASS | Exact WFP/service/TUN removal; DNS/routes match before/after |
| Linux cleanup | PASS | All-table route JSON byte-identical; owned TUN and TCP listener gone |
| Windows vet/checkptr/shuffle ×10 | PASS | runtime and sessioncontrol; native network test separately enabled above |
| Linux whole-tree vet/test | PASS | Mini-PC only; privileged network tests gated separately |
| Security | PASS | Windows Secure Boot, Defender and all Firewall profiles stayed enabled |

The selected endpoint/keys/lease are passed through an explicit disposable lab
coordinator. Its nonce UDP packet observes the NAT mapping; it is not STUN or
ICE. The helper allocates an active lease directly, not through M12 provisional
activation. The result therefore closes the real authenticated tunnel-control
component gate, **not** discovery, ICE, public Internet, production Exit NAT,
CLIENT+EXIT, or full Windows runtime acceptance. BIND and renewal use the actual
Noise wire protocol over the encrypted UDP physical path, not `net.Pipe` or a
fake callback. The wrong-token test requires a real remote error response, not
merely a dial timeout.

Reproduction and constraints: `tools/windows/w04/README.md` and
`tools/windows/w04/client-dp-check.ps1 -NetworkBind`. Test sources:
`internal/runtime/client_dataplane_bind_windows_test.go`,
`test/cmd/irvio-test-client-dp-peer/main_linux.go` and
`test/support/clientdplab/protocol.go`.

Frozen evidence: external lab `evidence/w04-network-bind/`. Source archive
SHA-256: `ae8296cfaf448f61c18a5012e5453e2c01742259c6cf3ea3636aa8095d48414c`.
It includes both native runs, peer logs, baseline/final routes, Windows DNS and
route snapshots, source gate output and build hashes; no identity keys or lab
authorization nonce are included. The workstation only built/copied files;
its networking and proxy were unchanged.

## W06 — staged packet translation and native port ownership

Added `internal/exitnat` as a bounded, lease-owned IPv4 TCP/UDP packet translator,
not yet wired into a runtime or driver. It validates active ownership, source,
remote tuples, path generation, mandatory host/LAN denial and existing Safety
Policy before emission. Endpoint-independent mappings share native reservations
across one lease's contacted remote tuples; revoked/expired flows lose authority
and ports remain quarantined. Failed resource cleanup remains accounted for.
The only Linux production change moves its unchanged destination-deny list into
a shared source file. No wire, selection, ICE/AWG or lease semantics changed.

Windows `ReserveWindows` owns real exclusive Winsock source/protocol ports with
native interface/source validation. TCP reservations never listen; handles are
non-inheritable. Close is serialized/idempotent, with failed native cleanup
retained for retry. No kernel forwarding or firewall mutation is required.

| Check | Result | Evidence scope |
| --- | --- | --- |
| NAT lease/source/tuple/Safety/generation/lifecycle tests | PASS | Pure packet engine and fake bounded emitters; not driver traffic |
| Native Windows reservations | PASS | TCP/UDP collisions including SO_REUSEADDR, release/rebind, non-inheritance, non-listening TCP |
| Windows native whole component suite | PASS | Initial shuffled ×10; final ×3 plus separately instrumented checkptr ×3 |
| Native resource cycles | PASS | Each repetition performs 100×10 reservations; counts stable within measured cycles |
| Native Windows vet | PASS | exitnat, exitfirewall, safetypolicy |
| Linux affected race shuffled ×20 | PASS | exitnat, exitfirewall, safetypolicy, awg on mini-PC |
| Linux whole-tree vet/test | PASS | Final frozen source; tests with explicit privileged gates remain gated |
| Parser fuzzing | PASS | 60s, 1,778,037 executions on final packet-engine logic |
| Windows host policy | PASS | Interface snapshots identical; Secure Boot/Defender/Firewall enabled |

Initial native all-suite repetitions grew from 138 to 159 process handles and
then plateaued across the last four repetitions. The measurement fixes one Go
scheduler thread and warms each resource fixture. Final regular repetitions
recorded baseline/last/max 137/137/137, 149/149/149, 159/159/159. This demonstrates
bounded native reservation cycles, not zero process-wide lazy initialization
or a 100-cycle full VPN leak proof. The instrumented run is separate; merely
setting a GODEBUG variable was not treated as enabling checkptr instrumentation.

The pre-final whole-tree run failed an unchanged AWG backpressure test. Independent
baseline `1176f3a` reproduced 2 failures/500; diagnostic baseline 2/15,000.
Failed cases received only 226/228 data datagrams and no control datagram at the
mux (zero classifier/control drops); the single STUN request did not reach its
intended assertion boundary. The test now paces the same 256 input datagrams in
batches of 16 while the Data queue stays full. It still sends one STUN request
and retains the same one-second deadline. Production code/timeouts are unchanged.
The corrected isolated test passed 1,000 repetitions, followed by affected race
and whole-tree gates. Initial failures and diagnostic counters are preserved.

Evidence: external lab `evidence/w06-exitnat-engine/`. Final tested source archive
SHA-256: `1ff4858735c8e723083bf501cbc5eb54bb5548305a831884cee0da5fd213eb24`.
Instrumented Windows test executable SHA-256:
`237d0bd2bdf5b1f6ff462ba40a88d938b093c8a64ede7f5b76fb86e2a17d95d3`.
Subsequent changes are validation/documentation only. Driver ownership/loading,
capture/injection, ICMP/PMTU/fragments, complete TCP corpus, AWG/runtime lease
hooks, real downstream Internet traffic and full CLIENT+EXIT remain open.

## W06 — pinned driver module and immutable package files

`internal/windivert` now verifies the pinned official x64 DLL/SYS, native cached
signature/revocation trust for SYS, protected SYSTEM/admin-only ownership and
ACLs, non-reparse/hardlink files and immutable read handles. Public loading uses
only a native Program Files Known Folder path; DLL search is restricted to its
verified directory and System32. Module/payload ownership lasts until process
exit, preventing unsafe DLL unload while vendor TLS state may remain.

Runtime performs no SCM mutation. The current no-traffic probe requires the
exact verified service path/type/start mode and running state, opens with
`NO_INSTALL` and filter `false`, then closes on a dedicated terminating thread.
It neither captures nor emits user traffic. Failed probe close retains ownership
and prevents new opens until retry succeeds. Packet I/O cancellation/driver
crash recovery and production installer integration remain unimplemented.

| Native Windows check | Result |
| --- | --- |
| Pinned vendor DLL + signed SYS verification | PASS |
| Missing service does not auto-install | PASS |
| Wrong service path refused | PASS |
| Modified DLL / modified SYS refused | PASS |
| Unsafe ACL / active writer refused | PASS |
| Rename/replacement/deletion blocked while pinned; released afterward | PASS |
| Unsigned DLL cannot satisfy SYS signature verification | PASS |
| 3×100 empty-filter driver probe lifetimes | PASS |
| Native affected vet and checkptr/shuffle ×3 | PASS |
| Owned service cleanup | PASS, 0 remaining drivers |
| Secure Boot / Defender / Firewall | Enabled throughout |

Handle counts in the three final probe repetitions were 348→349, 349→347,
349→347. These are empty-filter ABI/lifetime checks, not 100 VPN lifecycles.
Dependent native checks included winfile, identity, awgidentity, privateaccess,
productbootstrap, productprofile, productintent, windns and winroute. Explicit
environment-gated network tests are not implied by this package-suite pass.
Linux production files are unchanged in this step; Linux tests were not repeated.

An initial negative-test trial used `WriteAtomic(..., replace=false)` when it
intended to replace a verified file. Its failure and the corrected test's results
are retained separately; production checks were not relaxed. Final tested source
archive SHA-256:
`411f033ee54d4b7172262571606e40b2577f5dcd0236f1cb55251edf498f7079`.
Instrumented test executable SHA-256:
`d0fd469f7d76c741f3d143a83657e4acdb7c59c2f5898b8db0b0d327b7a2cdb6`.
Later deltas are documentation only. Evidence is in the external lab's
`evidence/w06-driver-module/`; the dedicated protected payload files remain
installed for subsequent lab work, but no driver service or test process remains.

See [driver boundary](../product/windows-exit-driver-boundary.md) and
`tools/windows/w06/module-check.ps1`. This does not finalize the backend decision
or close real downstream, CLIENT+EXIT, installer/licensing or clean Home/Pro gates.

## W06 — real overlapped driver I/O

Added typed exact-port capture and IPv4 forward injection with pinned native
overlapped buffers, context cancellation, admission/close fencing and retained
ownership on close timeout. Capture does not include unrelated host ports.
The lower-level driver API does not authorize leases or replace Safety Policy;
it is still separate from `exitnat` and the runtime.

The final native run passed 15/15 UDP round trips to the mini-PC's isolated
oracle across five simultaneously retained reservations/capture handles per
repetition. Source/interface and packet payload were checked; matching replies
did not also reach the host UDP sockets. Pre-canceled and wrong-source sends
were rejected. This test does not involve an upstream tunnel, WFP ProtectedMode,
AWG or Internet NAT, so it cannot prove anti-multihop or CLIENT+EXIT.

Three 100-cycle cancellation/close tests passed after 50 warmup cycles each.
Measured `(warmup max, before, after, sampled max)` process handles:
`(350,348,348,348)`, `(371,364,357,364)`, `(371,364,357,364)`.
The test uses one scheduler thread, disables GC so finalizers cannot hide missing
closes, and checks a warmup high-water envelope plus the existing four-handle
allowance. Process handles fluctuate; this is bounded component evidence, not
an assertion of zero allocation or a full VPN leak proof. Exact sources of the
remaining process-wide fluctuation were not attributed by object type.

Initial failures are retained, not replaced:

- v1/v2/v4 had failures against a single post-warmup handle sample; the curves
  oscillated rather than growing per cycle. Disabling GC alone did not fix that.
- v3 added thread joining and exposed a real lifecycle issue: Go's startup `m0`
  is parked, not terminated, when a locked goroutine exits. Joining it hung until
  the three-minute test timeout. The wrapper now avoids `m0` and waits for actual
  native thread termination; a focused 100-call regression checks worker IDs.
- v5 retained the same I/O implementation as v4 and corrected resource baseline
  sampling to a 50-cycle high-water window. v4's UDP tests already passed; the
  final combined traffic/lifecycle harness passed without skips.

Native vet and checkptr/shuffle ×10 passed for windivert, exitnat and winroute;
the separately gated native driver/traffic run above is the real I/O proof.
Secure Boot, Defender and Firewall stayed enabled. Owned driver service and
mini-PC oracle were stopped; no live fixture remains. Linux code was unchanged
and workstation networking/proxy was not touched. Native race remains an open
gate, not implied by checkptr.

Evidence: external lab `evidence/w06-packet-io/`, including failed runs and their
test executables. Frozen final source archive SHA-256:
`67319627d94058ae4ec891282a05e0e5dabd2d0ead12f22a082c26b086a3f4f2`.
Instrumented native binary SHA-256:
`4bfee5c6a76f88f3d9996d85089ec7dfdc39928431e2076c04c7e83a38197c29`.
Subsequent edits are documentation only. Next: reservation-fenced NAT bridge,
then actual AWG downstream traffic and full dual-role integration.

## W06 — reservation-fenced translator/driver bridge

Baseline `c30422d`. `exitnat.Backend` now composes the lease-owned translator,
exclusive port reservations, exact-port reply captures, a bounded 128-packet
queue and physical forward sender. It remains outside daemon/AWG runtime.
The engine supplies an opaque per-reservation `ReplyToken`; reverse translation
checks it while holding the engine lock. A retained old token cannot authorize
a queued reply after quarantine and reuse of the same port by another lease.
The regression exercises both TCP and UDP with identical transport tuples.

Capture workers never acquire the engine lock or wait for queue space. This
permits cleanup to join them without an engine/reader deadlock. Capture failure
cancels the backend and stops forwarding; overflow drops packets. Close cancels
work first, and failed native cleanup remains owned for retry. Each backend is
fixed to one generation; runtime must replace it on physical path changes.

| Check | Result | Scope |
|---|---|---|
| Mini-PC affected vet | PASS | exitnat, exitfirewall, safetypolicy |
| Mini-PC race/shuffle ×100 | PASS | Translator/bridge, policy and lifecycle tests |
| Native Windows vet | PASS | exitnat, windivert, winroute |
| Native checkptr/shuffle ×20 | PASS | Component tests; gated driver checks run separately |
| Missing driver | PASS | Constructor refuses; no runtime service installation |
| Five lease public DNS round trips ×3 | PASS | 15/15 answers through real translator and driver |
| Same inner UDP port on five leases | PASS | Exclusive external ports; matching inner destination and DNS ID |
| Revoke/quarantine/close | PASS | Zero active leases/flows on revoke; reserved ports released on close |
| Native reservation stress ×3 | PASS | 100 cycles ×10 ports; not full capture/backend stress |
| Driver fixture cleanup | PASS | Owned service removed; 0 remaining driver services |

The native DNS oracle was `1.1.1.1:53`, queried for `example.org`. Each run first
verified the oracle independently with a physical-bound ordinary socket. The
actual test then used five synthetic leases and raw inner UDP packets, with no
mandatory-policy exceptions, no real AWG clients and no own upstream VPN. Tests
check DNS response identity, success and reverse translation. Native elapsed
times were 0.20s, 0.13s and 0.12s including preflight; these are not product
connect timings. Resource-test handle triples (baseline/last/max) were
423/423/423, 430/432/432 and 432/432/432 within the existing test tolerance.
They are not evidence for 100 full CLIENT+EXIT lifecycle cycles.

Secure Boot, all firewall profiles and Defender remained enabled. Workstation
proxy/routes were not changed; test execution was confined to mini-PC and VM.
Evidence is in external lab `evidence/w06-backend/`, including initial and final
native runs, source archive, instrumented executable, cleanup and test logs.
Frozen final source SHA-256:
`71d4f340e68df041bf838c21b02fe36ffbb95cf31892bb7e4efceb63dafc9b60`.
Instrumented Windows test binary SHA-256:
`628664285443cf2d5badb5affd122b2658d044191a2c6d79e5ebf397760e3317`.
Subsequent edits are documentation only. Remaining next steps include real TCP
and ICMP/PMTU coverage, backend stress, AWG plaintext/lease activation wiring,
dual-role security/resource proof and complete runtime composition. Neither
this test nor the earlier driver primitive is Windows EXIT acceptance.

## W06 — Core lease resources and encrypted NAT composition

Baseline `ccd8bf1`. Added `LeaseResources`, implementing the existing Core
activator/renewer/revoker contracts around NAT and caller-owned AWG peer hooks.
It does not replace Core admission, allocation, session tokens or renewal rules.
NAT forwarding starts only after successful peer installation and a second
validity check; expired/cancelled activation rolls back. Failed peer cleanup
remains tracked and consumes capacity. Stale lease revisions cannot revoke a
renewed resource, and revocation disables NAT before removing the AWG peer.

During implementation, a reproducible lock-order defect was found in the new,
uncommitted adapter: holding its table lock during peer removal could wait for
an AWG plaintext worker that needed the same lock. The saved pre-fix regression
fails both explicit revoke and shutdown. Lifecycle serialization and packet
lookup now use separate locks; the fixed test lets the worker finish with DROP.
This was not a defect discovered in the deployed Linux release. A separate test
covers expiry during peer installation. Reply dispatch now supplies a two-second
cancellable context to its emitter; this is cooperative cancellation, not a
claim that a hung kernel/callback can be forcibly terminated.

The encrypted fixture uses five real AWG transports plus an Exit transport,
socketmux and the production obfuscation parameter values. Core provisional
allocation/activation installs exact `/32` AllowedIPs; all clients then obtain
NAT replies using the same inner UDP port. A forged source belonging to another
peer must not allocate a NAT mapping. After renewal, all five still work;
RELEASE removes one peer while the other four continue. RELEASE overlaps a
bounded burst of 128 policy-denied TCP packets, without sending those to Internet.

| Check | Result | Scope |
|---|---|---|
| Pre-fix adapter lock regression | Expected FAIL | Both revoke and shutdown, preserved baseline |
| Mini-PC focused race repetitions ×150 | PASS | Encrypted fixture and lease lifecycle tests |
| Mini-PC diagnostic race/shuffle ×50 | PASS | exitnat and existing AWG suites in parallel |
| Final mini-PC affected vet/race ×50 | PASS | exitnat, tunnellease, awg |
| Windows native driver ×3 | PASS | 42 encrypted public DNS replies plus 15 direct NAT probes |
| Missing installer-owned driver | PASS | Runtime refuses without installing service |
| Windows native vet/checkptr ×20 | PASS | exitnat, tunnellease, awg |
| Fixture cleanup | PASS | Backend counters zero, peer removal, owned driver service removed |

On Windows the translated traffic reached `1.1.1.1:53` for `example.org`. The
clients' outer AWG traffic used loopback sockets within the VM; keys/endpoints
were test-coordinated, and plaintext endpoints were channel TUNs. The test calls
Core lifecycle methods directly, not the Noise BIND/RELEASE wire protocol.
It therefore proves encrypted NAT/lifecycle composition, not real DHT/ICE,
native Wintun demultiplexing, Windows Exit signaling or a ready CLIENT+EXIT.
Those production integration gates remain open, as do TCP/ICMP/PMTU parity,
reply ownership across AWG replacement and full dual-role resource/security stress.

The existing reservation-only stress in the combined native process reported
handle triples (baseline/last/max) 183/183/183, 448/450/450 and 461/461/461.
Within each reservation test the checks passed, but the process baselines after
adding the native encrypted fixtures are not constant. Warm-up/cache versus
retained-resource attribution has not been established. These figures cannot
serve as a full backend/AWG no-leak proof; a dedicated warmed lifetime stress
with handle-type attribution is still required before runtime acceptance.

One initial Linux package invocation failed after 48.999s during the first
parallel count-20 run. The terminal output was truncated at the diagnostic
portion, so the exact assertion/cause was not retained; elapsed time alone is
not proof of a network timeout. A replay with its shuffle seed passed 20/20;
subsequent diagnostic count-50 and focused count-150 runs also passed. More
packet/peer/mux diagnostics are now included on timeout. This remains an
unclassified test observation, not a confirmed Core defect or proven lab
artifact; a later green run does not explain it.

Evidence: external lab `evidence/w06-encrypted-leases/`. Final frozen source:
`080103742246daab2ce8486463e806a2df5b7a5f492ef0a0b7204b89845dc497`.
Instrumented Windows test binary:
`9ee2ce505d8df6491d9540f257bd00f79c91fbeb607d8cc07d799ec83dbfa46b`.
Subsequent edits are documentation only. No workstation networking/proxy changes,
no GitHub push and no full-port acceptance are implied by this step.

## W06 — worker isolation and extended resource profiling

Baseline `8604013`. Extended encrypted lifetime profiling uncovered a real
Windows worker ownership defect, separate from the earlier adapter lock fix.
The old `onNativeThread` locked an arbitrary Go scheduler thread and terminated
it to obtain vendor DLL thread-detach cleanup. That thread could already own
unrelated pending overlapped I/O. A forced non-startup-thread named-pipe test
reproduced cancellation on the very first worker: pre-fix **3/3 FAIL**, with
`ERROR_OPERATION_ABORTED` and matching submit/worker thread IDs. The initial
unforced version passed and was not accepted as a reproducer.

Workers now use `CreateThread` and one process-lifetime Go callback, with opaque
per-call tokens rather than Go pointers crossing the native API. Completion is
joined through the native thread handle, so DLL detach still finishes before
return, without terminating an existing Go M. Explicit token/handle cleanup is
retained. No wire, lease authorization or packet-selection semantics changed.

| Check | Result | Scope |
|---|---|---|
| Forced pre-fix pending-I/O regression ×3 | Expected FAIL | Preserved named-pipe cancellation evidence |
| Fixed worker tests ×20 | PASS | Pending I/O survives; worker is not startup thread |
| Native driver module ×3 | PASS | Ownership, payload checks, overlapped cancellation/deadline/close |
| Native affected vet | PASS | windivert, exitnat, wintest, winroute |
| Native affected checkptr/shuffle ×20 | PASS | Same packages; explicit native fixtures run separately |

The driver suite's private echo test was not exercised in this run because its
separate private oracle was not configured. Public encrypted NAT profiling is
separate. No native race claim is made.

Resource profiling is **not yet acceptance PASS**. The initial native 120-cycle
run completed its traffic but failed a one-point thread-growth oracle. Per-type
warmup envelopes were then added as a diagnostic refinement, with zero growth
allowance for File/Key/ALPC/Etw objects. Three in-memory series initially passed;
a later series exceeded the small scheduler allowance (31 versus 28+2 threads)
without a corresponding File-handle rise. The fixed in-memory run also exposed
a lost post-burst UDP query: only 13 plaintext probes were observed and the mux
reported a data-queue overflow. This is not evidence that the worker fix failed,
and simply increasing allowances or calling all timeouts external loss is not
an acceptable resource proof.

Before the worker fix, native profiles also had DNS timeouts and one explicit
I/O-aborted failure. Added test-only counters separate successful native sends,
captured replies and AWG delivery by DNS ID. A bounded VM NIC packet capture
did not observe all successful injected traffic; it cannot locate every lost
response. Those timeouts remain distinct from the independently reproduced
worker cancellation. The original logs are preserved rather than overwritten.

After the worker fix, all three native resource repetitions still ended in
single-response timeouts (cycles -17, -13 and 41), without an I/O-aborted error.
Each missing DNS ID had `sent=1, captured=0, delivered=0`; the backend had no
reported fatal error or reply-queue drop. A successful injection API return is
not proof that the remote DNS service received a packet. This narrows the next
investigation to physical injection/underlay/return capture, but does not yet
classify it as an external-network artifact. Do not retry until green or credit
these incomplete runs as a 100-cycle native resource PASS. Final owned driver
cleanup passed; Secure Boot, Defender and all firewall profiles remained enabled.

Evidence: external lab `evidence/w06-resource-lifetime/`. Fixed source archive:
`7269425ac35fc090af3fe6b32c107cf9a70ad5973a93a2cddf69368f012a3485`.
Worker test binary:
`697500153dfb10fb54b4f7fb4264b76594a20f9161f85693c6fbd7e321c20b1c`.
Encrypted resource binary:
`e07cd33d13e635598b2f3b0aa1d2dc5166696b6af4766793746b598504514ea4`.
Subsequent report edits do not change those tested sources.

## W06 — captured reply loss and stabilized component resource gate

Baseline `cbf2ac6`. A bounded QEMU `filter-dump` capture at the Windows VM's
virtual NIC separates guest injection/capture from the outside underlay. It
does not change workstation routes or proxy settings. The capture filter was
removed afterward. The first frozen-binary run had 1,680 requests and 1,680
answers; all 120 traffic cycles completed, while its old warmup resource oracle
still failed. A second run captured 2,162 requests and 2,160 answers. The exact
two IDs reported missing by the fixture were visible leaving the VM, with no
matching return frame during the timeout:

| DNS ID | VM source port | Link requests | Link replies |
|---|---:|---:|---:|
| 4629 | 54271 | 1 | 0 |
| 15470 | 54306 | 1 | 0 |

For these captured cases the missing return is outside the guest backend. This
does not identify whether QEMU user networking, host underlay or the remote DNS
path lost it, nor retroactively classify every earlier timeout. There were no
unexpected reply tuples in the capture. Host and guest wall clocks were not
subtracted to manufacture latency measurements.

The test application's DNS exchange now permits at most two retransmissions,
500 ms apart while waiting, with explicit `dns_retries`. This is not a Core,
AWG or native-I/O retry change. Tuple, DNS ID and response validation remain
strict; only already validated responses for that same peer are recognized as
duplicates. Native/backend errors still fail immediately. New tests cover
exhaustion, cancellation, backend/validation/send errors and actual encrypted
exchange with deliberate first-request loss plus duplicate replies.

The resource profile now requires 20 consecutive cycles without a new per-type,
total-handle or goroutine highwater, within at most 100 warmup cycles. Monotonic
growth therefore fails warmup; it cannot keep moving an accepted baseline.
The existing measurement growth allowances are unchanged. The reference is
frozen before each 100-cycle measured interval. No forced GC is used.

| Mode / repetition | Warmup cycles | Warmup max / measured last / measured max handles | File handles | Goroutines |
|---|---:|---|---:|---:|
| In-memory 1 | 39 | 210 / 210 / 210 | 13 | 2 |
| In-memory 2 | 21 | 210 / 211 / 211 | 13 | 2 |
| In-memory 3 | 21 | 211 / 212 / 212 | 13 | 2 |
| Native 1 | 56 | 506 / 507 / 507 | 19 | 2 |
| Native 2 | 21 | 507 / 509 / 509 | 19 | 2 |
| Native 3 | 21 | 509 / 509 / 509 | 19 | 2 |

All six measured series **PASS** their component traffic/resource assertions.
The native runs completed 398 cycles including warmup, or 5,572 validated
encrypted DNS replies, with eight application retransmissions. Memory mode
needed none. Measured native Go heap allocation fluctuated around 5.95–15.08 MB;
this is not an RSS or kernel-pool leak proof. One sequential type enumeration
summed to 510 while the subsequent total query returned 509; these observations
are not atomic and cannot provide perfect instantaneous object attribution.

Mini-PC vet and race/shuffle ×20 passed; Windows vet and checkptr/shuffle ×20
passed. Both explicit driver fixtures cleaned up their service, with Secure
Boot, Defender and all firewall profiles enabled. Windows native race remains
a separate open gate. The production implementation is unchanged in this step.

Evidence: external lab `evidence/w06-reply-loss/`, including original failing
runs, NIC captures, correlated IDs, updated test runs, summaries and cleanup.
Frozen tested source SHA-256:
`ad2a25d364bee465d705c8664b65b4688b58c72d03ca13ab8d79dc9bbcfc7b13`.
Windows instrumented test binary:
`2dba6f06c7eaf5904db808eb5e63c635fc0f3f1ea8cb61df7941c05050c05e9e`.
Later changes are documentation only.

This closes the bounded encrypted AWG/NAT/Core-lease component resource test,
not full Windows EXIT or product lifecycle acceptance. Native Wintun routing
and plaintext demultiplexing, own upstream coexistence, full Windows Exit
signaling and daemon composition remain required. A fresh cross-build of
`./cmd/irviod` still fails at missing product/daemon composition and Unix API
credential fields; no stubs were introduced to call that executable complete.

## W06 — plaintext bridge and native Wintun dual-role component

`internal/exitbridge` now owns a borrowed TUN and separates authenticated AWG
plaintext into own-upstream host delivery, exact downstream UDP control, and
lease-authorized downstream forwarding. Host user packets cannot enter a known
downstream route. NAT replies carry an exact local attachment lifetime; stale
queued replies are discarded. This changes no AWG, ICE or TunnelLease wire
format and does not itself grant a Core lease.

Address reuse follows `Retire → remove authenticated AWG peer → Forget`.
Retire waits for forwarding users and the next TUN Read invocation: in the
pinned AWG reader this proves that the previous packet has already undergone
AllowedIPs lookup and been associated with its concrete peer. A real AWG test
pauses **after** bridge Read and **before** that lookup, proves early retirement
times out, then replaces the peer at the same inner address. The new peer gets
only the new reply; own-upstream traffic continues. The generic fixture uses
loopback muxes, test-coordinated keys/endpoints and in-memory TUNs.

The elevated native fixture instead uses the existing Windows Wintun owner,
two owned on-link prefixes, real Windows UDP sockets, five encrypted downstream
peers and one encrypted upstream peer. It checks exact control round trips,
separate downstream forwarding callbacks, own traffic round trips, and own
traffic after all downstream peers have been removed. It uses an echo callback,
**not** physical Internet NAT, Core admissions, DHT, ICE negotiation, WFP
ProtectedMode or a complete product session. No default route, host proxy,
firewall setting or IP forwarding setting is changed.

The initial native fixture failed all three cleanup assertions because it
queried interface disappearance immediately after Close. All traffic assertions
passed and the independent post-run adapter inventory matched baseline. The
fixture now uses the same bounded five-second PnP completion wait as the existing
native tunnel tests; the rerun passed 3/3, with unchanged adapter inventory and
all firewall profiles enabled. The original failures are preserved, not relabeled
as passing runs.

Final source archive SHA-256:
`23bf6acf1f56a60f894bc2969fe8376d240470bf3bf6fa6ac90ca3c7945edba1`.
Mini-PC vet for bridge/NAT, bridge race/shuffle ×20 and NAT race/shuffle ×3
passed. Windows final vet, checkptr/shuffle ×20 (6.255 seconds) and native ×3
also passed. Results, initial failures, tested source and executable are retained
in external lab `evidence/w06-plaintext-bridge/`. Documentation changes follow
the frozen source archive.
The Windows-built checkptr executable SHA-256 is
`5e52b4a459b47239ff435824f494691be68e45914bd05017e4a3a2cec7c5c263`.

This is a staged component: bridge composition with the native NAT/Core lease
adapter and product runtime remains open. IPv4 options/fragments and IPv6 are
explicitly dropped here; protocol parity, full fail-closed lifecycle and Windows
release readiness remain unproven. This is not a full W06 PASS.

## W06 — composed bridge, Core leases and native physical NAT

`exitnat.BridgedResources` connects the new plaintext bridge to the existing
Core resource activator/renewer/revoker and Windows NAT backend. Forwarding is
denied until activation finishes. Reply delivery runs inside the NAT lease
fence and queues only to the exact bridge attachment. Revocation first fences
NAT, then retires queued/returned plaintext, removes the authenticated AWG peer,
and releases the address. Failed removals retain ownership for cleanup retry;
lookup locks are not held while joining AWG workers. Callers must keep AWG
readers alive until resource cleanup succeeds, then close transport/adapter.

The encrypted five-client scenario now uses this composition directly instead
of a harness plaintext pump. It retains real AWG, Core activation/renew/RELEASE,
source-spoof rejection, concurrent forbidden SMTP traffic during RELEASE, and
continued service for the other four clients. The memory variant deliberately
loses and duplicates replies. New focused tests cover failed install/removal,
cleanup retry, denied pre-activation data, control-address collisions and
constructor rollback. Runtime/backend health errors are checked before DNS
application retransmission; no product retry policy changed.

The first integration attempt correctly rejected a fixture lease for its own
control address, `10.17.0.1`. The fixture had not needed to reserve that address
before it had a real control/host boundary. It now supplies allocator `Reserved`
just as the Linux Exit composition does; the collision guard remains tested.
This was a harness configuration failure, not relaxed production validation.

Mini-PC vet and race/shuffle ×10 passed (9.666 seconds). Windows vet and
checkptr/shuffle ×10 passed. The elevated native backend completed 10/10 five-AWG-client runs:
140 validated public DNS replies, one bounded application retransmission,
renewal, RELEASE isolation, spoof rejection and cleanup passed. Temporary driver
service was stopped/deleted; Secure Boot, Defender and all firewall profiles
remained enabled. These runs use channel host TUNs and test-coordinated keys and
endpoints: the preceding native-Wintun proof is separate, not a claim that the
entire Wintun/NAT/Core/signaling/product path has run together.

Evidence: external `evidence/w06-bridged-leases/`.
Frozen source SHA-256:
`463d4e3358f9779966b8fde6bd10764b5608093f24fe382214c2933d7a49ea69`.
Windows checkptr executable:
`f01e24cb0b31414b267e6028dc15dbd7adbc29ae8ebda87896929e1632e5bc02`.
Documentation changes follow these builds. Full product composition, pending
ICE/signaling sessions, native own-upstream coexistence with physical NAT,
TCP/ICMP/PMTU, route generations and W06/product acceptance remain open.

## W06 — production TCP session-control correction

Inspection before runtime extraction found a real defect in the staged Windows
bridge: the host exception accepted UDP, whereas existing `m12_exit_linux.go`
creates a **TCP** `sessioncontrol.ListenTunnel` listener for Noise
BIND/RENEW/RELEASE. The earlier UDP fixtures proved packet separation only;
their "control" label was not evidence of production session-control parity.
Linux runtime and deployed Linux nodes were not changed by this staged defect.

The bridge now accepts host-bound control only for well-formed IPv4 TCP at the
exact configured address/port, and returns control packets only from that TCP
source to the exact downstream attachment. UDP at the same endpoint, wrong
ports/addresses and malformed TCP headers receive no host exception. No Noise,
ICE, AWG or TunnelLease protocol was changed. The focused TCP regression fails
3/3 against the prior production bridge and passes with the correction.

The native fixture now drives bounded TCP handshakes and payload exchange from
five AWG peers through Wintun into an actual Windows TCP listener, alongside
the existing own-upstream and downstream packet tests. The initial native
run without an ingress firewall allowance timed out before SYN-ACK. Holding
the binary constant and allowing only the test executable, TCP/7890, local
`10.253.246.1` and remote `10.253.246.0/24` made 3/3 native runs pass. This
controlled comparison identifies host firewall policy as a separate integration
requirement, not a reason to disable protection globally.

`tools/windows/w06/bridge-check.ps1` now owns that bounded lab rule and removes
it afterward. Final source checks: mini-PC vet plus race/shuffle ×20 for bridge
and NAT passed (3.814 and 20.541 seconds); Windows vet, checkptr/shuffle ×20 and
native ×3 passed. Final cleanup reports adapter delta=0, route residue=0,
rule residue=0, Secure Boot/Defender/firewall enabled. Evidence and original
failure logs are in external `evidence/w06-tcp-control/`.

Frozen source SHA-256:
`231ce4236077c35e7070b4f1e38b0f56e2f74f38ea4144922ba7299979577d3e`.
Windows checkptr executable:
`c77ef5132b2261bf34e0f80b8419aa5576bf2bf9892c23019ec2282ba4492817`.
Documentation changes follow the frozen source. The native raw TCP fixture is
**not** Noise BIND proof. Product-owned WFP ingress, actual Noise control across
the composed Exit bridge, and common Exit/runtime extraction remain required;
no Windows readiness gate is bypassed by the test firewall rule.

## W06 native Windows Firewall control exception

The staged bridge no longer needs a PowerShell ingress allow rule. New
`internal/winfirewall` uses `INetFwPolicy2`/`INetFwRule` directly and permits only
the current executable, captured virtual interface, exact private IPv4 TCP
control endpoint and bounded downstream prefix. It captures LUID/GUID/name and
checks actual local-address ownership before installation. Readback verifies
scope before adoption/removal; changed rules are preserved with an error.
The same-name precheck is important because the native
[Add API can overwrite an existing identifier](https://learn.microsoft.com/en-us/windows/win32/api/netfw/nf-netfw-inetfwrules-add).
Interface restrictions use the API's
[friendly-name representation](https://learn.microsoft.com/en-us/windows/win32/api/netfw/nf-netfw-inetfwrule-put_interfaces).
This is not atomic protection against a concurrent administrator changing policy.

Native development exposed two configuration details: a fabricated interface
name is rejected, and explicitly setting an empty service name allows detached
property assignment but fails rule Add with `E_INVALIDARG`. Tests now use an
existing interface, and the absent service restriction is left unset and checked
as empty on readback. Neither failure was a network-protocol defect.

The same bridge executable passes three real Wintun/AWG TCP runs with the native
owner and no lab allow rule. A separately installed explicit administrator TCP
block causes the expected SYN-ACK timeout; removing that block restores the
ordinary policy, rather than using a forced WFP permit to bypass it. Cleanup
reports adapter delta=0, routes=0, rules=0; Secure Boot, Defender and all firewall
profiles remain enabled. Earlier incomplete log observation did not indicate a
teardown hang: all three runs completed normally.

Native control lifecycle: three repetitions of 100 measured install/remove
cycles PASS, including idempotence and refusing to modify/delete a deliberately
changed port. Warmup requires 20 consecutive cycles without a new handle-type
highwater within a maximum of 100 cycles; monotonic growth cannot pass warmup.
Final samples are flat in runs one/three; run two adds one Thread/Event pair.
File=17, ALPC Port=7, Key=16 and EtwRegistration=65 stay unchanged. No forced GC
or driver/service restart is used. This is component resource evidence, not
100 full CONNECT/DISCONNECT cycles.

Two earlier resource failures are retained: fixed 20-cycle warmup did not reach
thread stabilization, and the shared test oracle incorrectly treated a decrease
from 20 to 19 Thread handles as five extra handles. The latter arithmetic bug
is corrected in `wintest.ResourceGrowth`; regressions prove both that falling
counts pass and that fewer threads cannot conceal a File leak. The final
Windows vet/checkptr/shuffle x10 covers all nine affected packages, including
the existing helper's users. Mini-PC bridge/NAT vet and race/shuffle x10 pass.
The COM by-value VARIANT also has explicit 16-byte x64 ABI alignment and a
focused alignment/serialization check; no Go runtime thread is terminated.

Evidence: external `evidence/w06-native-control-firewall/`. Frozen final source
SHA-256: `8c40e085506cbb2b3efdb992fb69b807db95f89d8eb51b896525e613027edb7e`.
Native checkptr lifecycle binary:
`673cae43b88fc202911a318b170abf1fa009bed6590e172b097fa373da48ea6b`.
Native bridge binary:
`236c3c7022c4f625f196e7a4b977d9e534d931e1b665122ab0cfa72acfa34cc4`.
Documentation follows the source freeze.

Still required: journaled rule ownership and crash/reboot cleanup, full runtime
composition, real Noise BIND/RENEW/RELEASE through the composed Windows Exit,
and readiness under administrator/group-policy restrictions. This backend is
Windows amd64/IPv4 only; other architectures fail explicitly. Also inspect the
existing WFP worker's locked-Go-thread exit before runtime integration: it still
uses the thread-termination pattern implicated in the separate WinDivert pending
I/O defect. Its presence is a follow-up risk, not a claimed new reproduction.

## W06 durable control-rule recovery

Baseline `55e0286`. `OpenOwnedControl` now composes the native ingress exception
with existing `winfile` protected directories, exclusive locks and atomic durable
writes. The exact owner GUID, tunnel GUID, executable, interface and endpoint/pool
are journaled before mutation. Cleanup clears the record only after matching
native scope is removed; native cleanup or journal-clear failure retains a
retryable owner. Recovery cannot acquire a live owner's journal, does not create
rules, and preserves both a changed rule and its record with `ErrConflict`.
Replacement startup reclaims old recorded scope before creating a new owner.

The first real process-kill experiment passed with a live adapter but failed
after TUN removal: Firewall returned the removed interface as its GUID rather
than its old friendly name. This failure is preserved in `control-crash-v1.log`.
The exact lab-only orphan was inspected and removed explicitly. Verification
now accepts the recorded GUID or a current friendly name resolving to that GUID;
a reused name pointing at another adapter is not accepted. No arbitrary
interface string or renamed foreign rule is treated as owned.

Final native checks pass three repetitions of both crash cases (six forcibly
killed child processes). They prove live-journal exclusion, residual rule and
intent existence after kill, refusal to remove a deliberately changed port,
record retention on that refusal, exact cleanup after adapter loss and fresh
ownership on replacement. Unit fault injection covers failed write-before-add,
partial installation, cleanup failure, clear failure and retry. File tests
cover exclusive ownership, durable reopen, unknown/version/trailing-data refusal.
The encrypted five-client bridge passes x3 using the journaled owner instead of
the previous bare native rule. Native cleanup has adapter delta=0, rules=0 and
routes=0; Secure Boot, Defender and all firewall profiles stay enabled.

Windows vet and checkptr/shuffle x20 pass for winfirewall, winfile, exitbridge
and exitnat. Mini-PC affected vet and race/shuffle x10 pass. Tests did not run
on the Ubuntu workstation and its proxy/VPN/routes were unchanged.

Evidence: external `evidence/w06-control-recovery/`. Source SHA-256:
`bc2bd05ee625a564f1b011d62002757cc2f1237963c05f6478d98459894d867f`.
Recovery test executable:
`50a27686bce03ce8dea36c4662aeb6154fb7548936ed08cdbe369e57049c7023`.
Journaled bridge executable:
`bfccd39724f6a9bc24febcd13f388810db4312ce4d8ec691ba8d0e27650f2d74`.
Documentation follows the source freeze. This closes component-level durable
ownership, not full daemon crash/reconnect, real reboot, boot-recovery service
wiring or in-tunnel Noise BIND/RENEW/RELEASE. Those remain separate gates.

## Shared Exit protocol composition and immediate-shutdown regression

The Exit protocol owner, allocator/lifecycle wiring, M12 services, Private
admission and rotation coordination now compile as common runtime code. A narrow
platform factory supplies the packet backend. Linux still constructs its
existing nftables/forwarding/TUN implementation; its concrete traffic readers
remain Linux-specific. No Windows-ready stub or new wire protocol was added.
The production Windows Exit factory and full daemon composition remain open.

The new constructor test covers resolved defaults, missing/failed factories,
nil backend refusal, cleanup after M12 readiness failure, protocol service
creation and immediate/idempotent shutdown. Its first run had an invalid test
SignalConfig; this fixture error is preserved separately. Once corrected, the
test exposed a real pre-existing shutdown race: Close cleared iceService before
the run goroutine read it. Windows panicked and Linux race instrumentation
identified the same read/write. The worker now receives its service reference
at goroutine creation, independently of cleanup's mutable owner field.

Windows affected vet and checkptr/shuffle x10 pass for runtime, sessioncontrol,
tunnellease and sessionestablish. Immediate construction/shutdown passes x100.
Mini-PC affected vet and race/shuffle x10 pass for the same packages. Linux
production daemon and privileged runners were built on the mini-PC.

Real netns results with the frozen Linux daemon:

| Scenario | Result |
| --- | --- |
| Brokered M12 Internet and Rendezvous-independent renewal | PASS, 42.16 s |
| Dual-role independent tunnel control | PASS, 16.34 s after harness repair |
| Private invitation, HTTP/UDP/DNS/ICMP, reconnect and revoke | PASS, 59.71 s |
| Two concurrent Private clients and full host leave | PASS, 47.50 s |

The initial dual-role run was LAB INVALID: the legacy harness selected nsenter
only for protected public nodes, while production also requires host cgroup v2
for protected Exits. ip-netns-exec hid that mount, causing the explicit
unsupported-cgroup refusal before readiness. The harness predicate now includes
Exit, matching production; its regression passes race x10. The other three
scenarios passed in that initial run. Only the affected dual-role scenario was
repeated with the corrected runner. Both runs report host_integrity=true and
clean namespace teardown. Private scenarios retain their existing lab DNS
substitution; they are not a new production DNS acceptance claim.

Evidence: external `evidence/w06-shared-exit-core/`, including the original
fixture error, actual shutdown panic/race, lab failure and successful rerun.
Source before harness repair:
`0e706a513e7528d5ebef936cdc595a167d7f1c6b152a9eee33c00608bc34f65d`.
Source including harness repair:
`6931eac47323d2a76f03a02cc09b5eea8f8bd9c389a590f924bf1c25843dadc3`.
Linux daemon:
`67624b42ce2877f87f043885921167acc2a1d367900fe0f4029e7ad59e93d2c3`.
Corrected netns runner:
`0b89f977107e340a9b46981690451a0699dc8e52403038a4cce8f7be5ea90843`.
Documentation follows source freeze. No workstation tests or proxy changes.

## WFP transactions preserve unrelated pending Windows I/O

A targeted native regression confirmed that the WFP transaction goroutine could
reuse a Go OS thread that previously submitted an unrelated overlapped named-pipe
operation. Exiting the locked goroutine terminated that thread and aborted the
pending operation. Three pre-fix repetitions failed on cycle zero with
ERROR_OPERATION_ABORTED. This is a real composition defect, not a timeout or a
simulated failure; it must not reach the integrated daemon.

The already-tested WinDivert native-worker implementation is now shared as
`internal/winthread`. WFP runs its transaction on a newly created native thread
and joins its actual termination, preserving RPC thread-local cleanup without
killing a reused Go runtime thread. WinDivert uses the same implementation.
WFP rules, ownership checks, transaction/commit/cancellation semantics and wire
protocols are unchanged.

The fixed pending-I/O regression passes x20, with 100 transactions per repeat.
Windows affected vet and checkptr/shuffle x10 pass for winthread, wfp, windivert,
killswitch, exitnat and runtime. Native WFP suites pass x3, including ownership,
rollback, native condition roundtrips and three 100-replacement resource runs.
Process handles were 190→191, 194→196 and 196→201; per-type resource oracles
pass, including their allowance for Go runtime thread-related handles. The
unprivileged-account attack test was skipped here because no probe account was
configured; this run does not renew that separate acceptance claim.

Native pinned-driver load, overlapped cancellation/close and deadline tests pass
x3 after helper extraction. The absent-driver phase also confirms runtime does
not install the driver itself. Driver/service fixtures are removed afterward;
remaining driver count is zero, Secure Boot and Defender remain enabled and all
firewall profiles remain enabled. This targeted driver regression does not
claim a new encrypted-traffic or full Windows Exit acceptance run.

Evidence: external `evidence/w06-wfp-worker/`, including before/after logs.
Frozen source SHA-256:
`7ad728fd877e02cc74b1bc93e35bfc081136e23fd6188d4f16e2c16d541f04f7`.
Pre-fix executable:
`2b628f970a88408300524675382a77ca03cb4d99c34e6926beea4466dc52a8e9`.
Fixed WFP executable:
`04cdcf865824d350541625c854eb1b7cd42d904261b8513793eb01674562868f`.
Documentation follows source freeze. Tests ran only in the Windows VM; no
workstation proxy/VPN/routes were changed.

## Windows Exit adapter/control owner and dual-role source selection

The Windows runtime now has a native long-lived Exit adapter/control-rule owner.
It validates the pool before mutation, creates Wintun, excludes its static Exit
control address from automatic source selection, and installs the journaled
exact-interface TCP control rule. Failed cleanup retains ownership for retry;
the adapter cannot disappear before its control rule has been released.

Native source-selection tests confirm the kernel initially chooses the static
control address instead of the upstream lease address. Excluding only that
owned address fixes selection while explicitly bound control traffic still
crosses Wintun. Removing the upstream address does not fall back to the excluded
control address. This is not a change to the wire protocol or MTU.

Affected Windows vet and checkptr/shuffle x10 pass for tunnel, runtime,
sessioncontrol, winfirewall and winroute. Native source selection passes x3;
native device ownership passes x3 with two create/close cycles per repeat,
real tunnel control listeners and competing journal-owner rollback. Final
adapter and route deltas are empty, remaining control rules are zero, and
Secure Boot, Defender and all firewall profiles remain enabled.

Evidence: external `evidence/w06-exit-device/`. Frozen source SHA-256:
`756371c235ec0571fef4f170c94cee3c564922da31732e154aea115796417597`.
Linux runtime build compatibility also passes; no workstation tests were run.
The full generation-local Windows Exit plane and daemon composition are still
pending, so these results do not establish full Windows CLIENT+EXIT readiness.

## Exit shutdown retains failed resource ownership

Before composing the Windows generation plane, focused tests reproduced two
shared lifecycle defects three times on the mini-PC: failed resource revocation
permanently closed the lease lifecycle, preventing cleanup retry; and Close
ignored its caller's deadline while waiting for an in-flight activation.
Separate ExitServer regressions reproduced premature plane closure after worker
timeout and loss of plane/lease ownership after failed cleanup.

Shutdown now fences admissions and renewal once, joins existing workers through
one retained completion signal, and serializes retryable lease cleanup. Failed
leases remain quarantined and their addresses remain reserved. ExitServer keeps
its lifecycle and plane until each preceding teardown stage succeeds; worker
timeout cannot close a plane still in use. This does not change the wire
protocol, allocation, discovery, selection or normal lease TTL semantics.

Mini-PC affected vet and race/shuffle x10 pass for tunnellease, runtime,
tunnelsignal, sessioncontrol and sessionestablish. Windows affected vet and
checkptr/shuffle x10 pass for the same packages. Tests cover failed revocation,
retry, activation deadline, concurrent Close, renewal rejection during shutdown,
worker completion ordering and idempotent successful closure.

Four real Linux netns scenarios with the rebuilt production daemon pass:
dual-role independent tunnel control (15.77 s), brokered Internet and direct
renewal after Rendezvous loss (43.02 s), Private invitation/traffic/reconnect/
revoke (58.81 s), and full host leave with two active Private clients (47.37 s).
The runner exits zero and reports host_integrity=true. Private fixtures keep
their existing lab DNS launcher; this is not a new production DNS gate.
Linux daemon SHA-256:
`a7c0bf7c46543d815006342c90fc0df0591da2c31be918604c46156ad5b127c0`.

Evidence: external `evidence/w06-exit-shutdown/`, with before/after logs.
Frozen fixed source SHA-256:
`b86f1ef43bd9589c4f88688b4840b925a2cd411baae30e8971b5ce084568b9e1`.
This proves retryability of these owners, not full daemon crash/reboot readiness
or arbitrary backend constructor-failure recovery. Tests ran only on the
mini-PC and Windows VM; workstation proxy/VPN/routes were not changed.

## Windows generation-local AWG/bridge/NAT composition (partial gate)

The new runtime owner composes one AWG transport, one borrowed TUN plaintext
bridge and the native lease-owned NAT backend. Downstream promotion rechecks
the existing signed binding registry and exact peer/session/network generation;
renew/revoke retain Core lease revisions. Own upstream uses a separate bridge
attachment on that same AWG device. Failed peer installation cannot remove an
already installed peer. NAT cleanup precedes AWG reader shutdown, with failed
cleanup ownership retained for retry. ICE sessions, routes/DNS, the long-lived
adapter and full ExitServer composition remain the caller's responsibility.

An initial integration test caught a redundant borrowed-TUN Close. The new glue
now guards borrower closure and releases its fallback reference once bridge
cleanup succeeds. A separate initial native run was LAB INVALID: the fixture
omitted the physical source from its generation snapshot. Production correctly
refused that configuration; the fixture now supplies its real interface/source.
Both failures are preserved, not counted as successful acceptance runs.

Final affected Windows vet and checkptr/shuffle x10 pass for runtime, exitnat,
exitbridge, awg and tunnel. Channel-TUN tests exercise five encrypted downstream
peers alongside own upstream, no downstream-to-upstream delivery, own return
traffic, independent revoke/disconnect, duplicate installation rollback,
stale binding/pool/session rejection, late construction and failed NAT Close
followed by successful retry. The physical packet oracle is in-memory for these
tests, while AWG, the lease translator and binding validation are real.

Native runs use real Wintun, AWG and WinDivert, an independent physical DNS
preflight, five clients sharing the same inner source port, and isolated revoke.
The first valid native batch passed 3/3. A later batch passed 2/3 with one missing
UDP DNS reply. Ten diagnostic repetitions then passed 10/10 (60 encrypted DNS
round trips), without retries or production changes. The final stricter oracle
uses random per-run query IDs and full reply-tuple/RCODE validation: 9/10 PASS,
one timeout. **The native gate is not clean; do not report it as 10/10 PASS.**

The reproduced timeout is on the second client after the first succeeded:
both AWG handshakes completed, two NAT flows/mappings were created, no mux errors
or queue drops were reported, and the second peer received no encrypted reply.
The NAT backend reports no terminal error. This narrows the unresolved loss to
the physical NAT/oracle/reply-delivery segment; it does not identify the cause.
The separate packet trace belongs to the successful diagnostic batch and cannot
explain the failing packet. Classification remains NOT ENOUGH EVIDENCE, not an
asserted lab artifact or confirmed production defect. Do not hide it by adding
retries or increasing the product timeout.

All native cleanup artifacts show zero adapter/route delta, zero control rules
and zero remaining driver services; Secure Boot, Defender and all firewall
profiles remain enabled. PktMon is stopped with no filters left. No workstation
tests or proxy changes occurred. This is not a discovery/ICE/BIND, ProtectedMode,
full daemon or full Windows CLIENT+EXIT acceptance claim.

Evidence: external `evidence/w06-exit-generation/`, including all failed batches.
Final diagnostic source SHA-256:
`8d6bfe3b5996a2466add20086559e68609ac7d4799a7ea795c34453b971d29b0`.
The production generation source is unchanged across the final and diagnostic
batches: `cacbd5534653c82200ec3dffd403081a862257e8b44696c0baacc598f32a5c66`.

## Exit construction rollback ownership (Windows composition prerequisite)

A focused test reproduced two ownership gaps three times on the mini-PC:
a platform factory returning a partial plane plus an error was never cleaned up,
and a later protocol-construction error discarded the server even when plane
cleanup failed. Both prevent retrying native adapter/firewall cleanup safely.

The common ExitServer constructor now adopts the plane before inspecting the
factory error, cancels its work on failure and attempts cleanup with a bounded
context. Successful rollback still returns nil; failed rollback returns the
fenced server together with the original and cleanup errors, for Close only.
The Linux daemon retains that result before its error branch so its existing
shutdown path can retry. No readiness publication or protocol changes were added.

Four regression cases cover early/late construction failure crossed with
successful/failed rollback, persistent cleanup failure, successful retry and
idempotent Close. Affected runtime/tunnellease vet passes on both platforms;
Windows checkptr/shuffle x10 and mini-PC race/shuffle x10 pass. These tests use
injected cleanup failures; they do not prove arbitrary native rollback recovery.

Four real Linux netns regressions also pass: dual-role independent control
(15.86s), brokered Internet/direct renewal (42.06s), Private invitation,
traffic/reconnect/revoke (58.63s), and two Private clients/full host leave
(47.62s). The existing Private lab DNS launcher remains part of those fixtures.
The runner reports exit_code=0 and host_integrity=true. Linux daemon SHA-256:
`fa9e22f8f6d84549960b471d4f663d2817a3678cb9545c64350b95e9beaf7197`.

Evidence: external `evidence/w06-exit-construction/`, including the before-fix
failure log. Frozen source SHA-256:
`b62f2ba5a92e012a3913ea5a20e8d42006f7b8dbe3539489cbf264138c9d9ce6`.
The previous generation native UDP timeout gate remains open, independently of
this constructor fix. Full Windows ExitServer/daemon composition is not complete.

## Generation DNS forensic repeats with VM-boundary packet capture

Two subsequent batches use the unchanged v5 native test executable and its
strict one-packet/no-retry oracle: 20/20 and 20/20 pass. Each batch exercises
five encrypted downstream peers, six public UDP DNS exchanges per iteration,
isolated revoke and full cleanup. A temporary QEMU filter-dump on this VM's
net0 captures packets outside the guest network stack; it does not alter the
workstation proxy, routes or VPN. Both capture filters were removed afterwards.

The independent packet parser matches all 240 logged queries to outbound and
inbound tuples at the VM boundary, with no missing or ambiguous matches. Maximum
observed query/reply time there is 166.726ms in the first batch and 134.189ms in
the second. Both harnesses report zero adapter/route delta, zero control rules
and no remaining driver service; Secure Boot, Defender and firewall stay enabled.

These successful captures validate the capture method and show repeated traffic
success, but do not explain the older missing reply. The previous failures remain
NOT ENOUGH EVIDENCE; no retry budget, product timeout or native forwarding code
was changed to obtain these results. Full Windows CLIENT+EXIT/protection/discovery
acceptance remains open. Evidence, packet parser, raw captures and parsed query
summaries are external under `evidence/w06-generation-pcap/`; raw packet data is
not committed or published.

## Protected Exit control reply lane (composition prerequisite)

CLIENT+EXIT needs a narrow reply path for its own Exit listener while ordinary
host traffic is still blocked during upstream pre-BIND/NO_EXIT. The previous
policy had an upstream control grant only. A new independent ExitControl lane
constrains TCP, exact owned tunnel interface/local address/listener port and
private inner pool. It cannot share a physical underlay interface or silently
introduce a different tunnel interface. The ALE rule also pins daemon application
and privilege; the transport rule covers accepted-socket replies. It does not
permit arbitrary physical WAN or the whole tunnel before BIND.

Underlay and upstream policy updates preserve this lane; removal/re-creation of
ProtectedMode drops it and invalidates stale updaters. Caller-owned pointer data
is copied. The Windows Exit device can own its grant after protection starts and
revokes it before firewall/adapter teardown; failure retains ownership for retry.
The constructor was not wired at that checkpoint; the following composition now invokes it.

Compiler tests check scope, deterministic rules, invalid grants, independent
updates, failed transactions and no resurrection. Device tests cover failed
grant publication and failed revocation with retained resources. Affected native
Windows vet/checkptr/shuffle x10 passes. The native TCP test proves baseline
SYNACKs, blocked replies without the grant, permitted exact replies with it,
wrong-port/out-of-pool rejection, own pre-BIND/CONNECTED/recovery transitions,
ordinary TUN traffic rejection before activation, withdrawal and cleanup.
Initial and frozen-source batches each pass 3/3; the existing native IPv4/IPv6
pre-BIND authorization regression also passes 3/3, including foreign executable
and non-admin rejection. These tests are not a complete Noise/ICE/lease session.

An initial harness attempt stopped before network mutation because its disposable
service fixture was absent. After creating that exact fixture, the native tests
ran; the outer runner removed the fixture afterwards. This is a lab prerequisite,
not a product failure.

The final extended native gate also passes 3/3: an independent TCP/UDP echo
oracle on the mini-PC sees all 12 baseline and 12 restored requests, but none
from the protected/recovering phases, for both the daemon-path executable and
the separate foreign executable. Existing ProtectedController native regression
passes 3/3, including same-executable non-admin denial, NO_EXIT/revocation,
IPv4/IPv6 tunnel traffic and blocked Safety Policy tuples. The oracle sees no
forbidden-stage requests from either test. The ephemeral oracle was stopped and
its ports verified closed. Workstation proxy/VPN/routes were unchanged.
Final inspection finds no owned WFP markers, fixture services, probe users,
control firewall rules or test adapters. Secure Boot, Defender and all Windows
firewall profiles remain enabled.

Frozen extended source SHA-256:
`24245484d369b1e7cc7972088f26b944ba3c661bd63539d2377f5c0b298cae60`.
Evidence: external `evidence/w06-exit-control/`. These are component gates, not
full Windows daemon, production M12 readiness or complete CLIENT+EXIT acceptance.

## Windows ExitServer and own-upstream composition

Windows now supplies `NewExitServer` to the existing common lease/M12 services.
Its plane owns pending ICE sessions, generation-local AWG/bridge/NAT, lease
activation/renewal/revocation, local AWG rotation, and the long-lived adapter.
The installed protection updater also owns the exact Exit control reply grant.
Network reconciliation fences the previous generation before transport migration;
a still-current old transport cannot reopen the retired physical NAT path.
Partial generation construction/commit/teardown failures retain their owner.

Own upstream is a staged installer on that same adapter and generation, not a
second independent client dataplane. It validates the signed binding and lease,
adds its exact source address, waits for AWG, opens the narrow control route,
executes the existing BIND callback, then activates routes/DNS/user protection.
Cleanup closes control after the connection-owned RELEASE, revokes the upstream
grant while preserving ExitControl, restores DNS/routes, and removes only the
own peer/address. Failed route/DNS Close retries the existing durable journal
recovery because those native managers release their lock on the first Close.
Old staged handles cannot close a replacement. Pending and committed remote key
rotation both retain the exact AWG cleanup keys.

Component evidence:

- Windows native vet and runtime checkptr/shuffle x10 pass on the initial and
  generation-fenced snapshots (24.686s / 25.619s), then on the final snapshot
  including key rotation regression (37.783s).
- Real encrypted AWG tests exercise five downstreams plus own upstream, selective
  downstream removal, own cleanup without removing other clients, 13 injected
  setup/cleanup failures, failed promotion plus failed rollback, stale stage and
  network fencing, generation commit failure and journal recovery retries.
  These use channel TUN and controlled route/DNS/WFP dependencies, not native
  protected CLIENT+EXIT acceptance.
- Remote AWG rotation passes 10 repetitions, both abandoned staging and completed
  new-key handshakes; Close removes old/new keys without residual peers.
- Native ExitServer composition passes two independent 3/3 batches using signed
  Wintun/WinDivert, the real common constructor/M12 control listener, five
  allocator-owned leases, six encrypted public DNS exchanges and selective
  RELEASE. Its ICE endpoints are supplied by the fixture and lease activation
  is invoked through the lifecycle: this is not a brokered discovery/BIND test.
- Both native batches leave zero adapter/route delta, zero control firewall
  rules and no WinDivert service. Secure Boot, Defender and firewall remain on.
- Mini-PC Linux affected runtime vet and race/shuffle x10 pass (91.341s).
  The only common production change is the additive platform configuration field.

The first packet assertion incorrectly expected an unchanged TTL; observed
headers differed only in the two legitimate forwarding decrements and resulting
IPv4 checksum. The corrected oracle checks the complete expected forwarded packet.
No product timeout/retry was increased. Earlier unexplained native UDP outliers
remain recorded above; these successful runs do not retroactively explain them.

External evidence: `evidence/w06-exit-server/`. Native generation-fenced source:
`ab2ac4ae6aff94de57811feb503e4febea99d2bd91171fff34885b647e4e292d`.
Final source including rotation regression:
`d4b63fd8b5f40cb327a17179cfa05890ef9d23ef8c6b592d7334eac2bb0b8b1e`.

Full Windows `cmd/irviod` still fails to build: product boot/runtime/daemon
composition and platform-specific CLI credential policy are not yet connected.
This is an explicit unfinished port, not a release binary. Native protected
own-upstream plus concurrent downstreams, real M12 discovery/BIND, daemon
composition and all later acceptance gates still require implementation/evidence.
Workstation networking and proxy were not changed; tests ran only in the VM and
on the mini-PC.

## Protected Exit plus real upstream BIND: native thread ownership fix

The new dual-role native gate composes the Windows ExitServer with five active
AWG downstreams and an own upstream to a disposable Linux peer on the mini-PC.
Own upstream uses real Noise BIND/RENEW/RELEASE and the Linux lease authority;
an authenticated request with the wrong session token is rejected. Endpoints
are fixture-supplied and downstream activation is lifecycle-driven: this does
not claim brokered ICE, discovery, product daemon, or Private VPN acceptance.

Two implementation defects were found and fixed:

- The Windows Exit constructor attempted to open its `control` journal below
  a missing runtime directory. It now creates/validates the final directory
  through the existing secure Windows directory helper, owns its handle, and
  closes it after device cleanup. No permissive recursive directory creation.
- DNS and route operations retired locked goroutines on reused Go OS threads.
  A thread could still own an unrelated pending WinDivert read; terminating it
  produced `ERROR_OPERATION_ABORTED` and stopped the Exit NAT backend. Both
  wrappers now use the existing dedicated native-thread helper, preserving
  DLL thread-local cleanup without terminating another I/O issuer's thread.

Focused regression creates real overlapped named-pipe I/O, then runs the actual
DNS/route worker wrappers. Before the fix, each failed 3/3 at the first worker
operation. After the fix, each passed 20/20, with 100 worker operations per run.
No added network retry, timeout increase, ignored native error, or relaxed WFP
policy was needed.

Integration evidence:

- Run 3 before the thread fix: first five downstream DNS exchanges passed;
  query 6 after own BIND never reached the NIC. NAT reported the native aborted
  I/O error; AWG/mux remained alive. The independent pcap corroborates this.
- Runs 4 and 5 after the fix: both pass; each has five downstream DNS exchanges
  before own BIND, five while own upstream is active, and five after own RELEASE.
  All 30 sends/replies match independent NIC captures, with no retry in the oracle.
- Own unbound user traffic selects the own lease address, not the Exit address.
  Ordinary physical-WAN DNS probes are blocked while protection is installed;
  captures show only the allowed baseline and post-cleanup physical probes.
- Both Linux peers confirm RENEW and RELEASE with zero remaining leases.
  Both Windows runs leave zero route/DNS/adapter deltas and zero control rules;
  temporary driver/owner services are removed. Secure Boot, Defender and
  Windows Firewall remain enabled.
- Native DNS restoration, external-change preservation, adapter removal and
  process-crash recovery pass. DNS 100 cycles keep handles exactly 259 → 259.
- Native owned route/control-route lifecycle, collision, external-change,
  adapter removal and both process-crash recovery gates pass. Route 100 cycles
  plateau after one Go runtime thread allocation (12 → 13); no continuing
  file/native handle growth. Combined host-state deltas are zero.
- The separate ordinary Windows DNS resolver/protected oracle is not rerun by
  the DNS component batch; its environment-gated skip is not counted as a pass.
- Mini-PC affected runtime vet and race/shuffle x10 pass (97.446s); the Linux
  peer/support packages compile and have no unit tests of their own.
- Windows affected vet and checkptr/shuffle x10 pass for runtime (36.870s),
  windns, winroute, winthread, wintest, windivert, exitnat, wfp and killswitch.
- Native WinDivert cancellation/close and deadline gates pass three repetitions;
  each cancellation test performs 50 warm-up plus 100 measured cycles, with
  bounded handles. The temporary driver service is removed afterwards.

External evidence: `evidence/w06-exit-bind/`. Frozen source SHA-256:
`73e0be0e8e0b80cade26ff08e0cbb08030026a8369243a111ae5e36f25531601`.
Native runtime test executable SHA-256:
`c1230bb20487aa9c3d339814ae7665a931eed07ca97493fc0fb84ab604d6eccf`.
Earlier unrelated unexplained UDP outliers are not retroactively classified by
these results. Workstation proxy and networking were not changed.

## Shared production factories and Windows client generation reconciliation

Production transport/Exit/M12 client factories, binding resolution, resource
ownership and the Exit transport pump now build from one Linux/Windows source.
Linux keeps its existing DNS wrapper and marked underlay coordinator; Windows
passes its native protection/journal/DNS dependencies into the actual installers.
Windows does not create a second DNS owner around a shared Exit adapter.
Global and exact-target Private candidate selection retain their common M12
connector and provider; no protocol, selection or lease algorithm was forked.

Connecting those factories exposed a missing standalone Windows client
`NetworkReconciler`. It now fences the previous generation and revokes user
tunnel permission before migration. Failed revocation propagates an error and
retains the fence; retry can revoke again. A stale staged attempt cannot promote
while the transport owner still references its predecessor. Duplicate events do
not revoke a session already established on the current generation. Linux keeps
its existing WAN-route reconcile; its new floor field remains zero.

Evidence:

- Windows production factory tests cover owned Global, shared Global and owned
  Private assembly with real discovery/M12 objects, correct exact target,
  preserved shared installer and exactly one native DNS owner. These are
  constructor tests, not a Private invitation/connect acceptance claim.
- Native `newProductionExit` (now the common factory, not a direct hand-built
  ExitServer call) passes 3/3. Each run owns five leases, performs six encrypted
  public DNS exchanges and selectively releases a client. Zero adapter/route
  delta and zero control firewall rules after cleanup. Endpoints and downstream
  activation remain fixture-supplied, not discovered/brokered.
- Native Wintun/AWG/WFP client migration passes 3/3 × three generations.
  Each generation proves working user traffic, then blocked traffic after
  reconciliation and rejection of a previously staged attempt. The next
  generation restores traffic; stale Close cannot remove the replacement.
  The harness verifies restored DNS/routes, removed adapter and owner service.
  This gate has a test BIND callback; real network BIND was covered separately.
- Windows runtime vet and checkptr/shuffle x10 pass (38.095s).
- Mini-PC Linux runtime/CLI vet and race/shuffle x10 pass on the initial snapshot
  (95.332s / 44.511s) and final production snapshot (92.905s / 44.586s).
- Linux `cmd/irviod` still builds. No test ran on the workstation; its proxy and
  host networking were unchanged.

Evidence: `evidence/w06-production-composition/`. Final production snapshot:
`53097f395182e3f7c305510d9fb1b7eae69e49b2db7a64765750edd68b10c228`.
Snapshot adding the native migration oracle (production code unchanged):
`9c9163d40a1f210c1b436a0fc959c68d03bde8f4be534deacd7360aa3e3561d2`.
Native migration executable:
`e13d62b1c54aac8a500a52e4af09a1c76d369f9a568d99abf2dda5fe15d9169c`.

This is not a completed daemon port. Windows still needs the daemon composition
root with dynamic, bounded WFP control-plane grants, accepted control replies,
native underlay probes and NetworkGeneration updates; product/SCM/GUI/installer
and full mixed-network acceptance remain open. The common factories are now
available to that root instead of requiring another copy of Core orchestration.

## Windows owned underlay grants and outbound control connections

The Windows WFP backend now exposes an exclusive underlay owner tied to a
protection installation and caller-supplied NetworkGeneration. Each connection
holds an endpoint lease; identical endpoints share one narrow rule set. Limits
are 128 distinct endpoints and 1024 references. Interface, source, destination,
port, protocol and daemon application restrictions remain in the existing policy
compiler; this does not introduce an ordinary-WAN bypass.

Generation advance withdraws predecessor endpoints before new acquisitions.
Close is retryable, including a native transaction that committed but failed
during verification or engine close. Failed cleanup retains ownership. Both
other policy-lane updates and installation reconciliation are fenced while an
underlay change is unresolved, preventing cached rules from restoring revoked
endpoints. Removing the installation invalidates all its leases. Closing only
the underlay owner leaves ProtectedMode installed.

`DialerForOwnedBindings` resolves binding and authorization together, releases
the grant after failed dial, and wraps a successful native TCP socket with a
retryable Close. Half-close retains authorization for remaining traffic. The
existing binding-only dialer is unchanged for unprotected paths. A real libp2p
bootstrap/DHT test verifies that node shutdown closes its owned TCP grants.

Verified evidence on the final reconciliation-fenced snapshot:

- Windows affected vet and checkptr/shuffle x10: killswitch 9.980s,
  controlsocket 2.408s, discovery 116.782s, runtime 39.385s.
- Mini-PC Linux affected vet and race/shuffle x10: killswitch 1.038s,
  controlsocket 1.034s, discovery 111.609s, runtime 95.450s. The later changes
  are Windows-only guards/tests and do not change those Linux sources.
- Native WFP/TCP gate: 3/3 (26.32s / 26.40s / 26.35s), each with 20 real control connections, duplicate
  endpoint ownership, revocation of an already-open flow and failed-dial
  cleanup. Ordinary non-admin TCP/UDP probes fail during protection and after
  owner-only close, then succeed after installation removal.
- Independent mini-PC oracle: exactly 81 expected records for the three runs;
  zero forbidden probe tokens, including the revoked TCP flow. The oracle
  checker rejects incomplete, duplicate and forbidden-record evidence.
- Native host cleanup: zero DNS, route and adapter deltas; test account and
  exact-owner WFP fixture service removed. This is not a full product resource
  stress test or a global no-leak claim beyond the exercised probes.

Initial harness attempts are not product failures: PowerShell parsed unquoted
test flags incorrectly before any test ran; a new unit fixture assigned different
CLIENT/EXIT TUN LUIDs and was correctly rejected until fixed. An oracle restart
briefly failed to bind while the deliberately revoked TCP flow remained in
Linux FIN-WAIT-2; no listener was alive, and restart succeeded after expiry.
No production safety condition was relaxed to accommodate those fixtures.

Evidence lives outside Git in `evidence/w06-underlay-owner/`. Final source
snapshot (including the reconciliation guard):
`6b74bc1ee34f26b9112461fd601b34a2c6111b2bec1528dc1a6102b8f8de5ebe`.
Final native executable:
`ee42610f10bd3a71566c53890511bbce21a1c511530e11462a6d54efe8b79d9b`.

This milestone supplies outbound control ownership, not a completed daemon.
Accepted control replies, dynamic UDP endpoint ownership, native underlay
probes and the Windows daemon composition root still need integration. Full
Global/Private desktop, SCM, server and installer acceptance remain open.
Workstation networking and proxy were not changed; tests ran only on the VM
and mini-PC.

## W06 continuation: owned incoming control listeners

Added native Windows TCP listener ownership for the existing libp2p transport.
The listener binds the selected source/interface with `SO_EXCLUSIVEADDRUSE`,
then acquires a generation-fenced WFP reply grant. Accepted sockets retain the
interface binding. Closing interrupts admissions, revokes the grant, and only
then releases the port. Failed construction or revocation retains the listener
reservation for cleanup retry. The shared transport adapter retains libp2p
security, muxing and resource admission rather than introducing a new protocol.

Reply rules match the exact interface/source/local TCP port at outbound
transport, without granting outbound ALE connect. Corresponding inbound ALE
rules reject other applications and non-admin instances of the same executable,
including when a stale grant survives without its original socket reservation.
Generation advancement withdraws listener and outbound grants together; their
reference counts and uncertain-transaction cleanup remain independently owned.

Native VM evidence:

- Five incoming-control repetitions passed before the tunnel-route extension.
- Three extended repetitions passed (14.69s, 14.01s, 13.99s), including a real
  Wintun adapter and split default routes. Kernel lookup for the external oracle
  selected Wintun while the explicitly bound control RPC and listener still
  exchanged traffic through the physical interface.
- Independent mini-PC oracle recorded exactly 83 expected probe results across
  these runs: owner replies succeed, closed/revoked listeners and foreign or
  non-admin stale-rule listeners cannot exchange the test payload. Baseline and
  post-cleanup non-admin probes succeed. This is scoped probe evidence, not a
  universal no-leak claim or an actual process-crash acceptance test.
- DNS, route and adapter baseline comparisons are empty; temporary firewall
  rule, service, test identity, mini-PC oracle and QEMU forwarding were removed.
- Windows affected vet and checkptr/shuffle x10 passed: wfp 2.058s,
  killswitch 10.058s, controlsocket 2.318s, discovery 119.806s, runtime 37.301s.
  This includes IPv4/IPv6 exclusive-port and accepted-socket binding checks,
  constructor/revoke failure retention, rejected listener address shortcuts,
  generation fencing, and real libp2p/DHT node close draining listener grants.
- Linux affected vet and race/shuffle x10 passed: controlsocket 1.039s,
  discovery 111.593s, runtime 92.324s. Later changes were Windows-only tests.

The first gate attempt passed twice and failed its third baseline-user echo
before protection was installed. The test echo closed with `linger=0` immediately
after writing; an application ACK was added to avoid truncating the probe's own
reply. Five subsequent repetitions and all three extended repetitions passed.
The original failed evidence is preserved; the initial RST race is a likely
harness explanation, not a proven production failure or an erased result.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-incoming-control/`.
Final source snapshot:
`055013a70b3b33f94a16dc3b6d875b1d1fc1d3d9dbdaacbd5a1729c56eb99c26`.
Extended native gate binary:
`98c14b61f239e7d9b3e9cd89435887bf0fc05935a6152f04b4842c05590200c0`.

The native incoming gate explicitly installs a disposable inbound Windows
Firewall allow rule so it can test the additional WFP identity guard. Production
firewall ownership and daemon composition must still install and lifecycle this
capability. QEMU IPv4 forwarding is not public-server IPv6/NAT acceptance.
No workstation VPN, proxy or routing configuration was changed.

## Windows daemon preparation: platform-owned reachability probes

Added `controlsocket.DialEndpoint`, `underlay.NewTCPChallengeProbe`, and
`exitselector.NewTCPProbe` using an explicit platform TCP factory. Windows can
use the existing source/interface binding and generation-owned WFP grants;
there is no DNS resolution or unbound/default dialer fallback. Linux marked
probes delegate to the same nonce/observation implementations while preserving
their routing marks. The shared runtime underlay wrapper now accepts the
existing `underlay.Prober` interface rather than a Linux-specific concrete type.

The raw challenge keeps the existing 32-byte `HYU1` nonce exchange, rejects a
wrong response, supports cancellation without an explicit deadline, and returns
authorization-cleanup errors. Candidate observations retain reachability,
latency, observation time and NetworkEpoch. A cleanup failure is reported, not
silently treated as successful resource disposal. No ExitSelector selection,
suppression, ACTIVE/RESERVE/COLD or protocol semantics were redesigned.

Verified affected gates on source snapshot v2:

- Windows vet and checkptr/shuffle x10: controlsocket 23.903s, underlay 21.965s,
  exitselector 20.469s, killswitch 28.651s, runtime 53.736s. This job overlapped
  the disposable native WFP gate; these are suite runtimes, not network latency.
- Mini-PC Linux vet and race/shuffle x10: controlsocket 1.033s, underlay 2.494s,
  exitselector 4.644s, runtime 92.525s.
- Native Windows WFP/mini-PC exchange, three repetitions: 52.63s, 47.37s,
  47.29s. Each repetition contains 20 nonce exchanges and 20 candidate probes,
  a wrong nonce, a silent endpoint, old-generation rejection for both probes,
  a successful new-generation exchange, and verified native grant removal.

The initial Linux vet caught a removed `bytes` import still required by the
challenge service; it was restored. The initial native assertion expected only
`context.DeadlineExceeded`; a TCP socket deadline can instead return a timeout
`net.Error`. The assertion now accepts both timeout forms, without changing
production timeouts or widening permissions. Initial failed logs are retained.

The final serial native gate uses the actual 750ms network deadline rather than
the initial 3s harness allowance: 3/3 pass, 47.05s / 47.10s / 47.17s. Across the
two successful gates the independent oracle has exactly 258 expected records;
old-generation attempts introduce no additional connection. Host DNS/routes/
adapters are unchanged and the disposable recovery service is removed.

However, this is **not** a full-operation 750ms latency PASS. Across 60 serial
samples, candidate dial (including native authorization) is 442.66–485.20ms,
median 459.19ms; candidate total including permission revocation is
884.52–955.84ms, median 916.61ms. Nonce total is 897.25–945.38ms, median 919.61ms.
Every full operation exceeds 750ms because mandatory cleanup follows the
completed network exchange. No product budget was increased to hide this.
Before wiring concurrent runtime probing, profile the native permission path
and verify the production concurrency budget; a serial PASS is insufficient.
`Store.filters` currently enumerates all system filters and then selects its
provider, a candidate for scoped optimization, not a measured root cause yet.
Any optimization must still detect disabled/foreign objects and preserve native
post-commit verification; the [Windows enumeration template](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter_enum_template0)
documents provider selection and disabled-filter inclusion.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-platform-probes/`.
Final source snapshot:
`4d8734f44d32503ce23ef2b28282f4c1f2fca74c3a0431e8ae6208b58e6c56df`.
Final native executable:
`6ae220743d2a422900480cf5b3f06af610dc368a3e0d86cbeb1a255318d9af4f`.
The last source change after the v2 affected suites is test-only: tightening
the native deadline and recording full-operation durations.

Daemon integration still requires dynamic UDP permission ownership, protected
versus hosting-only lifecycle composition, production Windows Firewall admission,
and the Windows product/service entry point. In particular these probes do not
yet constitute a running Windows desktop or server daemon.

## W06 follow-up — native WFP enumeration and five-way TCP probing

Baseline: `08e54ce`. Profiling on the same Windows VM locates approximately
132–145ms of each read in global filter enumeration, versus less than 2ms in
the service/root checks. Increasing enumeration pages from 64 to 1024 did not
materially help. A provider template with an empty layer GUID is invalid on
Windows (`FWP_E_LAYER_NOT_FOUND`); that exploratory fixture was removed using
its exact logged provider/sublayer. These unsuccessful experiments are retained.

The final implementation enumerates all current native layers (99 on this VM),
then queries its own provider per layer with boot-time/disabled inclusion. It
does not cache the layer list or restrict auditing to the six known product
layers. Provider/sublayer/marker checks, bounds, context cancellation, readable
native rule checks, explicit transactions and post-commit verification remain.
The SDK confirms `FWPM_LAYER0` size 72 and offsets 40/48/64. The flags and
provider/layer selection follow the [native enumeration contract](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter_enum_template0).

Scoped enumeration alone still missed the five-candidate/750ms budget in two
of three exploratory repetitions; caching DLL procedure wrappers did not fix
that and was removed. `ReplaceInstalled` now checks existing roots and readable
prior rules inside the write transaction, eliminating a separate preliminary
read without removing the precondition. It refuses absent protection and cannot
recreate it. The native post-commit read remains mandatory.

Final measured TCP gate: three independent repetitions, each containing 20
serial nonce/candidate pairs and five batches of five different endpoints.
Every network attempt uses the existing 750ms deadline. The independent mini-PC
oracle received exactly 204 expected records; all 75 parallel candidates were
reachable. No product timeout, selector ranking or wire protocol was changed.

| Measurement | Samples | Median | p95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| Serial candidate dial, including authorization | 60 | 105.32ms | 116.70ms | 139.62ms |
| Serial candidate including grant cleanup | 60 | 209.79ms | 229.21ms | 251.17ms |
| Serial nonce including cleanup | 60 | 213.37ms | 230.18ms | 241.42ms |
| Five-way candidate dial | 75 | 322.61ms | 550.90ms | 569.10ms |
| Five-way candidate including cleanup | 75 | 856.39ms | 1083.96ms | 1129.87ms |

This is a TCP probe concurrency PASS on this lab, **not** a 750ms total-cleanup
SLA: 54/75 parallel operations finish after 750ms while queued permission
revocation completes. Cleanup is still bounded separately and grants are drained
before the next batch. Performance under larger policies/other native workloads
and full runtime acquisition remains an integrated acceptance item.

Other final checks:

- Windows vet and checkptr/shuffle ten repetitions of `wfp`, `killswitch`,
  `controlsocket`, `runtime`: PASS (2.072/9.897/1.748/35.478s respectively).
- Three native WFP suite repetitions: PASS, including independent global-vs-
  scoped enumeration, 130 same-layer filters, unexpected layer and owner marker,
  actual boot-time filter, absent-policy refusal, rollback and pending I/O.
  Disabled inclusion is fixed by the SDK flag; this run did not create a new
  real disabled-filter fixture.
- Three 100-replacement resource checks: 190→190, 192→192, 198→200 handles.
  The last increase is one Thread and one Event; File/ALPC/timer counts stay
  unchanged, within the existing oracle, not a claim of zero process-wide growth.
- Real non-admin WFP mutation refusal: PASS, separately executed after the
  general native suite's credential-dependent ACL test was skipped.
- Three native owned-underlay repetitions: PASS. Non-admin ordinary TCP/UDP is
  blocked under protection and restored only after cleanup; stale/closed grant
  traffic is absent at the external oracle (81 exact expected packet records).
- Host route/DNS/adapter deltas are empty; temporary services/accounts and both
  mini-PC oracles were removed/stopped. No workstation networking was changed.

The first underlay oracle parser rejected mixed stdout/stderr. Inspection found
exactly three 60s `readline` timeouts, each on the previously successful `second`
connection after intentional grant revocation; no unexpected packet tokens.
The complete original log is retained alongside its derived JSON event stream,
which passes the unchanged strict 81-record checker. This is a lab logging
limitation, not a suppressed product failure.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-scoped-wfp/`.
Source snapshot SHA-256:
`02a4cc54c9a75665b5f7e017223e263cb4f1334d96898fe9c8913c693c4c67e3`.
WFP test executable:
`950bcd2edcccaac3ec6f03e64f56c1348ff541ed4c2bbde03fb38adeb7c314a0`.
TCP probe executable:
`4413f8b21b32a94e4c606981bd4be730a16339f323ee3ba67ef2d5a31153a898`.
This change is Windows-only; no Linux implementation was changed or retested.
Dynamic UDP/STUN/ICE grants and protected-versus-hosting-only lifecycle remain
the next composition work; this is not a runnable Windows product release.

## W06 — owned physical UDP and transport generation factory

Implemented exact physical UDP tuple ownership for the shared production
STUN/ICE/AWG socket: source, local port, interface LUID, executable and privileged
identity. Grants are reference-counted, generation-fenced and withdrawn with
existing TCP grants. No per-packet policy rewrites or protocol changes were added.

`transportgen.Config.MuxFactory` retains a permission-aware lifetime instead of
closing the raw mux before authorization is revoked. The Windows factory holds
exclusive socket reservations across failed construction/revocation and retries
cleanup through its parent owner. It rejects sources/interfaces that no longer
match the generation snapshot. Nil factory preserves the Linux construction
path; constructor cleanup errors are now propagated.

Final measured checks on Windows 11 Pro:

- Vet and checkptr/shuffle ×10: killswitch 21.512s, runtime 45.963s,
  transportgen 18.633s, socketmux 30.352s, stunprobe 13.610s, all PASS.
- Native UDP gate ×3: actual STUN, two remote UDP destinations, wrong local
  port, stale generation, foreign executable and non-admin socket reuse.
  Independent mini-PC evidence has exactly 186 expected echo records and
  60 STUN requests; no `DENIED` packet reached the oracle.
- Production Builder → STUN → fresh ICE Prepare → owned teardown: 10 cycles
  ×3 with the final source/interface fence. Independent evidence has 30
  distinct translated socket tuples, exactly two STUN requests each. This
  proves initial discovery and fresh Prepare refresh, not an ICE selected
  pair, AWG handshake or full VPN session.
- Injected permission cleanup failure retains the exclusive port; retry
  releases it. A stale snapshot refuses authorization. Tests cover partial
  constructor failure, generation fencing and TCP/UDP ownership coexistence.
- Mini-PC Linux vet plus race/shuffle ×5 of transportgen/socketmux/stunprobe:
  PASS (1.823/1.120/1.889s). No tests ran on the workstation.
- Native host routes, DNS and adapter deltas are empty. Temporary service
  and non-admin identity removed; mini-PC UDP oracle stopped. Defender and
  host firewalls remained enabled; workstation proxy/network untouched.

The initial native run had a foreign-helper cold-start failure with empty
output; subsequent repetitions passed. Its log and 245 packet records are
preserved, not counted as PASS. The harness now preflights helper executables
before installing protection and reports elapsed/context errors. A coincident
Defender cloud-failure event does not establish the cause. The final three
native repetitions pass after preflight; arbitrary cold executable startup
under protection remains outside this socket-authorization claim.

One intermediate offline vet failed because the transferred source archive
omitted `test/support`, not because a new dependency was required. The complete
archive was retested successfully.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-owned-datagrams/`.
Final source snapshot SHA-256:
`675542fa5722d249ccf6556e989c2d74c69516986b5d921ac90c3571d285df39`.
UDP executable:
`e166c1b170a03aab6dcc8b008c7d39161bc196ff511779d3c8250adf84b46bc2`.
Final runtime factory executable:
`84e73173b67ded687d024960ddf1254f32b6f192310b9a0271aa8a32961742f3`.

Physical unsolicited inbound Windows Firewall admission, protected versus
hosting-only lifecycle and full daemon composition remain open. The parent
socket owner must be drained before product teardown can report IDLE; this is
not yet wired into a Windows product daemon. No Windows release is claimed.

## W06 — physical inbound firewall scope and crash ownership

`winfirewall.NewInbound` and `OpenOwnedInbound` add exact physical TCP/UDP
exceptions using the existing native COM backend and protected write-ahead
journal. Scope includes executable, interface GUID/alias, local source and
local port; remote peers may be unknown during ICE and are not enumerated.
The constructor refuses wildcard/loopback/link-local/mapped sources, invalid
protocols, wrong interface ownership and unavailable interfaces. Recorded
physical rules are recovered with exact-scope validation, including after the
original process dies. Existing tunnel TCP/private-pool scope is retained.

Final Windows 11 Pro evidence:

- Native IPv4 inbound gate ×3: TCP and UDP blocked before installation,
  application traffic succeeds while the exception exists, and new sockets
  on the same ports are blocked after cleanup. The independent mini-PC sender
  records exactly three UDP echoes and three acknowledged TCP greetings, with
  unsuccessful baseline and final probes for each protocol. Final gate times:
  8.81/8.61/8.63s. Temporary QEMU forwards were removed afterward.
- Real physical owner process kill → journal recovery ×3: PASS. A second
  opener cannot take a live journal; after kill the persistent rule is verified
  present, then removed through exact recorded ownership.
- Existing tunnel-control lifecycle: three repeats of 100 measured install/
  remove cycles after bounded warmup, PASS with unchanged resource tolerances.
- Existing tunnel-control process-kill recovery: three repetitions each with
  the adapter present and gone, all six PASS.
- Windows vet plus checkptr/shuffle ×10: winfirewall 2.836s and runtime 38.407s,
  PASS. This change is Windows-only; no workstation tests were run.
- Final firewall rule and adapter baselines restored. No proxy, host routes,
  firewall profiles or Defender settings were changed or disabled.

Preserved earlier non-PASS observations:

1. An initial post-removal TCP check accepted a queued connection. The final
   oracle closes and rebinds the listeners only after removing the rule, then
   tests new arrivals. This is not a claim that Windows Firewall immediately
   revokes already admitted streams; runtime socket closure and WFP teardown
   remain necessary. TCP application acknowledgment prevents stale sender
   connections from being counted as successful user traffic.
2. The first legacy native harness was interrupted while capturing driver
   stderr through PowerShell, and its global rule check overlapped another
   gate. Final gates ran sequentially with native output redirected by cmd.
3. One legacy resource repetition refused an incomplete handle-type baseline
   (`unqueried=1`); the next two passed. The full final repeat passes without
   altering the test, resource oracle or thresholds. The failed log is retained.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-physical-inbound/`.
Final source snapshot SHA-256:
`026e32ed34c42818fd654b7fb8d225f3f1535b3605c23da1c6e7d65d96905c3c`.
Native test executable:
`c6c05c0de8ab37497e7dbc414016fa76bb19ea06d23bac774577a1da6ff7f580`.

This is a firewall backend gate, not full Windows product acceptance. Per-socket
composition with WFP/UDP/TCP owners, protected versus hosting-only lifecycle,
daemon startup/recovery and real mixed-network Global/Private sessions remain
required. IPv6 unsolicited ingress was not exercised by this IPv4 lab.

## W06 — composed socket permission lifetime

`runtime.WindowsSocketPermissions` now composes the protected WFP grant and
the journaled inbound Windows Firewall rule behind one socket-owned lease.
Protected mode requires an explicit live underlay owner; hosting-only mode is
explicit and rejects an underlay owner instead of silently selecting a mode.
The manager never removes ProtectedMode. Four UDP and eight TCP journal slots
bound durable state; a protected root lock excludes another runtime or recovery
process while ownership is live. Startup recovery visits only those fixed slots.

Partial constructor failures return their lease. Cleanup independently attempts
both permission removals, retains failed ownership, fences subsequent grants,
and lets the socket owner retain its exclusive port until retry succeeds.
Generation advance drains predecessor permission scopes before advancing the
shared WFP owner. Socket handles remain the transport/listener owner's concern.
Root-journal close errors remain visible on subsequent Close calls.

Evidence on the Windows VM:

- Vet and checkptr/shuffle ×10: runtime 37.168s and winfirewall 3.189s, PASS.
- Fault injection covers WFP acquire, inbound open/install/revoke, cleanup
  retry, capacity, generation advance failure and stale
  generation refusal. A composed factory test verifies the actual exclusive
  UDP port stays unavailable until the failed firewall revoke succeeds.
- Real protected transport factory now uses the composed owner: three repeats
  of ten Builder → STUN → fresh ICE Prepare → teardown cycles, PASS.
- Independent mini-PC oracle observes exactly 60 STUN requests on 30 translated
  socket tuples, two requests per tuple. No extra echo/data records appear.
- Native host route/DNS/adapter deltas are empty; no `IRVIO.Inbound.v1` rules
  remain. Recovery fixture removed and mini-PC oracle stopped. No workstation
  tests or proxy/network modifications were performed.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-socket-permissions/`.
Source SHA-256:
`0a966b93b6637ed1deaa54937fb11778d7568ad339e3e0304721b5e4dc77408c`.
Runtime test executable:
`ba6d5980387bfdffcaaed564658e594a0a66fe02b829e12ffd56869ffd05acd0`.

This closes UDP permission composition in the native transport test, not daemon
composition or full VPN acceptance. Protected incoming TCP, hosting-only real
network sessions, socket-manager process-crash integration, product boot/SCM
lifecycle and complete Global/Private sessions still require their own gates.

## W06 — composed incoming admission in hosting and protected modes

The native socket gate now exercises `WindowsListeners.Factory` with a wildcard
libp2p-style multiaddr resolved to the selected source, and the production UDP
mux factory, both authorized by `WindowsSocketPermissions`. No broad temporary
Windows Firewall exception is installed by this harness. The only lab plumbing
is two temporary QEMU forwards from the mini-PC into the isolated guest.

Three repetitions each exercise hosting-only and protected mode. In each mode,
new TCP/UDP traffic is blocked before ownership, a real UDP echo and acknowledged
TCP greeting succeed while sockets own permissions, and new sockets bound after
cleanup reject incoming traffic. Protected-mode policy remains installed and
verifiable after socket closure; it is removed only by explicit fixture cleanup.

- All six mode cases PASS; total repeat times 9.86/9.42/9.43s.
- Independent mini-PC sender records exactly six successful UDP replies and
  six acknowledged TCP replies, with unsuccessful first and last probes per
  protocol. The sender was started before the native gate.
- Host route/DNS/adapter deltas are empty, inbound rule count is zero, recovery
  service removed, sender ended and both temporary forwards removed.
- Windows vet and checkptr/shuffle ×3: runtime 14.572s, controlsocket 1.437s,
  winfirewall 1.551s, PASS. No production code was changed for this gate.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-socket-admission/`.
Source SHA-256:
`95fe47a5b95155c9779ba47c68f5010424f00491e933aab64dc58859a8db071b`.
Native executable:
`555d94f4e66f8784096eeab3de731dc0053d6d203be47f4053cc20340f72b7dd`.

This is IPv4 socket admission evidence, not discovery, an AWG session or a
running Windows product. A fresh cross-build of `cmd/irviod` still fails on the
missing Windows product/daemon composition (`recoverPreviousBoot`, `runProduct`,
`productOptions`, `runtime.NewDaemon`/`DaemonConfig`/`StaticTargets`) and Linux
UID/GID credential-policy fields in the common CLI. Do not replace these with
stubs or weaken named-pipe authorization to make compilation green. The next
implementation work is platform composition, including protection-before-join,
socket ownership, generation changes, ordered teardown and product boot/SCM
recovery; then full mixed-network Global/Private acceptance.

## W06 — production transport and shared coordinator composition

`ProductionConfig.MuxFactory` now reaches the actual `newTransportOwner`
constructor. Its Windows native gate uses that production constructor rather
than constructing a test-only transport builder. A negative regression verifies
that denied platform authorization propagates unchanged, publishes no generation
and cannot fall back to an unowned socket.

Client orchestration moved from `production_linux.go` into the shared
`production_coordinator.go`: the same target selector, M12/private provider,
underlay oracle and public-readiness reconciliation now compile on Windows.
Linux retains its marked probes; OS-specific probes remain explicit dependencies.
Windows composition checks preserve owners and exact public/private targets for
owned-global, shared-global and owned-private clients. These are constructor
checks, not connected-session acceptance.

- Mini-PC: vet and race/shuffle ×3 runtime (30.393s), transportgen (1.475s), PASS.
- Windows: vet and checkptr/shuffle ×3 runtime (13.323s), transportgen (3.791s), PASS.
- Windows native production owner: 3 × 10 generations, real mini-PC STUN,
  fresh ICE Prepare, commit and socket teardown, PASS (4.83/4.89/4.85s).
- Independent mini-PC oracle observed 60 STUN requests across 30 source ports;
  each generation produced initial acquisition and fresh Prepare observations.
- Native route/DNS/adapter deltas empty; inbound firewall rules and the recovery
  fixture service removed. No workstation proxy or route changes.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-production-composition/`.
Frozen source SHA-256:
`e871a5bc759e35f262a82f3c9017627803a04eede477fb1fec883a5f24fc91bc`.
Native executable SHA-256:
`3835870826e6ccc72e90616685f06c5db7349fe9d385d6fab1b5223668951990`.
Two initial source archives omitted test dependencies; another constructor test
incorrectly supplied generation zero and was rejected as required. These were
harness/test errors, corrected before the final gates; the failed unit log is
preserved. Linux tests ran on the mini-PC, Windows tests on the VM only.

`cmd/irviod` still does not cross-build: Windows daemon/product composition and
the CLI credential-policy split remain missing. Full daemon lifecycle, SCM,
desktop UI, installer and mixed Global/Private/server acceptance remain open.

## W06 — shared daemon and Windows platform lifecycle

The former Linux-only daemon is now shared orchestration, with Linux-specific
protection, accepted-control ownership and cleanup isolated in
`daemon_platform_linux.go`. Private hosting orchestration is shared as well.
Linux production CLI still builds. Windows `runtime.NewDaemon` now compiles and
executes real identity/discovery/M12/STUN startup through native platform owners.

Windows platform wiring provides a locked journal before protection adoption,
source/interface-bound libp2p dial/listen policy, owned UDP transport factories,
and an independently owned Basic Binding socket on UDP/3478. STUN `Serve` accepts
an already authorized socket; its protocol handling is unchanged. Generation
advance fences new calls, retires old listeners/connections, revokes old grants,
and rebinds listeners/STUN. Runtime protection delegates unprotect to daemon
teardown, so discovery/downstream/socket owners close before WFP removal.

Final native gate (library daemon inside a test executable, not installed SCM):

- Two repetitions, hosting and protected mode, two restarts per mode: eight
  complete successful startups; PeerID and AWG epoch preserved across restart.
- A competing daemon using the live journal is refused before modifying the
  first daemon's WFP protection; original protection remains verifiable.
- Each startup exercises a forced generation advance/rebind on the unchanged
  physical path. This is not yet evidence of physical adapter/route migration.
- Protected DISCONNECT closes STUN/listeners and returns protection to VPNOff;
  repeated Close succeeds with no retained socket/permission entries.
- Native repeats PASS in 8.57/8.50s. Independent mini-PC sender observes 83 valid
  STUN replies during the final run, with unsuccessful pre/post-run probes.
  Sender output does not attribute each reply to a specific mode.
- Route/DNS/adapter baseline deltas are empty, inbound rule count is zero,
  recovery fixture removed, sender exited and temporary QEMU forward removed.
- Windows vet + checkptr/shuffle ×3: runtime 14.488s, controlsocket 1.535s,
  stunserver 1.496s, PASS. Mini-PC vet + race/shuffle ×3 on the shared/Linux
  changes: runtime 27.957s, controlsocket 1.023s, stunserver 1.020s, PASS.

The first native wrapper miscounted successful sub-tests as full repetitions;
the test itself passed. Its log is preserved separately and the anchored wrapper
was rerun successfully. No workstation tests or proxy/route changes were made.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-daemon-lifecycle/`.
Source SHA-256:
`b519d4cd85e4efca067f07d689aadacabf25dd23341d24437ca60d9b137df041`.
Native executable SHA-256:
`086d2cc7375858fa60e0add5219db1bbf9601ba943b04f7fbca7804d51a67960`.

This does not establish a connected daemon-owned CLIENT+EXIT or headless server
session: those paths compile but still require mixed-network acceptance. The
full Windows command now gets past missing runtime daemon definitions and fails
on product startup/boot recovery and Linux UID/GID credential configuration.
SCM, product lifecycle, GUI, installer and full crash/session gates remain open.

## W08 preparation — native product configuration resolution

Windows product defaults now resolve through the native ProgramData known-folder
API, with no environment-variable redirection. The CLI config-loading seam uses
the existing Linux path on Linux and an optional protected Windows config on
Windows. The Windows config directory/file must be SYSTEM/Administrators-owned;
the existing strict version/field/duplicate validation remains shared.

The product resolver now queries native Windows routes, falls back across the
bootstrap pool when a route is unusable, and accepts an explicit excluded tunnel
LUID set. A resolved route is not a bootstrap reachability or readiness claim.
Connected-prefix selection excludes default/gateway/loopback/multicast routes and
excluded tunnel interfaces. Exit pool collision checks conservatively include
other VPN routes and owned residue; cleanup must finish before re-resolution.

The existing private /24 selection algorithm is shared unchanged with Linux.
Windows persists pool/control-port records with existing `winfile` ACL/handle
validation and flushed create-once atomic writes. A concurrent winner is read,
not overwritten. Corrupt, oversized and multiply-linked records fail closed;
a persisted pool that later conflicts fails without silent renumbering.

Evidence from the Windows VM and mini-PC only:

- Windows native resolver gate, checkptr enabled, shuffle ×5: PASS. Kernel
  source/interface matched independent PowerShell `Find-NetRoute`; excluded
  LUID and own-host target were rejected. Route/DNS baseline deltas empty.
- Pool/port reuse across 30 reopen cycles per repetition, profile deletion,
  exhaustion/collision/corruption, concurrent create winner, hardlink refusal,
  strict config and writable-config refusal: PASS.
- SYSTEM config checks plus separate IrvioLab user-owned-config refusal ×3:
  PASS. No canonical installed profile/config was created by these fixtures.
- Windows source `go vet` and checkptr/shuffle ×3 for productruntime and
  productprofile: PASS (4.983s / 14.827s).
- Mini-PC `go vet` and race/shuffle ×5 for productruntime, productprofile and
  cmd/irviod: PASS (1.131s / 1.083s / 22.739s).
- Linux production binary builds; workstation tests and network changes: none.

Reproducible native harness:
`tools/windows/w05/product-resolver-check.ps1`.
Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w08-product-resolver/`.
Test source archive SHA-256:
`22afa9eaf883ae90c04942e25bd8995fbbc393d1a82d35de599bd5d84d97a41e`.
Checkptr-enabled native test executable SHA-256:
`fc4caa89ca2be3a4bd2161bb86b6a4346ee3f59be39c56a2f920dd4c014f7ef6`.

This is resolver preparation, not completed Windows product startup. Native
product orchestration, boot cleanup and SID-based CLI API wiring are still
missing: the full Windows cmd build remains blocked on those symbols. The
resolver has not yet been accepted in an installed CLIENT+EXIT/server session.
SCM/GUI/installer and full mixed-network acceptance remain open.

## W08 preparation — retained protection, owned recovery and desktop API identity

Windows daemon composition now supports an explicit product-owned protection
mode. Retiring a network attempt closes its resources and atomically revokes
its WFP permissions to the deny-only policy, without calling policy removal.
This includes failed startup. The network-local controller can finish closing,
while final kernel unprotect remains the product controller's responsibility.
This option is not yet wired into product Connect/Disconnect; a future product
IDLE response must follow successful explicit policy removal, not merely the
network-local controller's closed/off status.

`RecoverWindowsDaemonResources` locks the native daemon journal and refuses a
live owner. It visits only the fixed socket/control/DNS/route journal locations,
including dual-role upstream journals, through ACL-checked pinned directories.
It neither removes WFP nor deletes unknown files, creates absent roots, or
discards malformed evidence. Final WFP removal remains an explicit caller step.

CLI Local API authorization is now platform-separated. Linux UID/GID behavior
is retained. Windows reads the selected user's SID from installer-owned
`ProgramData/IRVIO/desktop-user.sid` with SYSTEM/Administrators-only ownership;
it does not trust a GUI environment variable or an identity/profile override.
The existing pipe transport still verifies SYSTEM service identity and the
selected client SID. The installer must provision this file; it is not a new
user-editable product configuration field.

Verified gates:

- Two native repetitions: failed startup followed by two successful network
  starts/closes; persistent protection was adopted and deny-only policy remained
  complete after every retirement. Recovery refused each live daemon.
- TCP to mini-PC port 22 was reachable before installation and after explicit
  unprotect, but blocked after all three retirements in each repetition. This
  targeted probe does not replace the full connected-user leak acceptance.
- Existing native daemon startup/migration/disconnect/restart gate passed twice;
  host route/DNS/adapter deltas empty, temporary recovery service removed.
- Native checks: retained-protection 2.29/2.30s; existing lifecycle 8.46/8.43s.
- Windows vet + checkptr/shuffle ×3: runtime 14.077s, localapi 13.401s,
  productruntime 4.757s, PASS. Separate IrvioLab user-owned identity refusal ×3
  also passed; no installed canonical identity record was modified.
- Mini-PC vet + race/shuffle ×3: runtime 28.212s, localapi 4.664s,
  productruntime 1.071s, cmd/irviod 14.208s, PASS.

The first retained-protection harness read adoption from the control-only
daemon's general status (where that field is not populated), rather than from
the protection controller snapshot. That run failed its assertion and is
preserved as native-01; the corrected gate and additional TCP probe passed in
native-02. It was not treated as successful evidence.

Linux regression also exposed a preexisting shared Local API client race:
after decoding a terminal response, a subsequent EOF closed the client and
could win a select over the queued response. A barrier regression failed on
the old client three times. The fix waits for the per-request result (closure
already completes outstanding requests), preserving server replies. The new
regression passed ×100 on Windows and under Linux race; API-version negotiation
also passed ×100 under Linux race. No wire or server response behavior changed.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w08-product-lifecycle/`.
Final source archive SHA-256:
`2e5196ccdc725c3247aa3331f852ac2de385fd79cbf773433ecc8244ba206952`.
Retained-protection native executable SHA-256:
`666c2364c2dbe776e5ea19394da22f27890556a1196e35e692171339cabfc5b2`.

At commit c020f16, Linux cmd built and Windows full cmd still needed product
startup, options and boot recovery. The next section records subsequent work.

### W08 real product daemon IDLE and SCM lifecycle — partial

The desktop controller, Private API/enrollment/management and product capability
configuration are now shared between Linux and Windows. Windows supplies native
paths, a SYSTEM-only product ownership lock, retained WFP protection, journal
recovery and the SCM adapter. Linux keeps its previous native hooks. No wire,
selection, M11 quorum or lease protocol changes were made.

`irviod.exe` now builds as the actual command and runs with zero arguments as the
demand-start `IRVIO` SYSTEM service. The adapter reports Running after Local API
startup, not merely process creation; server Running does not assert asynchronous
M11/Exit readiness. Intentional shutdown returns success, errors return a service
failure, and profile deletion reopens the product lifecycle inside the SCM
process. `IRVIO-Recovery --boot-recover` is wired separately; connected-session
reboot acceptance is still open.

Native Windows VM evidence:

- Real zero-argument daemon: Local API reports DISCONNECTED, contribution off,
  bootstrap EMPTY. Idle DISCONNECT succeeds.
- An IrvioLab local batch task successfully uses the authenticated pipe and
  SHUTDOWN. Token inspection found high integrity despite the task's Limited
  setting; this is not evidence of unprivileged API access. SSH/network-logon
  access was refused as intended;
  the initial interactive task did not run because no interactive token was
  available, so that attempt is not counted as a pass.
- A separate standard account was verified using a real non-admin, non-elevated
  LogonUser token on a locked impersonating OS thread: the actual product API
  accepted IDLE/EMPTY queries, DISCONNECT and SHUTDOWN (0.09s). Native ACL checks
  passed three times: allowed account opens the authenticated pipe, a different
  account is denied, and clients cannot create server instances.
  These are native API/authorization proofs, not GUI/process-launch acceptance.
  Ordinary-user PowerShell Task Scheduler attempts did not execute; a direct
  cross-session launch exited with `0xc0000142` before the probe. Neither failed
  harness launch is counted as an IRVIO test pass.
- Delete Profile closes the old API connection, reopens a usable EMPTY/IDLE
  endpoint, and keeps the same SCM process. Checked in both native runs.
- Forced process termination while **IDLE** triggers SCM restart with a new PID.
  The new API is usable; explicit SHUTDOWN then exits zero and stays stopped
  despite configured failure restart actions. This does not prove connected
  crash recovery or fail-closed user traffic.
- Cleanup confirms absent product WFP policy and ConnectIntent, released product
  lock, unchanged DNS/routes/adapters, and removed temporary services/tasks.
  Profile/binary fixtures were archived rather than discarded.
- The repeatable harness is `tools/windows/w08/product-idle.ps1`; it refuses an
  existing product installation and never removes foreign WFP or firewall state.

First-run route comparison incorrectly compared JSON numeric protocol values
with live enum names (`2`/`Local`, `3`/`NetMgmt`). Original failure evidence was
retained. Numeric-to-numeric recheck had no difference; the second complete
harness used identical representations at both ends and passed without residue.

Windows byte counters currently report unavailable, not invented zero values.
Connected Private clients are sampled from ready, unexpired installed leases;
expired/revoking leases and duplicate PeerIDs are excluded. Payload-equivalent
Windows byte accounting remains a required unfinished feature.

Source gates on the frozen v4 archive (Windows-only test additions after v2):

- Windows `go vet` and checkptr/shuffle ×3: cmd/irviod 2.650s, runtime 14.524s,
  productruntime 7.829s, localapi 12.989s, PASS.
- Mini-PC Linux vet and race/shuffle ×3: cmd/irviod 14.029s, runtime 28.169s,
  productruntime 1.073s, localapi 4.616s, PASS. No tests or VPN operations ran on
  the workstation.
- Linux command build and Windows cross-build pass; `git diff --check` clean.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w08-product-startup/`.
Source archive SHA-256:
`8740946054085225189e4806a27c6141ccf51ed720867b27bbef9aed62a4065d`.
Product executable SHA-256:
`628f4d175f40442ce9180ce034b3a24f7b57b592ec648dcbc31e3ff551e0c064`.
Native probe executable SHA-256:
`9255d3f36ab2d14617978bf40ca29d8c7cfade3f03018c3745b363afb0a1b10a`.
Final native probe with an explicit unprivileged-token guard, SHA-256:
`1c71e42e5fe355b248a0b75889a6f8df637905b811ad9a9fc7a62a257b57fdce`.
Non-admin product API probe SHA-256:
`382939a3dd03a40e315649274b3cd9b5efc584da695cbe0a4c77674cd297c8b6`.

At the bbf3634 checkpoint, not accepted yet: full product CONNECT/CLIENT+EXIT or Private sessions, connected
SIGKILL/reboot, boot attribution of protection with no intent, failed-start
cleanup retry ownership, real GUI/service activation, installer and server roles.

### W08 real Global VPN, connected crash and real reboot — partial

The actual zero-argument Windows SYSTEM daemon from bbf3634 was tested against
the existing three-entry bootstrap bundle, without direct Rendezvous/Exit
configuration. The guest used the canonical DB-IP MMDB and the existing signed
Wintun/pinned WinDivert payloads. Production code was unchanged for these gates;
new native tests and a repeatable SYSTEM harness are in
`cmd/irviod/product_global_native_windows_test.go` and
`tools/windows/w08/product-global.ps1` (usage in that directory's README).

Observed native results:

- Fresh profile imports all three bootstrap entries and discovers countries via
  production discovery. Initial country discovery took 0.51–0.73s in the recorded
  runs; CONNECTED took about 10s from CONNECT invocation. These observations are
  not a percentile study or an SLA.
- Windows native DNS, ICMP and direct HTTPS with `Proxy:nil` passed. The HTTPS
  service observed `89.124.71.34`, the NL Exit, not the guest's ordinary egress.
  The final harness requires an explicit expected-egress assertion oracle; this
  is never passed to the daemon or used for discovery.
- Connected process termination leaves persistent WFP active (95 rules in the
  first crash run). Four targeted ordinary TCP probes, proven reachable before
  CONNECT, fail during the crash interval. SCM restarts a new daemon PID and
  restores the persisted NL intent without another CONNECT command. The first
  recorded recovery was 18.67s, including an intentional **8s lab restart delay**.
  DNS/ICMP/HTTPS through the Exit pass after recovery.
- Explicit Disconnect/Shutdown removes product protection and intent, releases
  ownership, and restores the exact same-boot DNS/routes/adapter baseline. The
  assertion runs **before** any emergency cleanup; a manual cleanup is not
  counted as successful product teardown.
- Two actual Windows reboots were performed with an established, traffic-tested
  session. Before reboot the daemon was killed without teardown; only automatic
  crash restart was disabled, while the desktop service remained demand-start.
  Real boot timestamps changed. Automatic `IRVIO-Recovery` cleared the old
  intent/resources and exited zero; `IRVIO` remained stopped. Ordinary WAN was
  reachable. A subsequent manual daemon start remained IDLE, contribution off,
  with the durable bootstrap preserved, and Shutdown succeeded.
- Reboot changes Windows ifindices and recreates the OS-owned Hyper-V Default
  Switch GUID/subnet. Raw host differences are retained. Reboot comparison uses
  stable physical adapter identity and interface names, excluding only the named
  Hyper-V Default Switch recreation; this is not a claim of byte-identical host
  state across reboot. No owned WFP, active DNS/route journal, IRVIO firewall rule
  or TUN residue remained in the inspected first reboot run. The second run
  passed the repeatable normalized comparison and archived its fixture.

Harness issues were preserved separately: the first post-reboot assertion
incorrectly treated SCM `ERROR_SERVICE_NEVER_STARTED` (1077) for the demand-start
desktop service as an error; an exact driver-path comparison initially omitted
the native `\??\` prefix; and raw ifindex/GUID comparison was unsuitable across
Hyper-V recreation. These were harness corrections, not production fixes.

Windows vet and native checkptr/shuffle ×3 passed for cmd/irviod (2.709s),
productintent (20.700s) and localapi (13.154s) on the final frozen source archive.
No tests, VPN sessions or route/proxy changes ran on the workstation.
Linux production code did not change in this gate-only increment.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w08-product-global/`.
The guest retains original run directories and archived private profiles;
the exported evidence is allowlisted top-level logs/metadata only, not identities
or profile databases. Product binary SHA-256 remains
`628f4d175f40442ce9180ce034b3a24f7b57b592ec648dcbc31e3ff551e0c064`.
Final native probe SHA-256:
`b196f41461625d762587fbf193a6799da35ea6543980f3004ae4e1701fb345f6`.
Frozen final test/source archive SHA-256:
`b80bda12666a856c7285d909d688ba01cea32b2094430db9c1068b2e4ccc4fb1`.

These gates demonstrate a real Global **client** path, not downstream Windows
Exit traffic: `contribution_enabled` alone is not an Exit data-plane proof.
They also do not prove all-protocol/all-destination leak freedom, Private parity,
sleep/hibernate/Fast Startup, 100-cycle resource stability, GUI, installer or
headless server acceptance. Failed-start cleanup retry ownership and attribution
of protection residue without an intent remain open.

### W07 mixed product Private VPN — TCP/UDP progress, Exit parity incomplete

Real product daemons were exercised in both directions between the Windows VM
and the mini-PC. Windows used the same zero-argument bbf3634 product executable;
Linux used its installed daemon, SHA-256
`d9a9dba106e2def44cb4c963ecdc4e8e77b9c5f6334b10ae876680e05eeb85f8`.
Both used the actual three-entry bootstrap bundle. Invitations and approval
followed the existing Private Local API, registry/discovery and saved-target
path; no direct peer endpoint or fake candidate was injected.

Linux-host → Windows-client:

- Linux starts Private hosting and creates a one-hour auto-approve invitation.
  Windows imports it and enrolls in 2.24s. The saved grant appears while status
  remains DISCONNECTED: approval does not turn on the user's VPN.
- Explicit exact-target CONNECT succeeds in 6.16s. Native DNS, direct no-proxy
  HTTPS and a 1 MiB download pass. Disconnect completes, then a second CONNECT
  reuses the saved grant without another invitation/enrollment and succeeds in
  8.60s; the same traffic probes pass. The native test totals 41.46s, including
  intentional ten-second observation windows per connection.
- The Linux host reports one active downstream during traffic, zero after
  disconnect, and 105,035 shared-upload bytes at the final observation. That
  counter is upload-only, not the downloaded 2 MiB. Both machines share the same
  public egress IP; matching IP alone would not establish the forwarding path.

Windows-host → Linux-client:

- Windows enters HOSTING and creates a fresh invitation. Linux imports it and
  receives APPROVED, then connects to that exact Windows PeerID. Windows reports
  the member allowed/connected and one downstream client. The Linux kernel
  selects `hydra0`, table 51820, lease source `10.254.177.2` for internet traffic.
- Native DNS succeeds. A direct HTTPS request succeeds and a subsequent
  Cloudflare 1 MiB download returns HTTP 200 with exactly 1,048,576 bytes.
  Disconnect/reconnect with the same grant works without a new enrollment; a
  second HTTPS egress request succeeds.
- Revoking the active Linux member on Windows removes its downstream count
  (zero within the 15s bounded check), while Windows stays HOSTING. Linux moves
  to NO_EXIT without switching to a public target. An ordinary HTTPS attempt
  fails during DNS resolution with the eight-second probe timeout. This is a
  bounded no-fallback observation, **not** an all-address/all-protocol WAN-leak
  proof. Explicit Disconnect restores ordinary internet access.

**Unfinished gate W06-ICMP-001:** the first reverse-direction probe failed at
ICMP echo (one request, no reply). This is a real Windows Exit functional gap,
not a discovery failure: `internal/exitnat/packet.go:parse` accepts only TCP/UDP;
the native reservation/capture path also supports only those protocols. Do not
claim complete Windows Exit/Private parity or remove ICMP from acceptance. The
follow-up must provide lease-owned ICMP handling without broad capture of other
host traffic, preserve revoke/generation fencing, and consider ICMP errors/PMTU
as well as echo, with focused packet and native regressions. No production fix
for this gap is included in this evidence-only increment.

Cleanup and harness observations:

- Windows explicitly disconnects/shuts down; owned intent/WFP are absent and
  DNS/routes/adapters exactly match the same-boot baseline before fixture
  archival. The temporary IRVIO/recovery/WinDivert services are removed.
- Linux returns to DISCONNECTED, no hosting, no private target, zero saved
  targets; the disposable Windows membership is revoked. DNS matches baseline.
  NetworkManager temporarily raised the physical default metric 101→20101 after
  losing its connectivity check during protection. Its own connectivity recheck
  returns `full` and restores metric 101; the full route JSON then matches the
  baseline. Both raw difference and NetworkManager evidence are retained; no
  manual route overwrite was used to manufacture equality.
- The first mini-PC preflight ran in its intentionally isolated SSH management
  namespace and could not resolve internet names. It was restarted in the host
  namespace before hosting. The first Windows API probe ran after the expected
  idle auto-stop; SCM recorded a clean exit zero, not a crash. Starting the
  demand service immediately before the probe corrected that harness issue.
- The Linux observation script's outer `unix_ms` formatting was not reliable
  on that host; use API `sampled_at_unix_ms` and Go monotonic durations instead.
- No tests or networking changes ran on the workstation. Invitation links,
  identities/profile databases and downloaded payload are not exported to the
  evidence set or committed.

Native Windows vet/checkptr/shuffle ×3: cmd/irviod 2.910s, privateaccess 27.339s,
PASS. Mini-PC vet/race/shuffle ×3: cmd/irviod 13.955s, privateaccess 3.300s, PASS.
The API helper builds/vets in both OS sets; it has no standalone unit tests.
The reusable native client gate is `product_private_native_windows_test.go`;
helper/reproduction notes are in `tools/windows/w07/README.md`.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w07-mixed-private/`.
Frozen source archive SHA-256:
`3985479028f1fcfb43b70dd58387c36353507472c59a95d095a4081ca0314f24`.
Native Private probe SHA-256:
`7f2835f7b8ae2727decd5c75b319ea87ff5c58d40bc576695f2263d7d54b2d0a`.

Windows↔Windows, complete ICMP/data-plane parity, Windows byte counters, GUI,
multi-client product load, connected Private crash/reboot and the remaining
installer/server/lifecycle/security matrix are still required.

## W06 ICMP errors — bounded partial fix after mixed Private evidence

The Windows NAT now reverse-translates ICMP unreachable (including MTU),
time-exceeded and parameter-problem messages quoting an existing TCP/UDP flow.
It verifies the physical path, exact reservation token, quoted tuple and current
lease under the same engine lock. Errors do not allocate flows or extend timers.
The native capture filter includes only quotes for its exclusively reserved
TCP/UDP source port/address and physical interface; unrelated host ICMP is not
captured. Router address/MTU are preserved, quoted source/port and checksums are
restored, RFC 4884 padding/extensions are retained. No Core protocol changed.

Evidence from the isolated Windows VM and mini-PC:

- Windows native filter evaluation covers TCP/UDP positive matches plus wrong
  port, protocol, quoted source, recipient, interface, direction, type, options
  and truncation. Native inbound fragmentation-needed capture passes; no external
  packet injection is used by that test.
- NAT tests cover 80 translation combinations, malformed packets, lease/token/
  generation/tuple fences, expiry/revoke and no lifetime extension. Existing
  five-lease real DNS and encrypted-lease NAT regressions also pass.
- Mini-PC vet and race/shuffle x10: PASS (9.873s). Thirty-second parser fuzz:
  PASS, 452,820 executions. Windows affected-package vet: PASS.
- First Windows native/shuffle x3 run used ordinary test binaries; it is not
  counted as a compiler-instrumented checkptr run. A separate repeat uses
  `-gcflags=all=-d=checkptr=2` binaries: native/shuffle x3 PASS; both evidence
  directories are preserved. The 100-cycle pending-I/O lifecycle tests pass
  with no handle increase inside each measured warm interval.
- Temporary driver is stopped/deleted, routes/DNS match the exact baseline.
  No workstation tests, route, proxy or VPN changes. Product fixtures remain
  absent; no production service was upgraded in this component increment.

Evidence root: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-icmp-errors/`.
Guest runs: `C:\IRVIO-Lab\W06-icmp-error-01` and `W06-icmp-error-02`.
Frozen source archive:
`c4afc2d0f671467a9e65fdc78e10eb3a91750e4759f991682a15f163af36e21a`.
Instrumented NAT test:
`c242a07c05a71b1de18b245616f57845178796f5150067c036e584f3c1939ed7`.
Instrumented driver test:
`aded21c76bd097b593ccb38183b59027c83b1ef4d8d3e5c1f4b4df03f3aa0912`.
Updated production Windows executable (build only):
`84bdd4a33d769008192addc4221a5ffb07ed7811e52d9acf13a2410b3206994c`.

**W06-ICMP-001 remains open:** ICMP echo is still unsupported; full product
PMTU behavior needs an end-to-end constrained-MTU oracle, not just valid packet
translation. Native resource-lifetime/Wintun gate was explicitly skipped in
this fixture. This is not a full Windows Exit or Windows-port acceptance.

## W06 echo feasibility — native API rejected, no production bypass

The native `IcmpSendEcho2Ex` candidate was measured before integration. Explicit
physical source pinning works with the VM's split routes: ordinary echo enters
the lab TUN while pinned echo uses the physical path. The async buffer owner
passed checkptr/shuffle x3 with 100 requests/cancellations each; process handle
counts were 140→140, 146→146 and 154→154 within each warmed measurement window.
This is a component observation, not a claim that all lifecycle resources or
Windows Exit parity pass.

The WFP identity gate rejects the candidate:

- Native drop events identify ICMP as `System`, rather than the calling
  executable. The exact executable/source/interface/type permit does not work.
- Omitting only executable scope lets the privileged caller send, but an actual
  child process with a new non-admin primary token can also send through that
  permit. Neither an admin UAC token nor SYSTEM impersonating itself was used
  as the ordinary-user oracle.
- Deny-only rules block both callers; removing the permit blocks the standard
  caller again. The isolated gateway oracle avoids public ICMP dependencies.
- The rejection reproduced three times with checkptr instrumentation. The
  original failed acceptance assertion remains in `W06-echo-identity-01`; the
  repeatable negative feasibility regression is `W06-echo-identity-02`.
- Native Windows `go vet ./tools/windows/w06/echo/...`: PASS.

No echo permit was added to product WFP and no production package imports the
candidate. The candidate implementation and tests are confined to
`tools/windows/w06/echo/`. The Core/network protocol remains unchanged.
The next echo design must retain ordinary-user fail-closed protection and exact
ownership of host ICMP replies/errors; neither a random 16-bit ID nor a UDP
reservation proves exclusive ownership against local ping processes.

Harness qualifications and cleanup:

- Initial public echo preflight failed because QEMU's host user could not open
  Linux ping sockets (`ping_group_range=1 0`). A temporary GID-1000-only setting
  enabled the oracle for the source-pin/component experiments and was restored
  to `1 0`. Workstation routes, proxy and VPN were not changed; test programs ran
  only in Windows. The decisive identity reproduction needs no such setting.
- Routes/DNS/adapters match the VM baseline. Each WFP owner is removed and
  inspected; the disposable standard account and service are absent afterward.
- A PowerShell wrapper initially passed unquoted dotted test flags incorrectly
  after the identity gate had passed. A separate corrected component run passed
  parsing/validation x3; its native public echo stress was explicitly skipped.
  The earlier native stress evidence is retained separately, not inferred from
  that skip.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-echo-feasibility/`.
Only target-filtered WFP event summaries are exported, not unrelated host events.
Frozen lab source archive:
`855bedff1d456a9f1f784ba9a65e4ba4df966a20f9bf7aa621c09a080a28bfbd`.
Identity probe:
`e3da2e2e3b2d5e0cea6b4e34a30251813af05d84434b1898c8316608b35ce8be`.
Relocated native component probe:
`996bc27f4e84068c3fc4ef0c6bef9a89e60091ee6de2fc000954a9094b0d58d1`.
**W06-ICMP-001 remains open; full Windows port acceptance remains incomplete.**

## Desktop startup rollback ownership

The shared desktop wrapper assigned its daemon only after Start and bootstrap
Attach succeeded. A failed Start, canceled request or failed Attach could lose
the only reference even when daemon cleanup failed. This matters on Windows
where native adapter/driver/WFP ownership deliberately remains retryable.

The controller now owns the daemon before Start. An uncommitted runtime retains
its canceled lifetime and rollback flag until Close succeeds; replacement Start
is rejected, country queries cannot use the partial runtime, and it does not
advertise committed connection/traffic readiness. Same-boot recovery retries
cleanup before constructing a replacement. Explicit Disconnect still clears
the user intent first and stays DISCONNECTING when cleanup fails. Protected
intent is re-protected after rollback; hosting-only rollback does not enable
ProtectedMode. No protocol, selection or generation semantics changed.

Validation on the frozen code:

- Native Windows checkptr/shuffle x10 controller fault injection: PASS. Cases
  include failed Start, cancellation before/after Attach, failed Attach,
  repeated cleanup failure, replacement fencing, recovery retry, protection
  failure, successful replacement and Disconnect. Both protected and private
  hosting-only paths are covered with injected daemon dependencies.
- Windows complete `cmd/irviod` non-gated tests/checkptr/shuffle x3 and native
  vet: PASS. Environment-gated networking tests remain explicitly skipped.
- Mini-PC focused race/shuffle x10: PASS (1.227s); complete `cmd/irviod`
  race/shuffle x3: PASS (14.266s); vet: PASS.
- The new production Windows executable passes the existing actual SCM gate:
  zero-argument IDLE, profile reset without process replacement, forced idle
  process termination followed by SCM restart, intentional Shutdown without
  restart, no intent/WFP residue and exact route/DNS/adapter cleanup. This is
  an IDLE crash test, not another CONNECTED crash or reboot acceptance claim.
- Windows and Linux production builds succeed. No workstation tests or network
  changes; no installed mini-PC service or live distributor was upgraded.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/startup-owner/`.
Source archive: `d8baa356d7a6c5b441a8cfb520b61a39e5f52d505247ea88faa6359830258bf7`.
Windows test: `6c4c959ec4d17bd6005438cde8efe3f61f34b640e3cde74dad103539e9c9ff03`.
Windows daemon: `c17e5ab7033529f02d3e952462848a9f045de99e847785e15cad4f5ca0d09ca9`.
Linux daemon: `fb2e3b49271635b6fa3dca4dca34c51cf3392d37c026ba3abc143c05736e1f9a`.

This increment covers CONNECT/private startup ownership; the following increment
addresses the separate bounded idle country-discovery probe.

## Idle country-discovery cleanup ownership

The bounded probe also lost its daemon reference when Start or Close failed.
The controller now retains that separate owner until Close succeeds, including
failed Start and canceled queries. A new query, Global CONNECT and Private
CONNECT/hosting cannot allocate another runtime before cleanup. Disconnect and
Close retain retryable ownership; pending cleanup reports DISCONNECTING rather
than final IDLE and cannot trigger idle auto-stop. Background cleanup retries
without creating ConnectIntent, traffic sampling, contribution or protection.

The query's existing discovery cancellation fence remains in place. A focused
concurrent test holds failed cleanup while CONNECT cancels discovery, verifies
the command waits, then verifies cleanup failure still prevents replacement.
Tests also cover successful queries, Start/query errors, cancellation, failed
Disconnect/Close, repeated cleanup and idle recovery without network intent.

- Windows checkptr/shuffle x10 focused lifecycle tests and x3 complete
  `cmd/irviod` non-gated tests: PASS; native vet: PASS.
- Mini-PC race/shuffle x10 focused: PASS (1.280s); complete `cmd/irviod`
  race/shuffle x3: PASS (14.116s); vet: PASS.
- The updated production binary passes the real SCM IDLE/reset/idle crash/
  shutdown gate. Both temporary services are removed; route/DNS/adapter delta
  is empty. This does not substitute for connected networking/crash acceptance.
- No workstation tests, proxy changes or production deployment updates.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/idle-owner/`.
Source: `b2fbfb8ad22e4b1df6415b99ca42674ba0fe2e04b2df7b932872392166a5b671`.
Windows test: `7fb3bb0ae69312b03f132870fc38061a6ce3ffb2fdc30b1dbb25fcd9c0876400`.
Windows daemon: `b41e4b40f0cdc89830345ea886edd6005afe947302f576ee38ac40130d44ef6d`.
Windows Exit echo, GUI, installer and the full acceptance matrix remain open.

## W09 shared UI Local API client

The protobuf negotiation, frame decoder, request serialization, timeouts,
Global/Private commands and status/log mappings now live in
`ui/lib/irvio/local_irvio_client.dart`, independent of `dart:io` sockets.
`LocalApiTransport` owns the OS byte stream; the Linux factory remains a Unix
socket adapter and the existing framing exports stay available. The CLI smoke
helper uses the common `IrvioClient` interface. No API version or wire field
changed. Closing the UI connection sends neither Disconnect nor Shutdown.

Validation uses the same locked dependencies on both OSes:

- Mini-PC: Flutter analyze clean; all 162 tests PASS, including the existing
  500 socket/command lifecycles. Linux release build PASS; the existing vendor
  `tray_manager` appindicator deprecation warning remains.
- Windows VM: Flutter analyze clean; all 149 applicable unit/widget tests PASS.
  Only the two explicitly Unix-socket-based test files are Linux-only. The
  independent in-memory transport/framing gate passes 10 tests, including
  fragmented frames, both VPN command paths, concurrent serialized writes,
  EOF, malformed input, incompatible negotiation and UI-only closure.
- The first Windows attempt lacked pub-cache packages and did not run tests.
  Package sources/hashes already used on the mini-PC were copied into an
  isolated lab cache; offline `pub get --enforce-lockfile` then passed. No
  dependency versions were upgraded. A first mini-PC analyze invocation tried
  an unnecessary online pub get; the verified online-fetch processes were
  stopped and final analyze/test/build used `--no-pub` after offline resolution.
- The initial analyzer caught two stale Linux concrete-type annotations in the
  smoke helper; they are fixed and included in the final clean analysis.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/ui-transport/`.
Frozen source archive:
`ef6e762c00294dba79e8083a7ded40c9aebcd82572ac62ae9cf6a5718e870dcd`.
Windows toolchain is Flutter 3.47.4 / Dart 3.13.3, VS Community 2022 17.14.41
with x64 C++ tools, Windows SDK 10.0.26100.0. No workstation tests, network
changes or installed UI upgrades were performed.

This is the shared protocol/UX-test prerequisite, **not a running Windows UI**.
The Linux-only startup guard remains until the authenticated Named Pipe adapter,
SCM activation, Windows runner and OS desktop integrations are implemented and
tested against the real daemon. W09 acceptance remains incomplete.

## W09 authenticated Windows UI transport and SCM activation

`cmd/irvio-ui-bridge` is a same-user, non-resident helper beside the packaged UI.
It starts only the installed IRVIO SCM service, checks LocalSystem/own-process
configuration, authenticates the existing Named Pipe and additionally verifies
its server PID against that service. It accepts no path/service overrides and
receives no stop/configuration rights. The opaque bounded-buffer relay reuses
the Go authentication code without adding another protobuf parser. GUI EOF
closes the channel/helper, not the daemon's VPN intent. The Windows Dart adapter
uses the common Local API client with a separate activation/HELLO timeout and
bounded helper-close escalation. A subscription-initialization failure now also
releases transport ownership; background close errors are handled.

Final frozen-source gates:

- Windows SCM state/timeout tests, checkptr/shuffle x10: PASS. Relay tests x20:
  PASS. Full Local API suite checkptr/shuffle x3: PASS (14.044s); vet clean.
- Actual temporary **non-admin** account starts the fixture service with only
  START/QUERY_CONFIG/QUERY_STATUS permissions, negotiates API 1.7 and performs
  ten helper/pipe lifetimes. Fixture CONNECT intent and service PID survive
  each GUI-channel EOF. STOP, CHANGE_CONFIG and WRITE_DAC are denied; another
  ordinary account cannot use the helper/API. This CONNECT is an in-memory
  transport fixture, not a claim of real user-plane traffic.
- A compiled Dart executable uses `WindowsIrvioClient`, the co-located helper
  and authenticated pipe under that same standard-user token: PASS. No shell,
  PATH helper lookup, UAC elevation or extra service process is involved.
- Foreign SYSTEM-process and ordinary-user-owned pipes are both rejected before
  receiving API bytes. The initial ordinary-owner test incorrectly attempted
  to assign that owner from SYSTEM and failed in the harness; creating it under
  the real ordinary-user token corrects the oracle. Initial failure is retained.
- Windows Flutter analyze and all 153 applicable unit/widget tests: PASS.
  Mini-PC analyze, all 166 tests and Linux release build: PASS (existing vendor
  appindicator deprecation warning only). Mini-PC relay race/shuffle x20 and
  full Local API race/shuffle x3: PASS; affected vet and formatting clean.
- The compiled Dart client also starts the **actual zero-argument production
  daemon** and reads IDLE/empty-profile state. Existing SCM profile reset, idle
  crash/restart, intentional Shutdown and host cleanup gate: PASS. This product
  invocation is SYSTEM; the separate fixture above proves standard-user access.
  DNS/routes/adapters have zero delta; temporary services/accounts/helpers are
  removed. Installed mini-PC UI and services were not replaced.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/ui-bridge/`.
Source archive: `df58c6f77d65ffe47ae1e8f8bdd39d64458b62109ced7783d09ebab0fddcdee6`.
Windows helper: `ee5c9f416632d858f74fc2ec18d57f70b4d6dee7ba0dfc5c94d55814cd4b9446`.
Dart smoke: `151f7b8b031e488c89ae3b425562ff755ce53dbd13c249de2de3ce3b8d43abe7`.
Daemon: `c82d9c18b92f5bbab2d9984987179286fdb4e8d2f0f669c95eb60fbc2292b6b6`.
No workstation tests or proxy/route changes; no GitHub push.

The main Flutter Windows startup guard remains. Actual runner/window/tray,
autostart/notifications, GUI crash/shutdown and installed ordinary-user product
acceptance are still required. Pending-write/EOF backpressure stress also remains
part of UI transport acceptance; this increment does not close W09 or the port.

## W09 native Windows runner and desktop lifecycle

The actual Flutter Windows runner now uses the authenticated UI bridge and the
shared screens/controller. Linux keeps its Unix socket, XDG autostart and
notification adapters. CMake builds and packages the co-located unprivileged Go
bridge from the same checkout; no PATH helper lookup is introduced. The runner
adds an existing-user/session single-instance guard, branded multi-resolution
ICO, current-user GUI-only autostart, and best-effort notification-area messages.
The Windows tray requires an explicit popup on right-click; a regression covers
that behavior without changing Linux native menu handling.

Frozen source archive:
`d2481d5251ef358bf1a679258f5cfe551703e77a798805e16156e7c6602a76b9`.
Validation on Windows 11 Pro 26300.9457, Flutter 3.47.4 / Dart 3.13.3:

- Analyze clean, all 157 applicable unit/widget tests PASS, release build PASS.
- Real desktop opens empty-bootstrap onboarding and Settings; opening the GUI
  starts the zero-argument SCM daemon and leaves it IDLE/disconnected.
- Repeated launch of the final build retains one GUI PID and the same daemon
  PID. Activation checks the target window's user SID before sending a message.
- Start at login ON writes only the quoted GUI executable to HKCU Run. A real
  logoff/login starts one GUI, with native API status proving daemon IDLE and no
  auto-CONNECT. OFF removes the value; another real login followed by a
  45-second observation has zero UI/helper/daemon processes and stopped service.
- The window-close confirmation/cancel and confirmed Shutdown were exercised.
  On the final build, the real tray menu and confirmed Shutdown also pass:
  zero UI/helper/daemon processes, SCM stopped with exit code zero, no restart.
- Native product cleanup passes; DNS, route and adapter snapshots have zero
  delta against baseline. Temporary SCM services and lab tasks are removed;
  the exact lab installation/profile were moved into private guest evidence,
  not destroyed. Autostart remains OFF.
- Mini-PC: analyze clean, all 170 tests PASS, Linux release build PASS. Only
  the existing vendor appindicator deprecation warning remains. No installed
  mini-PC application was replaced, and no workstation tests were run.

Final Windows `irvio.exe` SHA-256:
`809af90439282eaa2b7d2d03336119e2511cd609bb98b12a9d2d021d6fa2d388`.
Packaged bridge SHA-256:
`a6283315892323ecc1dfa7600f9cb388b0c66a79bbe10eb64345c4cd9234b0a0`.
The production daemon is unchanged from the preceding transport milestone.
Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/windows-ui/`.
This evidence directory contains build/test logs, screenshots and bounded
state JSON, not the private daemon profile/keys.

Harness limitations are explicit: SSH/session-0 MainWindowHandle was not a
valid interactive window oracle; QMP screenshots replaced it. An initial login
observer timed out before keyboard-driven login completed, then passed after
actual login. The first native tray probe used an empty window title instead
of `IRVIO`; its error is retained separately from the corrected observation.
These were lab observation errors, not successful product tests.

This desktop run used the lab administrator account with UAC disabled. The
previous standard-user bridge/SCM gate remains valid, but it does not substitute
for a complete ordinary-user GUI acceptance run. Connected GUI-death semantics,
visible OS notifications, DPI/Unicode/file-picker coverage, externally disabled
StartupApps reconciliation and installed update/uninstall remain unproven.
No CONNECT or new network fault campaign was performed in this UI increment.
W09 and the full Windows port are still incomplete.

## W09 pending-write / EOF deadline regression

A focused in-memory transport reproduced an actual shared UI client defect:
when the writer was blocked, EOF completed an unobserved response future and
the caller kept waiting for the writer. The original test reported both an
unhandled connection-closed error and a two-second test deadline failure.

The client now observes the response/error immediately and applies its existing
per-operation deadline to queueing/writing as well as the reply. Write failure
or timeout closes the API channel and fails pending calls; queued frames check
the closed flag before entering the transport. This prevents a still-queued
CONNECT from being sent later after a transport deadline. It cannot undo a
command already received by the daemon, and does not change network retry,
selection or VPN teardown budgets. Closing this IPC channel still sends neither
DISCONNECT nor SHUTDOWN.

Three regressions cover blocked-write EOF, timeout fencing a queued CONNECT,
and explicit close followed by a late write failure. The 13-test shared/process
transport set passes with five shuffled seeds on each OS. Full UI analyze is
clean; Windows has 160 tests PASS and mini-PC Linux has 173 tests PASS. These
backpressure tests are controlled transport fixtures, not a claim of a native
Named Pipe saturation test or real VPN session survival.

Both release builds PASS. The Windows runner executable is unchanged; the
updated Dart AOT lives in `data/app.so`, SHA-256
`fa97e34ae0e7eb07096c2ad8d74c347d45fa0df1ea6603618fa80d02a5f1c489`.
Linux `lib/libapp.so` SHA-256:
`5ccf431d77a8d904476aecd11d230cf815d24de6a32dccb335e486cb62486533`.

The first reproducer attempt could not compile because the mini-PC temporary
filesystem quota was exhausted. It was stopped; a task-specific temporary
directory on the host disk allowed the original-code failure and fixed-code
passes to be recorded. No installed mini-PC application was changed.
Source snapshot: `5a97b3049e69d40e6cfa100b694c51a9fc60bc358c6154df68b28f80fc0f055a`.
Evidence is retained under `evidence/windows-ui/{linux,windows}/backpressure-*`
in the existing Windows lab directory. This closes the identified response
observer race, not all remaining W09 or Windows-port acceptance requirements.

## W06 echo follow-up — forwarded injection under production protection

The rejected Windows ICMP API is not the only available path. A new isolated
driver experiment uses the existing `OpenForwardSender`, with an exact inbound
echo filter additionally matching a random 128-bit payload nonce. The packets
are synthetic and target only the QEMU gateway, not the public network. No
product runtime import, WFP permit, wire protocol or production behavior changed.

Two retained runs establish the progression:

- `W06-echo-injection-01`: three compiler-instrumented checkptr/shuffle repeats
  pass with target-only ALE/transport ICMP denial. Ordinary-user echo works
  before/after protection and is denied while synthetic forwarded echo works.
- `W06-echo-injection-02`: the final test additionally installs the actual
  `killswitch.NewWindowsBackend` NO_EXIT policy, with no tunnel/underlay grants.
  In every repetition ten forwarded echo requests pass under target denial and
  ten pass under full production protection; the ordinary-user child remains
  denied before and after each batch, and succeeds after teardown.
- The ordinary oracle uses a newly created account with a real primary token,
  explicitly without the Administrators group. It is not an elevated lab-user
  process or an assumed process identity. Successful ordinary echo while the
  receive capture is live verifies that its unrelated replies are not consumed.
- Native filter evaluation passes the positive packet plus 14 negatives:
  nonce, identifier, sequence, source, recipient, type/code, truncation,
  interface, direction, protocol, fragmentation and IPv4 options.
- Final native vet passes. A separate process removes/inspects the exact WFP
  experiment owner, then the harness removes its driver, service and account.
  DNS/routes/adapters equal baseline. Both runs used the existing signed pinned
  driver. No tests or proxy/network changes ran on the workstation.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-echo-injection/`.
Frozen source archive SHA-256:
`ec009eb12ac5e2f80db9679f8a0aae979ac3f9b96ed915ccfbc4790efc713fda`.
Final instrumented probe SHA-256:
`d7421fb1c0cb76347198a959a744109fdfa6e3bc61e70965d4793613da631a1b`.
The child oracle SHA-256:
`ed663341a51b5126c160003ef48ded42445e972b548e0fcfee174e89b3d54aff`.

This changes the next implementation decision: use the existing physical
injection path for an owned echo translator rather than introduce an unsafe
host ICMP exception. It does **not** close W06-ICMP-001: lease admission,
bounded per-request state, original payload/identifier restoration, revoke,
expiry, generation changes, full error quotes/PMTU and encrypted product
acceptance remain required. In particular, a short ICMP error quote lacks the
nonce; exclusive ownership must not be inferred from a 16-bit echo ID.

## W06 echo implementation — native backend and encrypted lease lifecycle

Baseline: `5e7c29419948f7e29dda310de832822cb21e6336`, branch `windows-port`.
The previous primitive gate now feeds the Windows production NAT backend.
`ReserveEcho` is optional for generic fixtures; Windows installs the actual
exact WinDivert capture. There is no native ICMP API bypass, new WFP permit,
lease protocol change, or Linux runtime substitution.

Implementation invariants:

- Existing lease/source/Safety Policy admission precedes allocation. A fresh
  128-bit nonce and randomized identifier/sequence bind each reply to its
  physical interface/source, remote, capability token and lease lifetime.
- A saved payload prefix restores the original bytes, identifier and sequence.
  Payloads of at least 16 bytes do not grow on the wire; shorter payloads are
  temporarily padded and restored to their original size.
- At most 128 requests globally and 16 per lease, additionally charged against
  shared NAT capacity; lifetime is at most five seconds. No whole request
  payload is retained. Completion consumes the capability once.
- Cancellation/lease expiry are rechecked after capture allocation. Revoke,
  timeout and network generation changes retire captures. Failed closes remain
  in the existing cleanup ownership list rather than disappearing from stats.

Evidence and scope:

| Gate | Result | Scope |
| --- | --- | --- |
| Generic echo tests | PASS | Payload sizes 0–65507, checksum/tuple/nonce/token/generation fences, cancellation, expiry, renewal/capacity, partial allocation and cleanup retry |
| Mini-PC vet + race/shuffle ×10 | PASS | `exitnat`, `exitbridge`, `tunnellease`; `icmpv4` has no direct tests, its parser is exercised by `exitnat` |
| Windows vet + checkptr/shuffle ×5 | PASS | `exitnat`, `exitbridge`, `windivert`; optional native tests without explicit fixture are skipped |
| Production capture under protection | PASS | Prior primitive gate rerun with the new `OpenEchoReplyCapture`: ordinary-user echo denied under production NO_EXIT, forwarded nonce-owned echo permitted |
| Native backend ×3 | PASS | Five active leases, 50 replies/repetition, colliding original ICMP IDs, restored payloads, revoke and zero owned captures afterward |
| Native AWG/lease fixture ×3 | PASS | Five real AWG clients; 14 replies/repetition, zero application retries, Core renewal and release isolation, source spoof dropped |
| Encrypted echo resource lifecycle | PASS | 63 warmup cycles until 20 stable highwater observations, then 100 cycles of five leases, 14 replies/cycle |
| Native harness cleanup | PASS | Driver service removed; DNS/routes/adapters byte-equal to pre-gate baseline |
| Windows normal + trimpath builds | PASS | `cmd/irviod`, built on Windows, not installed as a release |

Resource measurements after warmup: 505 total handles and two goroutines;
sampled cycles 10–100 show 506–508 handles and two goroutines. File handles
remain 19, registry handles 42, I/O completions six, timers 44. The small
change is within the existing bounded runtime thread/event allowance, not
claimed as literally zero growth. Sampled heap allocation oscillates with GC,
approximately 7.3–16.3 MB; final 8.24 MB. This is a bounded component lifecycle
proof, not the complete desktop Connect/Disconnect or Private VPN stress gate.

The native oracle was QEMU's isolated `198.51.100.2` gateway on a second
e1000e adapter, source `198.51.100.15`, `restrict=on`, no extra default route or
DNS server. This is a documentation-prefix fixture, not an Internet host.
Its use avoids weakening the production private-address prohibition to reach
the VM's ordinary `10.0.2.2` gateway. The isolated adapter remains lab
infrastructure for further ICMP/PMTU checks; harness cleanup refers to the
state after this fixture was configured. Workstation networking/proxy and
installed mini-PC product were not changed.

Two setup failures were not product defects: the attempted USB NIC had no
usable guest network driver, so a supported e1000e was added on a VM restart;
the first ping ran before address DAD completed, then the independently
repeated preflight passed. A new renewal unit fixture initially exceeded the
Core maximum lease validity; it was corrected to test generation renewal
without violating the existing TTL contract. No product limit was relaxed.

Artifacts: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-echo-backend/`,
with final native evidence in `windows/W06-echo-backend-03` and mini-PC results
in `linux`. Source delta over the baseline (only specified implementation,
test and harness files) SHA-256:
`c12a624087900681ef78dfa4187c6bd9a9be056bd24264179901f218b727656a`.
Native backend instrumented test binary SHA-256:
`22ec3e84f60cedabb441c8531b9c303264b9c19533f0d6b60ac9bb4f6577780a`.
Normal Windows daemon SHA-256:
`d37337db3a8650478d030c5340691ccba4329bd6b46df0e71496732572d935f2`.
Trimpath Windows daemon SHA-256:
`9d97c0c91723773f54496bfb163f9d5470bd1894a1e000c74e960c8fd7939210`.

Still open: echo error quotations (short quotes do not carry the nonce),
real-product ICMP through Wintun/discovery/ICE, full PMTU/fragmentation and
dual-role/crash acceptance. The encrypted fixture uses channel TUNs and
test-coordinated endpoints/keys, not full product discovery. W06 and the
Windows port remain incomplete.

## W07 product ICMP / UDP PMTU — bridge delivery defect fixed

Baseline: `da4d120`, branch `windows-port`. This follow-up uses an actual
zero-argument Windows SCM daemon, real bootstrap discovery, a newly generated
Private invitation, approval, exact-target ICE/AWG/TunnelLease and the installed
Linux mini-PC client. Neither peer endpoints nor fake candidates were supplied.
The Linux client binary remains
`d9a9dba106e2def44cb4c963ecdc4e8e77b9c5f6334b10ae876680e05eeb85f8`.

The Windows VM's ordinary public ping failed independently of IRVIO: its QEMU
user network cannot use host ping sockets with the unchanged `ping_group_range`.
A QEMU Ethernet sidecar therefore emulates only a documentation-prefix router
(`203.0.113.77`, ICMP error source `203.0.113.78`). It is outside Windows, matches
only explicit synthetic probes, and does not intercept/change ordinary traffic.
Guest echo to it passed before the VPN gate. This is not a public Internet
ICMP availability claim. No workstation route/proxy/firewall/sysctl was changed.

### W06-ICMP-002 — translated errors lost before AWG

Initial product echo succeeded, but a 1200-byte DF UDP probe did not receive the
oracle's type 3/code 4 error. Source triage found `BridgedResources.deliver`
re-parsing every translated packet with the ordinary data/echo parser. That
parser rejects ICMP errors, so the backend correctly translated the quote but
the bridge dropped it before AWG. Existing translator-only tests missed this
composition boundary.

The focused reproducer fails on original code for unreachable, fragmentation
needed, time exceeded and parameter problem. The narrow fix lets this private
delivery callback validate ICMP errors with the existing `icmpv4.ParseError`
and select the recipient from that validated result. Engine lease/flow/token
authorization still precedes the callback; no broad receive filter, host
exception, protocol change or relaxed admission was added. The regression also
checks malformed checksum rejection, restored quote ownership/MTU and revoke.

### Product and regression results

- Before the bridge fix, two Private connections already pass all 36 echo
  probes (payloads 0, 1, 15, 16, 32, 1200; three each per connection), native DNS,
  no-proxy Internet HTTPS and two separate 1 MiB downloads.
- After rebuilding/restarting the Windows host with the same identity/grant,
  the identical UDP PMTU probe passes: originating Linux socket receives
  `EMSGSIZE`, ICMP origin, type 3/code 4, MTU 1000; kernel path MTU becomes 1000;
  the next oversized DF send fails locally and a 900-byte UDP payload round-trips.
  The configured IRVIO/AWG/Wintun MTU was not changed.
- A fresh invitation on the fixed binary then passes two more connections
  using the same saved approval between them: 36/36 echo replies, zero packet
  loss in every size case, DNS/HTTPS and two 1 MiB downloads. Kernel route lookup
  selects the IRVIO tunnel; Windows reports one downstream while connected and
  zero after disconnect. The saved Linux target is removed afterward.
- Mini-PC affected vet and race/shuffle ×10 pass (`exitnat`, `exitbridge`,
  `tunnellease`). Windows affected vet and checkptr/shuffle ×10 pass
  (`exitnat`, `exitbridge`). Normal and trimpath Windows builds pass.
- Windows natural cleanup passes the native WFP/intent gate and exact
  route/DNS/adapter comparison; owned daemon/recovery/driver services are removed
  and the protected disposable profile is archived locally, not exported.
- Linux finishes DISCONNECTED, no hosting, no private target, zero saved targets.
  DNS restoration is **not evidenced**: the before/after files are empty because
  the SSH mount namespace used a resolvectl shim (see correction below).
  A raw route difference is retained: NetworkManager temporarily
  raises the DHCP default metric 101→20101; its own connectivity recheck reports
  `full` and restores exact baseline route equality, without manual route edits.
- Both QEMU filter objects/character devices are removed and the oracle exits.

Two harness issues were corrected without changing Core: the old Linux API
helper lacked `private-remove-target`; the first focused bridge test stopped
its reader after one packet, so the existing AWG Read-boundary revoke fence
correctly waited. The fixture now keeps its reader alive through cleanup and
uses a bounded test timeout. The failing product PMTU result and original-code
regression are preserved separately from these harness failures and fixed runs.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w07-product-icmp/`.
Only allowlisted results were exported: no invitations, keys, profiles or
downloaded content. Reusable synthetic router and originating-socket probe:
`tools/windows/w07/{qemu-icmp-oracle.py,udp-pmtu-probe.py}`.
Fixed Windows daemon SHA-256:
`caaa2bf954761b0cbe7ff25f005b4d828fe9bc7ca6cf686caae2754397cd7d53`.
Trimpath SHA-256:
`cd76342329258678310e76eb7e612c34b51bf83eb795cc8f6046a6b4c367bef9`.

This closes the observed bridge delivery defect and proves product echo plus
one real originating-socket UDP PMTU path against a controlled router. It does
not close short echo error quotations, TCP PMTU, fragmentation/IPv6, Global
dual-role, Windows↔Windows Private, crash/power, installer or whole-port acceptance.

## W07 product TCP PMTU — originating TCP stack recovery

On source baseline `c1301e6`, the same normal Windows daemon from the UDP fix
was installed into a fresh protected SCM fixture. No production changes were
required. Mini-PC Linux imported a new private invitation, enrolled and reached
CONNECTED through actual bootstrap/discovery/ICE/AWG/TunnelLease. Its kernel
route to synthetic `203.0.113.79` selected the IRVIO tunnel.

`tools/windows/w07/tcp-pmtu-probe.py` opened a real TCP socket through Windows
Exit to the bounded QEMU Ethernet oracle. The oracle completed the TCP handshake,
advertised MSS 1460 and sent minimal ICMP type 3/code 4 quotes for DF packets
larger than 1000 bytes. It acknowledged only contiguous expected payload bytes.

- Initial connected socket PMTU: **1280**; final PMTU: **1000**.
- All **8192** application bytes arrived and the oracle's success response
  reached the originating socket; elapsed **623.02 ms**, not a distribution.
- Independent oracle events include six rejected 1280-byte IP packets followed
  by TCP packets of 1000, 600 and 792 bytes and successful cumulative delivery.
- Neither tunnel MTU nor WFP/security policy was changed for this experiment.
- Mini-PC finishes DISCONNECTED with zero saved targets. Routes are byte-equal
  to baseline immediately after cleanup. The empty DNS snapshots do not prove
  DNS restoration (see correction below).
- Windows explicit Disconnect/Shutdown passes the native idle cleanup gate;
  route/DNS/adapter comparisons are empty. Owned daemon/recovery/WinDivert
  services were removed and profile/binary recoverably archived in the fixture.
- QEMU filters and character devices were removed and the oracle exited 0.
  Workstation proxy/routes/firewall were untouched.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w07-product-tcp-pmtu/`.
Windows protected original fixture: `C:\IRVIO-Lab\W07-tcp-pmtu-01`.
Mini-PC original fixture: `/tmp/irvio-tcp-pmtu-client-O2bFED`.
Only allowlisted results were exported, not invitations, keys or profiles.
Daemon SHA-256 remains
`caaa2bf954761b0cbe7ff25f005b4d828fe9bc7ca6cf686caae2754397cd7d53`.
The final helper scripts also pass Python syntax compilation on the mini-PC.

This closes one real TCP path-MTU recovery scenario through Windows Exit, not
general fragmentation/IPv6, all TCP variants, dual-role or whole-port acceptance.

## W06 full-product Global dual-role — one downstream, two admissions

Baseline source: `4482888`; unchanged normal Windows daemon SHA-256
`caaa2bf954761b0cbe7ff25f005b4d828fe9bc7ca6cf686caae2754397cd7d53`.
The disposable SCM service used zero daemon arguments, canonical protected
profile/MMDB paths and the real three-server bootstrap bundle. Windows Global
CONNECT selected NL and enabled contribution. The installed mini-PC Linux
daemon selected RU through ordinary country discovery; no injected peer,
Rendezvous endpoint, static lease, invitation or direct AWG setup was used.

Results:

- Windows physical WAN egress and own NL Exit egress were distinct. Windows
  observed one downstream client while Linux was connected.
- Linux completed **20** DNS/HTTPS/1 MiB download iterations, then DISCONNECT.
  A second real CONNECT/DISCONNECT completed **3** more iterations.
- All **23/23** downstream HTTPS egress results equal physical Windows egress,
  not the NL upstream egress. All downloaded bodies had the expected length.
- **131** Windows status samples remain CONNECTED with exactly one unchanged
  `connected_since`; all recorded own HTTPS egress results remain the NL Exit.
  Its downstream count changes 0→1→0 across each admission/release, without
  dropping/replacing the own upstream. Five additional HTTPS probes pass after
  the first RELEASE. The checked-in observer passes the second admission.
- A Windows HTTPS process explicitly bound to physical source `10.0.2.15`
  fails with curl exit 7 and a zero-byte response body while protection is on.
  After explicit Windows Disconnect, the same command succeeds with physical
  baseline egress. This is a scoped negative control, not an all-traffic capture.
- Windows native WFP/intent cleanup passes; route/DNS/adapter deltas are empty.
  Owned services are removed and profile/binary recoverably archived. The client
  ends DISCONNECTED with no private targets; its installed binary is unchanged.
- First Linux immediate route comparison records only NetworkManager metric
  101→20101. Later routes match the original baseline, its normal connectivity
  check returns `full`, and final routes compare byte-equal without manual
  route edits. Second-cycle immediate routes also compare equal. Empty Linux
  DNS before/after files are not restoration evidence (see correction below).

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-product-dual/`.
Original Windows fixture: `C:\IRVIO-Lab\W06-product-dual-01`.
Mini-PC fixtures: `/tmp/irvio-dual-product-client-{4CVQWr,A05tGA}`.
Reusable observer: `tools/windows/w06/product-dual-role-observe.ps1`.
One attempted SSH evidence copy while Windows was protected was cancelled;
management continued through QGA, without adding an underlay bypass. Allowlisted
evidence was copied after natural teardown; profiles/keys were not exported.

This proves a real Global CLIENT+EXIT product path and upstream survival across
two downstream admissions. It does **not** close the required five-concurrent-
downstream product gate, 100 product cycles, general leak/security matrix or
whole-port acceptance. Workstation proxy and network configuration were unchanged.

## W06 five concurrent product downstream clients — functional pass, performance open

Source baseline `0f91058`; Windows daemon SHA-256 remains
`caaa2bf954761b0cbe7ff25f005b4d828fe9bc7ca6cf686caae2754397cd7d53`.
The zero-argument Windows SCM fixture performs Global CONNECT to NL with
contribution enabled. Five fresh Linux production daemons use independent
network namespaces, macvlan/DHCP addresses, identities and profiles on the
mini-PC. They are explicitly client-only engineering fixtures, not five
zero-argument desktop acceptance tests. Each learns the Windows RU Exit through
the original bootstrap bundle, DHT and Rendezvous, then real ICE/AWG/TunnelLease;
no hidden Rendezvous/Exit endpoint or synthetic lease is injected.

Measured fixture `EqhlWE`:

- All five connect to the same Windows PeerID with distinct client identities.
  Ten full 1 MiB downloads, DNS and HTTPS probes pass. All ten observed egress
  addresses match physical Windows WAN, not its own NL upstream.
- 102 Windows status samples remain CONNECTED with the same `connected_since`;
  downstream counts include 0 through 5 and return to 0 after explicit releases.
  Recorded own HTTPS egress remains NL.
- Some initial attempts fail STUN refresh, signaling or ICE and retry. The last
  clients connect approximately 138 seconds after startup. This is an open
  discovery/connection long-tail observation, not a healthy latency verdict.
- The previous `vVqTml` run connects all five but fails its 25-second download
  deadline. Its partial bodies and failure evidence are retained. Raising only
  the lab measurement deadline to 120 seconds allowed measurement of completion;
  no product timeout was changed or failure relabeled as PASS.

| Concurrent 1 MiB downloads | Samples | Min | Median | Max |
| --- | ---: | ---: | ---: | ---: |
| Linux direct before clients start | 5 | 0.365 s | 0.515 s | 0.555 s |
| Linux through Windows Exit | 10 | 27.909 s | 34.558 s | 48.951 s |
| Windows direct after cleanup | 5 | 0.292 s | 0.388 s | 0.536 s |

This is a substantial throughput problem in the Windows Exit/AWG/virtualized
path, not explained by generic external bandwidth: both direct controls are
fast. Root cause is not yet established. QEMU header capture records a SYN MSS
of 1240 but synthetic peer segments carrying 1440 bytes; that observation alone
does not establish causality. A per-packet native source/interface validation
cost is being measured separately; no protection fence has been removed.

The 180-sample Windows resource series spans 892–1388 handles and about
75.8–100.9 MB RSS with changing active flows. It is not a 100-cycle leak proof.
Windows native idle cleanup passes and routes/DNS/adapters match baseline;
owned services are removed and the disposable profile archived. Linux root
routes, real nonempty DNS snapshots and nftables match baseline, forwarding
returns to its original 0, DHCP processes/macvlan namespaces are removed and
only the preexisting management namespace remains. QEMU capture is detached.

Earlier extra-NAT fixtures `jVhTKy` and `ghYZWe` discovered the candidate but
failed ICE. Even explicit lab DNAT received no matching inbound ICE. They do
not prove that this added topology had a usable direct path and are not
classified as a confirmed Core defect. Preflight failures and the first native
Windows baseline's process-exit-code collection error are separate lab artifacts;
the corrected `direct-windows-02` baseline completed with five verified exit 0s.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-product-five/`.
Original Windows fixture: `C:\IRVIO-Lab\W06-product-five-01`.
Original mini-PC fixtures: `/tmp/irvio-five-product-{jVhTKy,ghYZWe,vVqTml,EqhlWE}`.
The reusable harness is `tools/windows/w06/product-five-clients.sh` with
`product-five-dhcp.sh`; the parameterized checked-in form passed syntax checks,
while the equivalent fixed-path lab form produced the measurements above.
Only allowlisted evidence was exported, not profiles, private keys or invitations.

### Correction: Linux DNS evidence from previous SSH mount namespace

The mini-PC SSH environment inherited a mount namespace where resolvectl was
replaced by a lab shim. Entering only PID 1's network namespace did not escape
that mount namespace. Five exported DNS before/after pairs are zero bytes:
W07 product ICMP (`qlnc43`, `MUEzOp`), W07 TCP PMTU, and W06 one-client dual-role
(`4CVQWr`, `A05tGA`). Their raw evidence is preserved; previous DNS-restoration
claims above are withdrawn. Successful DNS traffic is still traffic evidence,
not proof of native resolver configuration restoration. Native Windows DNS
cleanup evidence is unaffected.

The new five-client host oracle enters both PID 1 network **and mount** namespaces
and obtains matching 603-byte DNS snapshots. Its clients deliberately use an
isolated DNS launcher and per-namespace resolv.conf, so that run proves host
state preservation, not five native Linux DNS lifecycle transitions and not
retroactive restoration for the earlier runs.

## W06 Windows packet-path source lookup — measured bottleneck and bounded fix

`windivert.validBinding` calls `winroute.ForSource` before every injected packet
and after every captured reply. Previously, ForSource called net.Interfaces,
then Addrs for every interface: multiple full adapter inventories per packet.
An opt-in native benchmark on the cleaned Windows VM measured **6.770–6.988 ms**
per lookup, approximately **84 KB / 77 Go allocations** per call. The existing
individual live binding validation takes only **17–18 microseconds**.

ForSource now obtains one fresh native unicast address table per call using
[GetUnicastIpAddressTable](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-getunicastipaddresstable),
rejects missing, ambiguous or unready source owners, then performs the unchanged
live binding validation. Native table memory is freed on every return. There
is no cached success, timer window, removed packet check, protocol change or
new firewall exception. Index/LUID, DAD/lifetime, interface readiness, forwarding
and weak-host restrictions still fence the result. Table size is bounded.

The same native benchmark (three 2-second repetitions) measures **33.35–33.88
microseconds** per lookup with **zero Go allocations**; native API allocation is
not included in Go's allocation counter. This is about 200× faster for this
specific operation, not a claimed 200× product throughput gain. Windows vet
passes for winroute/windivert/exitnat; checkptr/shuffle ×10 passes those packages.
Privileged tests gated off in that package invocation are not claimed as run.
New table layout and source-owner regressions cover IPv4/IPv6, missing or
unrelated sources, duplicate LUID/index ownership, tentative/expired rows and
invalid identities.

### Repeated five-client product measurement

Fresh Windows fixture `W06-product-five-02`, Linux fixture `OOZoBp`, and the
checked-in parameterized harness repeat the same production path with the
fixed Windows daemon. All five Linux clients connect; **10/10** full 1 MiB
downloads, DNS and HTTPS pass. All ten downstream egress probes equal physical
Windows WAN, whereas all observed own Windows egress remains NL. **52** Windows
status samples retain one unchanged CONNECTED session; downstream counts reach
five then return to zero. Initial connection retries still occur and are not
addressed by this packet-path change.

| Concurrent 1 MiB downloads | Min | Median | Max |
| --- | ---: | ---: | ---: |
| Before, ten downloads | 27.909 s | 34.558 s | 48.951 s |
| After, ten downloads | 0.725 s | 3.664 s | 6.935 s |
| After, first five-client round | 5.564 s | 5.681 s | 6.935 s |
| After, second five-client round | 0.725 s | 0.873 s | 1.765 s |

This confirms a material bottleneck was removed; it does not explain the
remaining first/second-round difference or establish final throughput capacity.
Physical-bound Windows HTTPS remains blocked while CONNECTED (nonzero curl
exit, zero response bytes) and succeeds after Disconnect. This is the same
scoped negative control, not a comprehensive packet leak matrix. Windows native
WFP/intent cleanup passes and route/DNS/adapter deltas are empty. Mini-PC real
DNS, routes and nftables match baseline, with fixture processes/namespaces
removed. Workstation networking and proxy were not modified.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-binding-bench/` and
`/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-product-five-fixed/`.
Normal daemon SHA-256:
`f87f6b12040b916c08fa562b33ff7cbbad6da8d2a60f31d74dfd466371f7b34a`.
Trimpath SHA-256:
`017d0b676c5e4b82b56089e1dc897c932d676a28cbf94d9134bc480c0f0157af`.
The Linux installed daemon is unchanged. The lab helper's bootstrap extraction
uses the existing bundle parser; affected helper vet/bootstrap tests pass on
the mini-PC. No tests ran on the workstation.

The additional elevated native source-owner lifecycle gate passes **3/3** with
checkptr enabled. It creates a real Wintun source, verifies its exact owner,
then verifies lookup and stale binding rejection after adapter removal. This
Windows build rejects duplicate source provisioning at CreateUnicastIpAddressEntry
with ERROR_OBJECT_ALREADY_EXISTS; therefore actual two-owner table rejection is
covered by the deterministic row tests, not falsely claimed as a native result.
Route/DNS/adapter deltas are empty. Native evidence is in
`w06-binding-bench/native/04/`.

Earlier native fixture attempts are preserved: an overlong adapter name was
correctly rejected by the existing config validator; PowerShell's Stop handling
mistook driver stderr for a runner error; the next attempt incorrectly assumed
Windows would permit duplicate address provisioning. The final runner collects
native stdout/stderr and exit status separately, and the test explicitly
distinguishes kernel rejection from lookup rejection. These harness failures
are not product failures or successful native gates.

## W06 remaining download tail — DNS/underlay isolation

Baseline `dc5000a`, same fixed normal Windows binary, fresh SCM fixture
`W06-product-five-03`, mini-PC fixture `PXAzH2`. No production code changed.
The five-client harness adds curl DNS/TCP/TLS/first-byte timing and opt-in
160-byte HTTPS header capture in each Linux namespace. A separate QEMU filter
records 160-byte physical headers, including DNS; it is detached afterward.

All five real clients connect, all ten full 1 MiB DNS/HTTPS/download rounds
pass and all ten downstream egress results match physical Windows egress.
56 Windows status samples remain CONNECTED with one `connected_since`, own
HTTPS egress stays NL and downstream counts go 0→5→0. Automatic connection
retries remain visible; this experiment does not fix discovery latency.

| Stage, ten downloads | Min | Median | Max |
| --- | ---: | ---: | ---: |
| Total | 0.697 s | 6.002 s | 7.079 s |
| DNS lookup | 0.016 s | 5.041 s | 5.104 s |
| TCP after DNS | 0.028 s | 0.041 s | 0.057 s |
| TLS after TCP | 0.041 s | 0.065 s | 0.117 s |
| Request to first byte after TLS | 0.054 s | 0.067 s | 0.264 s |
| Response body after first byte | 0.549 s | 0.823 s | 1.896 s |
| Entire post-DNS path | 0.680 s | 1.034 s | 2.069 s |

Seven of ten lookups take approximately five seconds. QEMU's physical capture
shows the initial paired A/AAAA queries leaving the Windows guest on the same
UDP tuple. In slow cases only one initial response returns to the guest; the
missing response arrives after a client retry approximately five seconds later.
This rules out the Windows reply-capture/translation dropping a response that
had already arrived at that capture point. Header-only TCP analysis reports
52 retransmission flags and 24 fast-retransmission flags among 15,073 selected HTTPS packets, no
lost-segment flags or zero-window flags. These are analyzer observations, not a
loss-rate proof or proof that all TCP behavior is optimal.

### Independent no-IRVIO controls

After natural product teardown, `dns-pair-probe.ps1` uses native .NET UDP in
Windows with **no IRVIO service or WinDivert service installed**. The physical
source sends A then AAAA to the same public resolver on one fresh socket.
An independent Python/socket probe runs directly on the mini-PC host with its
installed daemon DISCONNECTED, no contribution and no private session.

| Direct control, resolver 1.1.1.1 | Pairs | Missing A | Missing AAAA |
| --- | ---: | ---: | ---: |
| Windows, zero gap | 10 | 10 | 0 |
| Windows, 20 ms diagnostic gap | 10 | 0 | 0 |
| Mini-PC Linux, zero gap | 10 | 10 | 0 |
| Mini-PC Linux, 20 ms diagnostic gap | 10 | 0 | 0 |

The mini-PC observation repeats in a second independently recorded batch with
the same counts. No production setting or DNS timeout was changed. Probe
receive budgets are 800 ms without retry; successful records have DNS rcode 0.
Thus the paired-query loss is an **external DNS/underlay observation**, not a
Windows-only or IRVIO-induced defect. Its exact location (LAN router, upstream
path or resolver behavior) is not established; calling it specifically a QEMU
bug would be unsupported. Do not add sleeps, replace DNS or weaken Core to hide
this environment behavior. The earlier per-packet ForSource bottleneck remains
a separately confirmed and fixed Windows implementation issue.

Windows natural WFP/intent cleanup passes, route/DNS/adapter deltas are empty,
and forced physical HTTPS works after Disconnect. Mini-PC host routes, real
DNS and nftables match baseline; all five fixture namespaces/processes are
removed. Both QEMU capture filters are detached. The workstation proxy/routes
are unchanged. TShark 4.6.4 was installed on the mini-PC after traffic completion
for offline analysis; the initial installation attempt in its isolated SSH
namespace could not resolve the package mirror, then succeeded from the actual
host namespaces. This tooling issue is not an IRVIO networking failure.

Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-dns-stages/` with
allowlisted Linux/Windows results, local header captures and direct controls.
No profiles, private identities or invitation material were exported. This
separates the observed multi-second tail from product throughput; it is not a
high-bandwidth capacity gate or completion of the full Windows port.

## W06 Windows session traffic counters

Baseline `84f162f` plus this implementation. The previous Windows source reported
CLIENTS but left byte counters unavailable. A lifetime-owned atomic accumulator
now supplies the existing Local API/UI fields without a wire or selection change.
`MY TRAFFIC` remains own upload + download; `SHARED` remains downstream upload
to the physical Internet, not downstream download. No UI-specific Windows
counter format was introduced.

Measurement occurs on accepted plaintext IPv4 packets, including IP headers and
retransmissions. Upload is counted when the live bridge hands a packet to AWG;
own download follows a successful TUN write; shared upload follows successful
lease-authorized NAT forwarding. The exact upstream TCP TunnelLease endpoint,
downstream control, encrypted transport overhead, rejected/retired packets and
downstream replies are excluded. The readout does not acquire the plane lifecycle
lock and cannot select a route or alter a lease. The plane owns counters across
bridge generations and teardown; the existing product session owns reset and
final retention. These are traffic indicators, not application-byte billing;
Windows counts TUN acceptance before subsequent host filtering, whereas Linux
uses its existing firewall/conntrack measurement points.

Focused component tests check two bridge generations, own/shared separation,
control exclusion, retired queued packets, forwarding errors and malformed
input. The encrypted five-peer Windows generation test checks exact final
totals (39 own upload, 39 own download, 208 shared upload bytes), including
cleanup failure/retry. A runtime test proves captured ownership survives daemon
detachment and byte sampling remains available during the lifecycle lock.

Verification on the actual test hosts:

- Mini-PC: affected component `go vet`, and `go test -race -shuffle=on -count=10`
  for exitbridge, exitnat and trafficstats PASS.
- Windows: affected `go vet`; checkptr=2, shuffled ten-repeat component suites;
  checkptr=2, shuffled ten-repeat `TestWindows(Exit|Traffic|ProductConnectedClients)`
  runtime selection PASS. Native opt-in tests are not implied by this command.
- Fresh zero-argument SCM product fixture `W06-product-traffic-01` connects to
  NL, contributes RU and uses the same existing live bootstrap/discovery path.
  First acquisition includes signal-route failures followed by autonomous
  recovery after approximately 70 seconds; this is preserved in setup evidence,
  not claimed to be fixed or explained by the counter change.
- Own 2 MiB download and 1 MiB upload succeed: delta 2,210,001 own download,
  1,174,212 own upload and **zero shared upload** bytes.
- Five fresh Linux clients (`xdNPRn`) each perform two full 1 MiB downloads and
  one 1 MiB upload through this Windows Exit. Ten download and five upload
  probes PASS. Downstream egress is the physical Windows WAN; Windows's own
  egress remains NL. All 35 observer samples retain the same own connection.
- During that interval shared upload grows 5,786,395 bytes, while own download
  grows only 138,866 bytes from the observer/host traffic, not the downstream
  10 MiB. CLIENTS reaches five and returns to zero on explicit RELEASE.
- Final Disconnect retains 1,231,134 own upload, 2,379,783 own download and
  5,786,395 shared upload bytes with `active=false`, `clients=0`, `complete=true`.
  The recorded samples are monotonic and retain one session ID.
- Physical-source-bound HTTPS is blocked under ProtectedMode and succeeds after
  Disconnect. Native WFP/intent cleanup passes; Windows route/DNS/adapter deltas
  are empty. Mini-PC host routes, real DNS and nftables match baseline; fixture
  clients and namespaces are removed. Owned Windows services are removed and
  profile/binary state archived recoverably inside the evidence fixture.

Normal daemon SHA-256:
`e5b1b576f82db02125398032c3da914432662ef32dc17a550f50b8488bf61a9a`.
Trimpath daemon SHA-256:
`15d4712f9d1fc1a544c9567e2d6f4e398b59550abaa73bc33c87b514302d27a0`.
Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w06-product-traffic/`,
allowlisted results only, without profiles or private identities. This proves
the API data consumed by the existing traffic widget, not a fresh ordinary-user
Windows GUI rendering gate. Workstation proxy/network settings are unchanged.

## W09 ordinary-user GUI and real SQLite profile deletion

Baseline `ee2d7c1`, followed by the Windows-only profile deletion correction.
The disposable `W09-standard-product-01` installation uses the release Flutter
runner, native named-pipe bridge, demand-start zero-argument SCM daemon and real
three-entry bootstrap. IrvioLab is removed from Administrators before a new
interactive login; the recorded token is genuinely non-admin. SCM grants only
query/start to that SID, executable files remain read/execute-only and profile
state remains SYSTEM/Administrators-only. This is a manual product fixture, not
an installer acceptance claim.

Observed gates:

- UI starts the stopped daemon, displays empty onboarding, imports bootstrap
  text and shows live locations. CONNECT shows the cyan pending state, then NL
  CONNECTED with real ordinary-user DNS/HTTPS and an exact 10 MiB download.
- Forced physical-source HTTPS is blocked while connected. The same ordinary
  account cannot open the daemon binary for writing or read the protected SID
  file. The GUI shows approximately 12 MB MY TRAFFIC after the download.
- `tools/windows/w09/product-gui-crash.ps1` kills only the owned GUI. Fifteen
  subsequent samples retain the original daemon PID and connected-since value;
  GUI/bridge exit, and HTTPS still exits NL. Starting the GUI task twice produces
  one new GUI attached to the unchanged active session.
- Save QR invokes the native Windows Save As dialog. It writes a valid PNG to
  `Documents/IRVIO тест/bootstrap.png`. Explicit GUI Disconnect returns IDLE and
  physical WAN; final traffic counters remain displayed.

**Confirmed defect:** Delete Profile rejected the real peer database. SQLite
creates `hydra.db` with trusted inherited SYSTEM/Administrators ACEs, but the
profile removal helper required a protected file DACL on every entry. Deletion
failed before removing anything; SCM restarted the daemon and bootstrap remained
configured. Earlier tests created a protected fake database or deleted an empty
profile, so they did not cover this state. The new real `peerstore.Open`/Close
regression fails on the old implementation with `unsafe Windows private state`.

The correction permits trusted inherited file ACEs only for the four known
SQLite database/journal filenames, beneath a pinned directory with a protected
trusted DACL. Removal still checks owner, type, single link, exclusive handle,
read-only state, parent ownership and all entries before deleting any entry;
the marker remains last. Key/bootstrap reads, writes and removal retain the
strict protected-DACL requirement. No file ACL is repaired or broadened.

Verification:

- Windows affected vet PASS; checkptr=2 shuffled ten-repeat winfile/profile
  suites PASS, including real SQLite, all four inherited SQLite filenames,
  inherited non-SQLite rejection, public/explicit-unprotected ACL rejection,
  symlink/hardlink/read-only/busy files, and closed-parent refusal.
- checkptr=2 shuffled three-repeat identity, AWG identity, bootstrap, private
  access, intent and daemon suites PASS. These commands do not imply opt-in
  privileged suites or native race coverage.
- `W09-profile-fix-gui-01` replaces only the fixture daemon, then deletes the
  same populated profile through the ordinary-user GUI: bootstrap becomes zero,
  old identity is absent, daemon PID remains 5780 and onboarding returns.
- Native QR import from the Unicode path restores the three bootstrap entries.
  Explicit CONNECT creates a different identity and reaches NL; HTTPS egress is
  `89.124.71.34`.
- Closing the connected GUI shows the confirmation dialog. Confirming Shutdown
  leaves SCM stopped and zero GUI/bridge/daemon processes for twenty samples.
  Native owned WFP/intent cleanup passes; route/DNS/adapter differences against
  the installation baseline are empty; physical-source HTTPS works afterward.
- Owned fixture services/tasks are removed, profile and binaries are archived
  recoverably, and IrvioLab administrator membership is restored before signout.

Harness issues were kept separate: an initial test edit needed an explicit
`SECURITY_INFORMATION` type; the first post-delete oracle incorrectly expected
an immediately recreated identity although an idle empty profile correctly has
none. The latter oracle was corrected before QR reimport. A PowerShell ANSI
metadata file mangled the Unicode path; the actual saved PNG and UTF-8 record
were correct and native import succeeded. No product workaround was added.

Fixed normal daemon SHA-256:
`3b12ab62b43301cc67ffb6ec8c3a1fe47ecd036c0261a715a631e36869f6747d`.
Evidence: `/home/ubuntu/Work/IRVIO_WINDOWS_LAB/evidence/w09-profile-gui/`,
allowlisted outputs without profile keys or QR payload. Workstation proxy and
networking are unchanged. This closes these particular GUI/profile gates, not
the remaining Private, power, DPI, installer or whole-port acceptance.

## W07 repeated private startup — diagnostic evidence, acceptance still open

The `W07-private-cycles-01` fixture uses one persisted approval on the mini-PC,
fresh Windows network participation each cycle, and simultaneous private
upstream + private hosting. It does not change selection or wire semantics.
The two measured attempts stopped after 11 and 17 completed cycles respectively;
neither is a 100-cycle PASS. Each completed measured cycle returned its own
routes, DNS and adapters to baseline and recorded zero remaining daemon TCP/UDP
sockets, but these short runs do not close the resource-growth gate.

The second run captured `WSAEACCES` while binding the concrete in-tunnel TCP
listener `10.6.17.1:51822`. Post-failure inspection found no listener or static
exclusion at that port; that inspection was too late to establish the cause.
An additional observer samples the TCP table every 25 ms and the OS port
exclusion table separately. Windows' dynamic exclusions change while adapters
are recreated. Their responsibility for the earlier bind failure remains a
hypothesis, not an established result. No `SO_REUSEADDR`, firewall bypass or
protocol-port workaround has been applied.

A separate repeated startup failure reports that Windows underlay grants are
owned or awaiting cleanup. In `port-repro-02`, iteration 6 retained Connect
intent, recovered automatically and resumed real user traffic. The earlier
observer incorrectly aborted on the expected API lifecycle-busy response during
recovery; its output is retained as a harness limitation. The acceptance harness
now observes recovery boundedly before teardown while still failing a rejected
CONNECT. The two failure signatures must not be conflated.

`port-repro-02` completed all 100 diagnostic cycles, including real 64 KiB HTTPS
downloads. Six initial CONNECT requests failed with the underlay-owner error;
all six recovered automatically. Observed CONNECT-to-CONNECTED times were
9,365–119,371 ms (median 9,918.5 ms, nearest-rank p95 84,341 ms).
The final state was DISCONNECTED with hosting/contribution off
and zero daemon-owned TCP/UDP sockets. No session-control bind failure occurred
in this repeat. This is recovery evidence, **not** a clean 100-cycle acceptance
or a complete resource/security proof; that diagnostic observer did not enforce
all of the acceptance harness's per-cycle invariants. Completed events, bounded
failure logs and TCP/exclusion observations are preserved in the allowlisted
`evidence/w07-private-cycles/port-repro-02` directory outside the repository.

Startup rollback now records the original cause before cleanup, and an uncertain
Windows underlay owner preserves its original error in subsequent busy errors.
The ownership fence and deny policy are unchanged. Native Windows checkptr=2,
shuffled ten-repeat underlay/startup regressions and relevant vet passed in the
second VM. The earlier common startup/logging regression also passed race/shuffle
on the mini-PC. The diagnostic binary is SHA-256
`f9292ac51ac807c6b922e7625416a6f4ebdc387f8748ffbadbf95d291881ee6c`;
it is not a release or a claimed fix for either startup failure.

On diagnostic02, `dirty-cause-01` subsequently passed 30 measured combined
Private cycles with exact per-cycle DNS/route/adapter restoration, zero idle
TCP/UDP sockets, 64 KiB HTTPS success and blocked physical-source HTTPS. Neither
startup failure reproduced. Median connection time was 10,402 ms, p95 79,723 ms,
maximum 84,735 ms; slow NO_EXIT/retry episodes remain an observation requiring
separate timing analysis. With no warmup, handles ranged 522–553 (first 526,
last 553), RSS changed 51,716,096 → 61,661,184 bytes and private bytes
66,502,656 → 75,169,792. This short result does not close the 100-cycle resource
gate or establish that a diagnostic-only change fixed the intermittent errors.

The second VM is `IRVIO-W11-PEER`, Windows 11 Pro with Secure Boot enabled and
Hyper-V disabled, using a separate QEMU subnet and a new IRVIO identity/profile.
Native pinned-driver probing passed 100 opens/closes (handles 356 → 360), and
zero-argument desktop daemon entered private HOSTING after bootstrap import.
This proves those narrow gates without Hyper-V, not Windows↔Windows forwarding.
The same VM subsequently completed 20 Global CONNECT/HTTPS/DISCONNECT cycles
with diagnostic02 and no startup rejection (`W07-peer-global-startup-01`).
This is a diagnostic control, not the full Global 100-cycle resource/leak gate.
The first manual fixture copy lacked protected file ACLs and was correctly
rejected; applying the required SYSTEM/Administrators payload ACLs fixed the
fixture without weakening production validation. Original and corrected outputs
remain in `C:\IRVIO-Lab\W07-peer-product` and native diagnostic outputs in
`C:\IRVIO-Lab\peer-startup-diagnostic-02` on that VM.

The opt-in `TestNativeSessionFixedPortLifecycle` isolates 100 adapter/listener
lifetimes without DHT, ICE or AWG and captures OS TCP exclusions before closing
the adapter on a bind failure. It requires an idle disposable Windows VM and
the signed Wintun payload. Its result must be reported separately from the
full private reconnect campaign. The first 300-second wrapper expired during
adapter creation before completing 100 lifetimes, without a recorded listener
bind failure. After checking that its adapter was gone, the same binary was
restarted with a 900-second harness bound and a separate evidence file. That
retry completed all 100 adapter/listener lifetimes with PASS and no remaining
`IRVIO-Session` adapter (`session-port-stress-02.log`, QGA wrapper exit 0).
This narrows the intermittent full-runtime bind failure; it does not prove its
cause or establish full-session lifecycle acceptance. Completed diagnostic/driver outputs are
also allowlisted under `IRVIO_WINDOWS_LAB/evidence/w07-private-cycles`; invitation
payloads and profile keys are excluded. Workstation proxy/networking remain
untouched.

## W07 Windows-to-Windows Private path and cold-boot fixture dependency

Two independent Windows identities used the existing bootstrap, invitation,
automatic enrollment, exact-peer selection, ICE/AWG and TunnelLease path. The
native client test now locates its imported target without requiring unrelated
saved connections to be deleted. Its native checkptr=2 executable compiled in
the secondary Windows VM; this change does not alter product behavior.

The initial separate-QEMU-NAT topology approved enrollment in 6.345 seconds but
did not establish a tunnel within the four-minute bound. The host observed zero
clients. Both processes disconnected cleanly afterward. With IRVIO stopped, a
separate bounded UDP oracle obtained validated STUN observations on both VMs,
then sent 298/297 nonce-tagged datagrams to the exchanged observed endpoints;
both received zero. This proves those tested UDP paths were unavailable even
without IRVIO; it does not prove every possible ICE path impossible or locate
which NAT/firewall layer dropped them. The original failed product gate remains
preserved, not relabeled as a passing Windows-to-Windows test.

The next topology joins both QEMU NICs through a Unix-stream-backed emulated
hub, with one user-mode Internet gateway. No workstation TAP, bridge, route,
firewall or proxy changes are used; see the [QEMU hubport contract](https://www.qemu.org/docs/master/system/qemu-manpage.html).
After clean shutdown/start, the guests received 10.0.2.15/10.0.2.16 and both had
ordinary no-proxy WAN with zero IRVIO processes before user intent.

The first LAN attempt was invalidated by a missing manual-fixture prerequisite:
WinDivert was demand-start and stopped after reboot. Production validation
correctly refused hosting rather than loading an unverified replacement. After
starting the same pinned signed driver, the saved hosting intent recovered.
The W08 fixture installer now sets `IRVIO`'s SCM dependency to `WinDivert`;
the boot recovery service remains independent. On **both VMs**, stopping daemon
and driver, then starting only `IRVIO`, automatically started the driver and
passed hosting → Disconnect → stopped-service checks. Native PowerShell parsing
of the modified fixture also passed. The eventual W11 installer must preserve
this dependency; this fixture change is not a completed production installer.

With the host confirmed ready, `W07-windows-client-lan-02` passed two real
Windows-to-Windows Private connections with the same approval, including native
DNS, no-proxy HTTPS egress and a 1 MiB download each time. First connection took
11.168 seconds; reconnect took **87.484 seconds** and remains a timing outlier,
not a claim of acceptable latency. The host independently observed one client
and 47,609 cumulative forwarded upload bytes, then zero clients after explicit
client Disconnect. Host Disconnect returned DNS, routes and adapter identities
exactly to its shared-LAN baseline. Both services were subsequently stopped by
the dependency checks. This closes this bounded Windows-to-Windows traffic and
saved-approval gate, not the larger NAT, crash, resource or performance matrix.

Evidence is allowlisted under `IRVIO_WINDOWS_LAB/evidence/w07-private-cycles`:
`W07-windows-client-01`, `nat-primary`, `nat-peer`, `W07-windows-client-lan-01`
(driver prerequisite failure), `W07-windows-client-lan-02`, the shared-LAN host
and per-VM driver dependency records. Invitation payloads and profile keys are
not exported. Intermittent underlay-owner/bind failures and long-tail connection
timing remain open; no production workaround was applied in this step.

## W07 connector diagnostics and W08 hibernate follow-up (2026-10-11)

Production brokered-client composition now supplies the existing daemon logger
to the connector. Previously its fallback logger bypassed the Local API log
buffer, obscuring failed signaling attempts. The Windows composition regression
checks the logger identity for owned, shared and Private configurations. This
does not change selection, deadlines, protocol messages or protection policy.
The logger-only binary was SHA-256
`b25bb2bccca193864ee542482042847844a0c81a487805e9f6be4fa5f8d25097`.
Native Windows composition checkptr=2/shuffle ×10 and runtime checkptr=2/shuffle
×3 passed, as did runtime vet. Mini-PC runtime vet, focused race/shuffle ×3
and full runtime race/shuffle ×1 passed; no workstation tests were run.

`W07-logger-client-01` completed six Windows-to-Windows Private connections
with native DNS, no-proxy HTTPS and 1 MiB downloads. Connection times were
11.059–13.073 seconds. Host-side clients returned to zero and explicit host
Disconnect restored its DNS/routes/adapter identities. This small sample does
not disprove the previously recorded 87.484-second reconnect outlier.

`W07-logger-combined-01` exercised Private upstream to the mini-PC together
with Private hosting. Its first 21 cycles passed traffic, blocked
physical-source HTTPS, exact idle DNS/routes/adapters and zero idle sockets.
Cycle 22 passed traffic and network restoration but retained one TCP entry
(UDP zero), so the 30-cycle campaign **failed**, not passed. The original
collector did not retain that entry's state/endpoints; a resource leak cannot
be established or dismissed from its count. The harness now records the
failing socket details and five follow-up samples without waiving the failure.
It also preserves the initial saved-target count rather than requiring a
profile containing exactly one saved connection.

Cycles 18/19 took 45.485/49.608 seconds with OFFER-declined and route-timeout
observations. The initiator's capacity-busy wording alone does not establish
capacity exhaustion: the responder maps multiple ProcessOffer failures to
that decline code. Handles were 520–541 and private bytes approximately
67–74 MiB, without warmup. This is not the required 100-cycle resource proof.

The primary Windows VM cold-booted following the workstation reboot. Two
subsequent single CONNECT probes (`W07-logger-sockets-01` and
`W07-signal-probe-01`) timed out at the 90-second harness bound, with all
three Private broker routes rejected before ICE. Diagnostic binary
`bfbc374d9228de091bfc99eb10cf9b1ad282917da656596bca9cca249b8d2caf`
distinguished a broker error response from local signal validation. The
mini-PC remained HOSTING with the Windows identity authorized. At that point
the cause was not established; these failed runs remain preserved.
Windows time service was stopped and an external time query measured about
1.5 seconds of lag. That initial observation alone did not establish causality.
Private signal diagnostics retain the original error classification and only
display two allowlisted broker explanations (`invalid signal`/`invalid target`),
never arbitrary remote text or signed payloads. Diagnostic regression shuffle
×10/checkptr=2, full Rendezvous checkptr=2 and vet passed natively on Windows;
Rendezvous vet and race/shuffle ×3 passed on the mini-PC (43.218 seconds).
The resulting diagnostic binary is SHA-256
`27478917b856952d2b1298a9087733d10dddab8fc73f309985d4d533e69b2cdc`.

The later controlled clock experiment isolated the rejection without changing
that binary, profile, mini-PC or distributors. Windows Time events 37/35 showed
valid time received at approximately 09:22:35Z. The subsequent
`W07-signal-reason-probe-01` passed a complete cycle: CONNECT 9,173 ms,
Disconnect 1,494 ms, real DNS/65,536-byte HTTPS, physical-source HTTPS blocked,
exact network restoration and zero idle TCP/UDP. QGA clock queries bracketed
by host timestamps showed millisecond-scale alignment.

With time synchronization paused in the VM, setting its clock two seconds
behind reproduced `private signal broker response (invalid signal)` on all
routes and a 90-second CONNECT timeout (`W07-clock-lag-control-01`). Restoring
the clock and time service on the same binary restored actual traffic:
CONNECT 19,192 ms, Disconnect 1,649 ms
(`W07-clock-restored-control-01`). Source inspection explains the sensitivity:
ExitSelector's initial `FastConnectTimeout` is 1,500 ms; BrokerCarrier copies
the caller deadline into absolute `DeadlineUnixMilli`; the distributor's
`validateSignal` rejects an expired deadline. No lifetime, validation or
selection rule was weakened. Accurate clock synchronization is a lab/product
precondition requiring attention after VM resume, not evidence of an ICE bug.
General protocol clock-skew robustness is not fixed by these diagnostics.

The restored-clock run stopped after its first successful connection because
the old socket oracle found one TCP entry. This time the enhanced capture proved
it was `FIN_WAIT_2`, 10.0.2.15:53539 → 194.87.52.164:4000. It remained attributed
to PID 3752 even at 09:30:28Z, when the IRVIO service had stopped and that process
no longer existed. This specific assertion was a harness false positive, not
an open daemon connection. The corrected oracle records FIN_WAIT_2 separately
(`tcp_fin_wait2` plus endpoint files), as it already did for TIME_WAIT; listeners,
established sockets and CLOSE_WAIT remain failures. The original failed evidence
is retained, and the older cycle-22 failure cannot retrospectively be assigned
the same state because it lacked the necessary capture. A new 100-measured-cycle
campaign with ten warmups was launched as `W07-private-100-clock-corrected-01`;
its launch is not a completed acceptance result.

The secondary VM resumed the real product hibernation prepared in
`W08-product-hibernate-01`. Kernel-Boot event 27 reported BootType 2; SCM and
daemon process creation times survived. After an initial NO_EXIT observation,
it automatically returned to Global NL CONNECTED without a new CONNECT
command, with native DNS and a 65,536-byte no-proxy HTTPS download passing.
The intended short hibernation was extended overnight by host interruption,
so no precise recovery-duration claim is made. Explicit Disconnect then
returned DNS/routes/adapter identities exactly to the pre-hibernate baseline,
cleared contribution/hosting, and the service was stopped. Continuous
hibernate leak capture, Fast Startup and the full power matrix remain open.

Allowlisted outputs are under
`IRVIO_WINDOWS_LAB/evidence/w07-private-cycles/logger-regression`; profile keys
and invitation payloads are excluded. Production distributors were not
restarted or modified for these diagnostic steps.

## W11 foundation — bounded, redacted Windows service journal

Windows SCM product processes now asynchronously consume the existing sanitized
Local API log stream into `ProgramData\IRVIO\logs`. Separate daemon/recovery
prefixes each use four JSONL slots capped at 1 MiB; rotation does not enumerate
or remove unrelated files. Each record includes the daemon incarnation and
existing sequence/timestamp. Slow consumers resubscribe to the bounded ring and
emit explicit gap records rather than blocking producers or growing a queue.
This is diagnostic logging, not an audit log guaranteeing every event survives
crash or storage failure. File opening/writes use the existing trusted native
directory/ACL/link checks and exclusive range locks. Live files are protected;
Local API remains the ordinary user's sanitized diagnostic view.

This does not install an Event Log source or a production installer. The
journal runs only inside SCM, leaves Linux journald behavior unchanged, and a
journal initialization/write failure does not disable networking or crash-state
adoption. Normal daemon exit drains the bounded tail before process exit.
Private invitation and bootstrap URI prefixes are now explicitly redacted by
the existing LogStore in both message and error fields, including mixed case.
No keys, invitation files or profile contents are copied to the journal.

The first native journal run caught that opening a trusted file alone did not
exclude another writer. The implementation now also takes the existing
LockFileEx lock per slot; that original failure is retained as
`W11-journal-01`. Corrected `W11-journal-02` passed shuffled checkptr=2 journal
tests ×10 (14.793s), covering bounds/rotation, partial tails, restart, sanitized
final drain, concurrent producers/Close, subscriber release, busy files,
oversized existing files and unsafe prefix rejection. Native Local API and
daemon checkptr=2/shuffle ×3 passed (19.664s/5.124s); affected vet passed.
Mini-PC Local API/daemon vet and race/shuffle ×3 passed (4.580s/14.267s).

`W11-journal-product-01` installed the verified binary only into the idle
secondary Windows SCM fixture. Demand startup returned DISCONNECTED, the
protected journal persisted PRODUCT_RUNTIME_READY, and explicit Stop returned
exact baseline DNS/routes/adapters. The four daemon slots stayed within limits.
The service ended stopped. This is an actual service-journal/IDLE gate, not
connected lifecycle, installer, Event Log or whole-port acceptance.
Binary SHA-256:
`8214edb4f4f2e594704be105c27fafe60dfb9f9c999731d7b2fdcdf9b17def25`.
Allowlisted evidence: `IRVIO_WINDOWS_LAB/evidence/w11-journal`.

## W07 100-cycle gate — cancelled underlay-grant race remains open

`W07-private-100-clock-corrected-01`, on the unchanged pre-journal diagnostic
binary, completed ten warmup cycles, then failed CONNECT on iteration 11.
Automatic recovery reached CONNECTED, but the harness correctly retained FAIL
and performed Disconnect; zero measured cycles completed. The startup error
was `Windows underlay grants are owned or awaiting cleanup: context canceled`
while building transport generation 1's mux. This is distinct from the proven
clock fault and the FIN_WAIT_2 measurement issue.

Targeted inspection identifies the race to test next: a cancelled WFP grant
update marks the shared underlay owner dirty, returns its partial lease, and
the caller closes that lease after the backend mutex has been released. A
concurrent datagram authorization can acquire the mutex before that cleanup
and receive ErrWindowsUnderlayBusy. Protection must remain fail-closed; the
next correction requires a deterministic cancellation/rollback regression,
not removing the dirty-owner check or declaring recovered CONNECT a PASS.

## W07 cancelled WFP grant rollback correction

The deterministic native regression reproduces the startup ownership window
on the pre-fix implementation in four cases: cancellation before commit, an
error after commit, cancellation during post-commit inspection, and inspection
of a duplicate grant. Each returned a dirty shared owner before caller-owned
cleanup could run. The next UDP authorization could therefore fail even though
its own request had not been cancelled. Original failing output is retained in
`W07-cancel-grant-before-01`.

On a failed acquisition whose caller context has ended, WindowsUnderlayOwner
now rolls back only that acquisition before releasing the backend mutex. It
uses the same bounded three-second independent cleanup context as lease Close,
reinstalls and verifies the remaining owned policy, then forgets the cancelled
lease only after success. The cancelled call still returns its original error.
Other peers' grants and the protected baseline remain installed. A failed
rollback still returns the retained lease and error; dirty-owner fencing and
explicit cleanup retry remain mandatory. This introduces no sleep/retry in
selection, no wider bypass, and no wire/ICE/AWG change.

Post-fix native checkptr=2/shuffle regression ×50 passed (5.489s), including
failure to roll back and later retained-owner cleanup. Full killswitch,
controlsocket and runtime checkptr=2/shuffle ×5 passed (5.951s, 1.401s, 17.934s);
affected vet passed. This Windows-only change has no Linux implementation diff.
Binary SHA-256:
`05e677f1aca664743d21aa93356ad828bf6f710774518c2f9bfcf30fe1230d38`.
Evidence: `IRVIO_WINDOWS_LAB/evidence/w07-cancel-grant`.

The primary VM received that binary with unchanged saved identities/bootstrap.
Its clock was within milliseconds of the host at launch. The real
`W07-private-100-cancel-fixed-01` campaign is a new ten-warmup/100-measured
CLIENT+private-hosting test, not a relabeling of the previous failure. Its
completion and resource distribution must still be evaluated before acceptance.

## W12 native race toolchain preparation

The Windows VM previously had CGO disabled and no C compiler. Native race
coverage is therefore not implied by earlier checkptr tests. Per the
[Go race detector requirements](https://go.dev/doc/articles/race_detector),
Windows/amd64 needs CGO and a compatible MinGW runtime. The isolated lab is
being provisioned with the official
[LLVM-MinGW 20261006 release](https://github.com/mstorsjo/llvm-mingw/releases/tag/20261006),
Windows x86_64 UCRT archive, SHA-256
`317492c456aa27ee607a5919f1d2d38dcdc1112516a24d0bf4b00d078f52d17a`.
The hash is verified before extraction, and `libsynchronization.a` is checked
before any race build. Compiler/CGO/PATH overrides are confined to that VM's
test process; this is not a production dependency upgrade. Native race results
are pending and will be recorded separately from compiler installation.

## W12 native race results and whole-tree fixture correction

`W12-native-race-01` verified the pinned LLVM-MinGW archive and synchronization
library, then completed native Windows/amd64 race tests with CGO enabled.
The cancelled-grant regression passed shuffle ×10 (11.490s). Affected race
shuffle ×3 passed: killswitch 7.739s, controlsocket 2.501s, runtime 13.527s,
platformjournal 3.976s, localapi 20.793s and cmd/irviod 4.753s. The wrapper
returned exit 0. These are real race-detector results, not cross-build or
checkptr substitutes. Allowlisted logs and toolchain metadata are retained at
`IRVIO_WINDOWS_LAB/evidence/w12-native-race`.

The first whole-tree native run, `W12-native-full-01`, correctly reports FAIL
for vet and normal tests. Three Linux-only lab commands had no build constraint:
irvio-test-internet (SO_MARK/raw Unix sockets), irvio-test-killswitch (nftables)
and irvio-test-icesignal (Linux TUN/netlink). Their complete command packages
now have explicit linux build tags; no production package or portable test
is excluded. Mini-PC affected vet and race/shuffle ×5 pass; two command
packages have no test functions and are compile/vet checks only.

The portable M0/M2 discovery integration fixture also passed t.TempDir directly
to identity creation. That directory already exists with inherited Windows
temporary-directory ACLs, so production correctly refuses it. The fixture now
passes a new profile child and lets the existing secure creator establish its
ACLs, as the identity unit tests already do. No production ACL is relaxed.
Native Windows race/shuffle ×10 passes (7.346s); the same Linux mini-PC gate
passes (4.242s). Full native vet passes on the corrected source copy.
Evidence: `W12-fixture-fix-01`, copied to
`IRVIO_WINDOWS_LAB/evidence/w12-fixture-fix`.

Two further integration failures remain distinct: M5C3 and encrypted M6
generation migration request 127.0.0.2 and 127.0.0.3 without assigning those
addresses to a Windows interface. Native source-ownership validation refuses
them. The original source and environment are retained for the currently
running full shuffle/race gates; they are not patched underneath a live run.
A separate disposable-VM loopback-fixture check is queued after that process
exits. It must preserve the original source-address migration assertions and
restore its address/route baseline. Until that evidence is evaluated, neither
the integration suite nor the whole-tree matrix is PASS.

## W12 STUN diagnostic test synchronization

The first full shuffled Windows run additionally caught one failure of
TestClientDualStackBindingUsesMuxPort: both probes succeeded and transmitted
exactly once, while ValidResponsesDispatched was still one. handleResponse
delivers the response before incrementing that independent atomic diagnostic
counter, so a completed Probe does not join the dispatcher. This was a final
counter assertion racing asynchronous accounting, not failed STUN exchange.
The test now closes/joins its dispatcher after verifying both results and both
observed source ports, before asserting the unchanged exact final counters.
No production STUN logic, network timings or assertions were relaxed.

Native Windows focused race/shuffle ×500 passes (7.346s), followed by the full
STUN suite race/shuffle ×20 (4.657s), in `W12-stun-stats-01`. Linux mini-PC
equivalents pass (1.913s and 4.655s). The original whole-tree source stays
unchanged during its remaining stages; a corrected whole-tree rerun is still
required. The lifecycle evidence analyzer also passes five mini-PC test cases
(including invalid-evidence subcases); it explicitly does not infer leak-free
behavior from a green CSV verdict alone.

## W07 completed Private CLIENT + HOSTING lifecycle gate

`W07-private-100-cancel-fixed-01` completed ten warmup plus 100 measured cycles;
the original runner exited 0. Each cycle connected to the same saved Linux
Private host with concurrent Windows private-hosting intent, transferred 65,536
HTTPS bytes, blocked the forced physical-source HTTPS probe, preserved saved
targets, and restored baseline DNS/routes/adapters on explicit Disconnect.
The daemon did not restart between cycles; every measured idle row has zero
active TCP and UDP sockets. This is not a five-downstream load test or an
exhaustive packet-capture leak proof.

| Measured metric | Median | p95 | Maximum |
| --- | ---: | ---: | ---: |
| Connect | 9.695 s | 18.642 s | 19.092 s |
| Disconnect | 2.3295 s | 2.431 s | 2.515 s |
| Handles | 559.5 | 565 | 568 |
| Threads | 22 | 23 | 24 |
| Working set | 62,038,016 B | 64,040,960 B | 65,449,984 B |
| Private bytes | 75,622,400 B | 77,328,384 B | 78,954,496 B |

First-versus-last-20 measured medians: handles 549 → 562; threads 22 → 22;
working set 59,136,000 → 62,908,416 B; private bytes 73,424,896 → 76,169,216 B.
Handle growth is stepped, not one allocation per cycle: after overall cycle
61 it stays within 562–568 through cycle 110, with final 565. This shows a
bounded observed plateau, not proof of arbitrary-duration leak freedom or a
known attribution of every retained handle. Working set also fluctuates rather
than increasing monotonically. FIN_WAIT_2 remains separately visible, maximum
two, final zero. No analogous per-generation socket accumulation was observed.

The separate terminal `final-check` exited 0: native idle cleanup verified no
ConnectIntent and no owned WFP protection; the service stopped with exit 0;
final DNS/routes/adapters exactly matched baseline; ordinary no-proxy WAN
downloaded another 65,536 bytes. The installed binary remained SHA-256
`05e677f1aca664743d21aa93356ad828bf6f710774518c2f9bfcf30fe1230d38`.
Allowlisted evidence: `IRVIO_WINDOWS_LAB/evidence/w07-private-100-cancel-fixed`.
The earlier failed campaigns remain retained and are not relabeled PASS.

## W12 source-address migration fixture and corrected full rerun

`W12-integration-fixture-01` assigned only 127.0.0.2/32 and 127.0.0.3/32 to the
existing Windows loopback interface. The complete integration package passed
native race/shuffle ×10 (13.690s), including M5C3 address migration and M6
encrypted generation migration, without replacing addresses, weakening source
ownership, changing protocol timeouts or skipping those tests. Cleanup removed
the two fixture addresses and exactly restored the prior loopback address/route
state. The explicitly gated long soak test remained skipped as intended.

All five original `W12-native-full-01` stages finished with exit 1; the failures
are preserved (Linux-only lab compilation, unsafe identity fixture, unassigned
loopback sources, and one STUN diagnostic-counter assertion race). No race
detector warning or panic was found in those logs, but the matrix is FAIL, not
PASS. The corrected full matrix is running separately as `W12-native-full-02`,
using a clean git archive of `fbc6271`, archive SHA-256
`19355f133421c14580e6cec5e7a9e015adb51f9fe42a459d412260e623867db2`.
The native fixture runner records each stage and restores its temporary aliases.

The corrected full run has now passed native whole-tree vet (15.450s) and
normal tests (139.342s); shuffled/race stages and final alias cleanup are still
running. Do not promote those two passing stages to a complete matrix result.

## W12 separate Global CLIENT + EXIT cycle campaign

The existing cycle harness now accepts either an exact saved Private target or
an explicit Global country, never both. Global mode checks the selected country
and contribution intent while forbidding Private session/hosting state. Both
modes retain all original byte-count, physical-source HTTPS, final traffic,
idle socket, saved-target and route/DNS/adapter assertions. No introducers or
Exit endpoints are supplied by the harness.

Two Global/NL smoke cycles passed as `W12-global-cycles-smoke-01`. A subsequent
one-cycle Private smoke on the updated harness also passed (9.521s Connect,
1.524s Disconnect), verifying the default Private path was retained. The next
autonomous campaign, `W12-global-100-01`, has started ten warmup plus 100 measured
Global cycles on the same frozen binary; its result is pending. This exercises
public contribution intent, not independently observed downstream forwarding
on every cycle, which remains covered by separate mixed-client gates.

## W12 corrected native whole-tree matrix completed

`W12-native-full-02` completed with runner exit 0 on Windows 11 Pro x64,
Go 1.27.1 and the pinned LLVM-MinGW native race toolchain. The immutable source
archive remains `fbc6271` as recorded above; the subsequent harness/documentation
commit did not alter production Go sources.

| Stage | Exit | Duration |
| --- | ---: | ---: |
| `go vet ./...` | 0 | 15.450 s |
| `go test -timeout=300s ./...` | 0 | 139.342 s |
| `go test -shuffle=on -count=10 -timeout=600s ./...` | 0 | 369.038 s |
| `go test -race -timeout=600s ./...` | 0 | 220.444 s |
| `go test -race -shuffle=on -count=5 -timeout=900s ./...` | 0 | 293.428 s |
| Normal native daemon build | 0 | 3.134 s |
| Trimpath native daemon build | 0 | 42.060 s |

Final `result.json` records test exit 0 and an empty loopback cleanup delta.
All stage logs, input hashes, baseline/final state and build hashes are archived
under `IRVIO_WINDOWS_LAB/evidence/w12-native-full-02`. No race detector warning,
panic or failed test appears in these logs. Privileged tests that require their
explicit fixture environment remain gated: this is a whole-tree Go matrix,
not proof of all W00–W12 acceptance or a Windows release verdict.

Normal SHA-256: `055453f7cf8d68180618b829a0f1be78dba0b40683eb0757106fb7860d9a14e7`.
Trimpath SHA-256: `ed42902aec4c6e30db4489cc1a9a8847aae9d053cfc5d1efb0cebf6115ac496a`.

## W11 payload staging foundation

`packaging/windows/stage-payload.ps1` stages explicit daemon/UI/driver/GeoIP/
license inputs, pins the Wintun/WinDivert payload hashes, requires their valid
Authenticode signatures, checks AMD64 PE headers, rejects reparse-point paths
and extraneous UI artifacts, and writes a complete size/SHA-256 manifest.
Unsigned IRVIO EXEs require an explicit development-only switch recorded in
that manifest; normal staging refuses them. The source revision is deliberately
named `declared_source_revision`: a supplied commit string is not build proof.

`W11-payload-02` passed 13 native packaging fixture checks: invalid revision and
version, existing output, unexpected EXE, PDB residue, private-key filename,
missing notice, wrong pinned Wintun, x86 PE, root/nested junctions, unsigned
release rejection, and successful development manifest coverage (18 files).
Its results, script/input hashes and manifest are archived under
`IRVIO_WINDOWS_LAB/evidence/w11-payload-02`. The fixture intentionally uses
non-runnable Flutter/GeoIP/license stand-ins, not a distributable payload.
Real Flutter bundle staging, provenance/license collection, the EXE installer,
service registration, upgrade/rollback, uninstall and signing remain pending.

## W11 real native UI payload and fresh desktop service installation

`W11-ui-build-02` passed locked dependency resolution, native Flutter analyze
(21.093 s), all 160 tests (39.830 s), and Windows release build (61.998 s).
The initial offline build attempt only lacked a populated Dart package cache;
the second used `pub get --enforce-lockfile`, without dependency upgrades.
UI SHA-256: `6ff6212563dd3337bf41701dd105b0ef1e4b2210cd0c3798b015c73e10447d26`.
Bridge SHA-256: `b892fc649935d402c1d3005412fd1615971a4ba7fc46579f33abd8ee78a2571d`.

`W11-real-payload-02` staged 502 actual files from those release outputs, the
previous native trimpath daemon, pinned signed drivers, actual DB-IP MMDB and
collected dependency notices. MMDB lookup passed through the existing
CountryResolver. Manifest SHA-256:
`d0bb3e66fd029c4b799aa3b72a3bbf83f354059df3b1b80f1d1c0aedc80ee062`.
This is explicitly development-only: product EXEs are unsigned and MSVC remains
a VM prerequisite, not yet an included redistribution. Source is the frozen
`fbc6271` archive; the later installer-test overlay does not change those built
product binaries. The first module-inventory attempt needed uncached unrelated
modules; collection was narrowed to modules actually linked by daemon/bridge.

The second VM's stopped W08 installation was independently checked clean and
archived privately, including identity/profile/driver files. Only its verified
SCM registrations were removed. `W11-desktop-install-01` found a real defect in
the new installer: inherited-only desktop authorization file ACLs were rejected
by Core's immutable-file validator. No Core validation was weakened. The
installer now applies explicit protected ACLs to private files as well as atomic
protected directory creation. The failed install and logs remain archived.

Fresh corrected `W11-desktop-install-02` succeeds. The installed, genuine bridge
then passes native **race-enabled** ordinary-user activation/IDLE/EMPTY/Shutdown
five times in `W11-desktop-activation-03` (3.26 s then 0.22–0.23 s). The standard
user cannot acquire STOP, CHANGE_CONFIG or WRITE_DAC rights or write the daemon.
An initial test attempt aborted on stderr while emitting a race diagnostic;
PowerShell's stop-on-native-stderr behavior prevented retaining its full stack.
The failure path read its stderr buffer before joining the writer; this unsafe
test-only access was corrected before the passing repetition.

`W11-desktop-install-final-02` confirms boot-recovery invocation exits 0, no
ConnectIntent/WFP protection remains, routes/DNS/adapters equal the original
preinstallation baseline, and ordinary WAN downloads 65,536 bytes. The first
comparison incorrectly treated PowerShell's serialized `{value,Count}` array
wrapper as one network row; the original baseline was retained, its wrapper
validated/unpacked, and the corrected comparison passed. Affected Local API vet
and native race/shuffle ×5 passed (33.264 s); unrelated privileged fixtures stayed
gated. `W11-payload-reader-01` additionally passes 12 manifest/path/hash rejection
and coverage checks on a private copy of the real payload.

Allowlisted outputs: `IRVIO_WINDOWS_LAB/evidence/w11-ui-build-02`,
`w11-real-payload-02`, `w11-desktop-install`, `w11-payload-reader`. The fresh
installation was executed by the SYSTEM lab harness; an actual UAC elevation,
single EXE installer, standard-user GUI session, upgrade/rollback, uninstall,
server installation and release signing remain pending. The authorized account
on the second VM is the disposable non-admin `IRVIOInstallTest`; the original
IrvioLab profile remains in the private backup, not silently migrated.

## W12 Global cycle 81 rejection — acceptance remains FAIL

`W12-global-100-01` ended with runner exit 1: 80 cycles completed (10 warmup,
70 measured), then cycle 81's explicit CONNECT was rejected. At
2026-10-11 10:44:15.352 UTC the bounded log records `PRODUCT_DESKTOP_START_FAILED`:
Winsock denied binding the inner session-control listener `10.6.17.1:51822`
with WSAEACCES. Existing recovery reached CONNECTED 17.096 s after the original
request, without another CONNECT. This does not convert a rejected command into
a clean 100-cycle PASS. All 80 completed rows retain their successful 64 KiB
traffic/physical-source denial and per-cycle cleanup checks; cycle 81 is not a
completed traffic/resource row.

This reproduces the earlier, still unresolved **bind** signature, not the
cancelled-underlay-grant defect. Post-failure exclusions cover 49675–49974 and
50000–50059, not 51822; the dynamic TCP range is 49152–65535. Those late snapshots
cannot prove the transient cause. A moment-of-failure TCP/WFP/exclusion capture
is still required; no socket sharing, firewall weakening or port workaround was
introduced. Resource acceptance for the complete Global100 run remains open.

Separate `final-failed-run-cleanup` passes native WFP/intent absence, exact
baseline DNS/routes/adapters and ordinary-WAN 65,536-byte download. Frozen
production binary is unchanged. The failed run and bounded logs are preserved
under `IRVIO_WINDOWS_LAB/evidence/w12-global-100-01`; cleanup PASS is explicitly
separate from campaign FAIL.

## Remaining acceptance

W00 is partially evidenced; the Windows Exit backend decision is not finalized.
W01/W02/W03/W04/W05/W06 have partial implementations; none is complete. W07–W12 remain
incomplete. In particular, Global CLIENT+EXIT,
Private VPN parity, Windows ProtectedMode, SCM lifecycle, headless server,
installer, signing, and the complete privileged/mixed-network matrix are not
yet PASS. The native whole-tree Go/race matrix above is complete, but does not
substitute for those product acceptance gates.
## W11 app-local MSVC payload requirements — 2026-10-11

Native PE import inspection of the real `W11-real-payload-02` UI found
`MSVCP140.dll`, `VCRUNTIME140.dll` and `VCRUNTIME140_1.dll` dependencies in
the runner and Flutter plugins. That earlier payload lacked these files and
relied on the build VM's installed runtime; it is not a clean-machine package.
Staging now requires these three AMD64 DLLs with valid Microsoft signatures,
plus `MSVC-LICENSE.txt`. The installer manifest reader independently rejects
any of those four missing components. This changes packaging only, not Core.

Native secondary-VM evidence:

- `C:\IRVIO-Lab\W11-msvc-payload-check-02`: 16 staging fixture checks PASS,
  including missing DLL, unsigned substituted DLL and missing notice rejection.
- `C:\IRVIO-Lab\W11-msvc-reader-check-02`: 16 manifest reader checks PASS,
  including separate rejection of each runtime DLL and its notice.
- Input runtime: Visual Studio `14.44.35112/x64/Microsoft.VC143.CRT`, file
  version `14.44.35211.0`; all three verify with Windows Authenticode.
- The first `W11-msvc-payload-check-01` failed because the publisher allowlist
  only allowed `Microsoft Corporation`. The actual valid certificate is
  `Microsoft Windows Software Compatibility Publisher`; both legitimate names
  are now accepted after successful Authenticode validation. Failure preserved.

Allowlisted results/hashes are archived under
`IRVIO_WINDOWS_LAB/evidence/w11-msvc-{payload,reader}-check-02`.
Fixtures contain synthetic UI/license/MMDB content and are **not releases**.
Clean-host GUI launch without the system runtime, actual redistribution notice
review, one-EXE installer, upgrade/uninstall and signing remain unfinished gates.

## W12 bind-failure observer — 2026-10-11

Added `tools/windows/w07/observe-session-port.ps1` and its native smoke test.
The checked-in version passed on the secondary VM in
`C:\IRVIO-Lab\W12-port-observer-smoke-02`: the TCP owner sampler observed a
controlled exclusive loopback listener on port 51822 and its subsequent removal,
then stopped and joined cleanly. No failure-audit setting or firewall rule was
changed by that smoke test. This verifies sampling, not WFP-denial event coverage.
The allowlisted result and transition log are archived under
`IRVIO_WINDOWS_LAB/evidence/w12-port-observer-smoke-02`.

A separate primary-VM diagnostic campaign `W12-global-bind-audit-01` uses the
unchanged `05e677...` daemon, up to 100 Global cycles, and temporary failure-only
Filtering Platform Connection auditing. The wrapper saves/restores that exact
subcategory setting and performs independent WFP/intent/network cleanup.
At this entry the campaign is still running; no PASS or root-cause conclusion
is claimed. The earlier cycle-81 bind failure remains unresolved. TCP snapshots
can miss IPv6/bound-only/short-lived conflicts, and two-second exclusion samples
are not a synchronous bind oracle. Preserve any failed run and its raw evidence.

## W11 installed ownership inventory — 2026-10-11

`packaging/windows/installation.ps1` adds a read-only prerequisite for future
upgrade/uninstall: protected root/metadata ownership and ACLs, manifest digest,
file hashes/sizes and parent ACLs, no reparse points, consistent desktop SID,
and exact service executable/account/type plus kernel driver ownership.

Secondary native VM `C:\IRVIO-Lab\W11-installation-inventory-01` passed **19**
checks. Negative fixtures rejected modified files/manifest, four unsafe paths,
duplicates, missing daemon, changed authorization, a world-writable file,
a directory junction, foreign daemon command/account, wrong recovery command,
and foreign driver path/start mode. The final read-only check validated the
real installed desktop's **502** mapped files and three SCM entries against
manifest `d0bb3e66fd029c4b799aa3b72a3bbf83f354059df3b1b80f1d1c0aedc80ee062`.
Results/hashes are archived at `IRVIO_WINDOWS_LAB/evidence/w11-installation-inventory-01`.

No installed file, service, profile or network state was changed by this gate;
fixture mutations remained within its new evidence directory. This is not
uninstall/upgrade acceptance: daemon-owned shutdown, WFP/network residue checks,
profile preservation/deletion choice, interrupted-operation recovery and the
single-EXE frontend remain required work. Earlier installed payloads can be
inventoried without satisfying the newer candidate-package MSVC requirement.

## W12 Global diagnostic campaign completed — 2026-10-11

`W12-global-bind-audit-01` completed **100/100** Global CLIENT+EXIT-intent cycles
on the unchanged `05e677...` daemon, with no excluded warmup cycles. Each row
has 65,536 HTTPS bytes, a denied physical-source HTTPS probe, idle active TCP/UDP
counts zero, and exact route/DNS/adapter restoration. Final independent cleanup
passed WFP/intent absence, unchanged host network state and ordinary WAN traffic.
Filtering Platform Connection failure auditing was restored from temporary
enabled state to its recorded `No Auditing` baseline. The terminal QGA exit was 0.

| Metric | Median | p95 | Max |
| --- | ---: | ---: | ---: |
| Connect | 9.974 s | 25.533 s | 32.696 s |
| Disconnect | 1.900 s | 2.076 s | 2.363 s |
| Handles | 552 | 563 | 563 |
| Threads | 22 | 23 | 23 |
| Working set | 63,424,512 B | 65,810,432 B | 67,559,424 B |
| Private bytes | 76,462,080 B | 78,671,872 B | 79,626,240 B |

First/last 20 handle medians were 543/563; last 20 samples were 560–563, not
per-cycle accumulation. Working-set medians were 59,426,816/64,849,920 bytes.
This supports bounded resources over this run, not unbounded-duration leak
freedom. `FIN_WAIT_2` had median 1/max 2, reported separately under the previously
documented Windows TCP oracle; active TCP and UDP remained zero after teardown.

The observer ran 1,625,915 ms, reported no TCP sampler error and zero matching
5155/5159 events. **The earlier `51822` bind refusal did not recur and remains
unexplained/unfixed.** This successful diagnostic run does not erase the prior
cycle-81 failure, prove missing WFP audit events impossible, or establish full
Global/Windows acceptance. Security coverage is the targeted physical-source
probe, not exhaustive packet capture. Allowlisted evidence is archived in
`IRVIO_WINDOWS_LAB/evidence/w12-global-bind-audit-01`.

## W11 standalone development EXE — 2026-10-11

Added `cmd/irvio-setup`, `build-setup.ps1` and native PE manifest verification.
The frontend captures the initial account SID before its explicit UAC request,
validates the embedded bounded archive, extracts into private Windows state,
and invokes the existing strict backend without installation-adjacent inputs.
Release mode is deliberately unavailable until signing/provenance is configured.

Native secondary-VM gates:

- `W11-setup-native-02`: `go vet` plus race/shuffle tests repeated five times PASS;
  archive paths/kinds/duplicates/digests, private extraction and target-SID
  handling are covered. `-01` compile failure used an absent `windows.IDYES`
  constant; corrected to the documented MessageBox result and preserved.
- `W11-real-setup-01`: restaged real 507-file payload includes three signed
  MSVC DLLs and the original Microsoft runtime license DOCX plus text rendering.
  Production daemon/UI source remains the previously validated `fbc6271` build;
  the installer frontend is new work, not falsely attributed to that commit.
- `W11-real-setup-02`: final AMD64 EXE includes an inspected
  `asInvoker/uiAccess=false` manifest. SHA-256:
  `7ff713daf651346331b47ca204bc7567e576b8b8f2ef275bb10188d9940daa0d`.
  Embedded payload manifest:
  `125230cab9291a5a683cfec01cfa8f5a7c8000fb3e4b43533d6feba4b208c586`.
- `W11-setup-install-01`: standalone EXE first refused the existing installation
  without changing its verified inventory. The disposable old fixture was then
  proven idle/clean and archived privately, not erased. The EXE installed all
  507 files from its own embedded payload, with correct desktop authorization
  and daemon initially stopped. Five race-enabled genuine standard-account
  bridge activation/IDLE/EMPTY/Shutdown checks passed. Native WFP/intent absence
  and exact final DNS/routes/adapters equality passed.

Installation was invoked by the **SYSTEM development-unattended harness**;
interactive UAC, alternate-admin credentials and GUI launch without preinstalled
MSVC were not exercised. Old profile/program/driver remain in the VM's private
`W11-setup-install-01/previous-*` backup; they are excluded from evidence exports.
The VM now has the new 507-file desktop payload, still authorized for the local
`IRVIOInstallTest` account. This is fresh desktop installation evidence, not
upgrade/uninstall, server deployment or signed installer acceptance.

## W11 interactive UAC / alternate administrator — 2026-10-11

`W11-interactive-uac-01` exercised the actual Windows secure-desktop credential
prompt on the secondary Pro VM, using the unchanged `7ff713da...` standalone
development installer. The initiating process belonged to standard account
`IRVIOInstallTest`; the elevated child belonged to administrator `IrvioLab`.
The observed child command retained the initiating account's SID (`...-1002`),
not the administrator's SID (`...-1000`). UAC policy was not disabled or bypassed.

- Existing installation: backend refused replacement; all 507 installed files,
  manifest digest and desktop authorization passed the ownership/hash inventory.
- Fresh installation: the verified idle fixture was archived in a private
  `previous-installation` directory, including its profile, rather than erased.
  A second interactive UAC approval installed all 507 files and displayed the
  success dialog. The daemon remained stopped; no VPN connection was started.
- Post-install: five race-enabled genuine standard-account bridge activation,
  IDLE/EMPTY and Shutdown checks passed; installed authorization remained the
  original standard user. Native WFP/intent absence and exact DNS/routes/adapter
  baseline equality passed. Final verification guest process exited 0.
- Temporary launcher task and its private credential script were removed; the
  disposable account password was randomized again. Backups remain guest-only.

The lab scheduled-task wrapper reported exit 1 after spawning the alternate-user
process; that wrapper result is **not** an installer exit result. Actual process
ownership/arguments, secure-desktop screenshots, backend refusal, installed
inventory and independent native post-install checks establish the outcomes.
Allowlisted evidence is in `IRVIO_WINDOWS_LAB/evidence/w11-interactive-uac-01`.

This closes the exercised alternate-admin UAC and fresh-install authorization
path, not clean-host GUI/runtime, upgrade/repair/uninstall, interrupted-operation
recovery, signed release, or full Windows acceptance. The earlier intermittent
TCP/51822 bind failure remains unresolved.

## W11 bounded maintenance stop prerequisite — 2026-10-11

Added `packaging/windows/maintenance.ps1`: installed ownership validation,
exclusive maintenance lock, flushed private start-mode journal before SCM
changes, activation fencing, graceful SCM stop and restoration of recorded
start modes without reconnecting. Failures retain the journal and do not kill
the daemon, remove protection, or erase profiles/resources. These primitives
are not yet wired into the single-EXE frontend.

Native `W11-maintenance-stop-02` on the secondary Windows installation completed
with guest exit 0 and **15 checks PASS**: idle/running-service stopping, duplicate
and concurrent-operation rejection, malformed/foreign journal rejection,
durable mode restoration, no restart on restoration and unchanged payload.
An injected failure on the second SCM configuration occurred after IRVIO was
already disabled; the pre-operation demand-start mode survived in the journal
and restoration returned the service to Manual. The native independent
`TestWindowsNativeProductIdleCleanup` also passed WFP/intent absence. The earlier
12-check `-01` passed before adding this partial-failure case.

Evidence and exact helper/test inputs are archived under
`IRVIO_WINDOWS_LAB/evidence/w11-maintenance-stop-02`. This gate started the real
installed daemon in **IDLE**, not a live VPN session. It does not prove active
downstream draining, full network-state restoration, power-loss recovery,
replacement rollback, repair or uninstall. A stopped SCM service is not a
substitute for a complete native cleanup oracle; deleting runtime journals or
profile state is still not authorized by this helper's success alone.

## W11 read-only network-journal inspection — 2026-10-11

Added `winfile.InspectLockedDirectory`, inactive-journal checks in `windns`,
`winroute` and `winfirewall`, and runtime composition over the exact existing
daemon/dataplane/upstream/socket layout. Inspection opens only existing state,
uses the existing exclusive ownership locks and journal validators, and rejects
active or corrupt records without invoking recovery or rewriting evidence.
Absent state stays absent; an existing directory without its lock is rejected.
The installed-product idle cleanup test now invokes this additional check.

`W11-maintenance-inspect-01` on the secondary Windows VM passed native vet for
the five affected internal packages plus `cmd/irviod`, race/shuffle repeated five
times for those internal packages, and the enhanced native stopped-installation
cleanup test. Tests cover live-owner refusal, active/corrupt state preservation,
all composed scope types, first/last socket slots, absent paths and cancellation.
This used the current Windows source overlay; the installed daemon remained the
previously documented `fbc6271` payload, not a rebuilt production deployment.
An additional `W11-inspect-lock-01` native race/shuffle ×10 passed after adding
explicit missing-lock, invalid-name, nil-callback and failure-release tests.

The inspector proves **recorded resource ownership is inactive**, not an
independent enumeration of every live adapter/firewall object. Combining it with
the product WFP/intent gate, native adapter/firewall absence checks and installer
fencing remains necessary before production file removal. No maintenance CLI or
uninstall authorization is enabled by this change alone.

## W11 fenced product maintenance probe — 2026-10-11

Added internal `irviod.exe --maintenance-check` and installer composition
`Assert-IRVIOMaintenanceClean`. The probe requires elevated access, canonical
LocalSystem service commands, stopped/disabled SCM state, no product intent,
inactive recorded network ownership, absent native product WFP roots and no
product tunnel interface. It never invokes recovery. The installer pins a
digest-verified probe, applies a bounded child-process timeout and independently
checks hidden adapters and IRVIO firewall groups in the active policy store.
The trusted probe may be from the candidate payload, supporting older installed
daemons; a caller-provided hash alone is not publisher authentication.

`W11-maintenance-cli-01`: native `cmd/irviod` vet, race/shuffle ×5 and trimpath
build passed, followed by six real installed-state checks. Probe SHA-256:
`7dd46ae7c7ad3299576a72f4c1dcf3bc58226d2aa165f6438835d338059391c3`.
`W11-maintenance-native-02` expanded the gate to **8 PASS**: unfenced refusal,
wrong digest refusal, clean fenced runtime, intent residue refusal/preservation,
orphan firewall refusal/preservation and clean state after fixture removal.
The injected firewall rule blocked only guest loopback TCP/65534; only the
test harness removed that exact fixture. The malformed intent fixture was also
preserved by inspection and removed by the harness after verifying its digest.
Original service start modes were restored; the installed daemon stayed stopped.

Linux compatibility was checked on the mini-PC in isolated
`/home/ubuntu/irvio-maintenance-linux.uPHWSB`: vet, race/shuffle ×5 and trimpath
build PASS; the command refused with its Windows-only diagnostic. Binary SHA:
`bded2e1fbb8e927132c15869f6fb2e549fbf353dd1598c2cf90ec402608ccab4`.
Initial missing-module-cache failure and final `rg`-missing exit 127 were harness
issues. The existing module cache was selected; after successful vet/test/build,
only the output assertion was repeated using available `grep`, exit 0.

Evidence is archived in `IRVIO_WINDOWS_LAB/evidence/w11-maintenance-cli-01`,
`w11-maintenance-native-02` and `w11-maintenance-linux-01`. No installed payload
was replaced or removed. Remaining gates include active-session/downstream
maintenance, live orphan adapter/protection rejection, transactional replacement,
GUI file holders, interruption recovery, profile-aware uninstall and frontend
integration. A returned clean report is not a lock held across a later mutation;
the final transaction must retain fencing and maintenance serialization.

## W11 software replacement and synchronous rollback — 2026-10-11

Added `packaging/windows/update-desktop.ps1`, using the verified installed
inventory, candidate manifest and fenced native cleanup gate. Program and GeoIP
trees are staged on the same volume and swapped while retaining the maintenance
lock. Installation metadata follows the replacement, with rollback of each
completed step on a caught failure. Profiles, memberships, identities, desktop
authorization and the unchanged WinDivert installation are not replaced.
Start modes are restored without starting a daemon or VPN; transaction backups
remain for recovery/finalization work. Reparse paths, unknown installed files,
changed drivers, split volumes and existing transactions are refused.

Native `W11-software-update-02` passed three deliberate errors after program
swap, GeoIP swap and installation metadata replacement, followed by a successful
real package update and five standard-user activation/IDLE/Shutdown cycles.
Each rollback verified the original installed inventory and a non-exported
aggregate hash of profile files. The successful update retained that profile
hash and the authorized desktop SID; final native journal/WFP/adapter/firewall
inspection passed. This replaced the earlier `fbc6271` daemon with the
maintenance-enabled daemon SHA-256
`7dd46ae7c7ad3299576a72f4c1dcf3bc58226d2aa165f6438835d338059391c3`.
Manifest changed from `125230cab9291a5a683cfec01cfa8f5a7c8000fb3e4b43533d6feba4b208c586`
to `f036d99e50e29f2c813eea4720e870fce43531eee392a72314cfbff03cf1042b`.
The manually assembled lab manifest is not release build provenance.

The earlier `-01` stopped before installed-file/service mutation: PowerShell
marshalled `$null` as an empty backup path in `File.Replace`. An isolated native
reproduction confirmed that `[NullString]::Value` fixes the argument. That failed
transaction was preserved after verifying the unchanged installed baseline;
rollback now also preserves the primary error when a secondary error occurs.

The checked-in `tests/update-desktop-test.ps1` then ran as
`W11-software-update-03`, guest exit 0, **10 checks PASS**: wrong digest,
development payload in release mode, release fault injection, unowned-file
preservation, the three rollback points, successful replacement with retained
profile/SID/start modes, refusal to reuse an existing transaction, and native
clean-state inspection. This repeat changed the lab manifest/version but reused
the same maintenance-enabled binary; `-02` is the actual different-binary proof.
The backend also rechecks installed processes immediately before swapping, in
addition to its initial UI/bridge preflight. This narrows a staging race but is
not a complete GUI file-holder acceptance test.

Allowlisted evidence and exact script inputs are archived under
`IRVIO_WINDOWS_LAB/evidence/w11-software-update-02` and `w11-software-update-03`.
Archive SHA-256 values respectively:
`c3a64ed9566e310e42b1af90cc13be2bd4cddbaae91b3c73bebdc0a85dfca6f1` and
`7cfa787274416767f631e5c6bc3b2cbe6340be1ca05265f94d4c9097584bdae2`.
No profile/identity contents were exported in these archives.

Affected maintenance regressions passed again against the updated installation:
`W11-update-maintenance-stop-01` **15 PASS** and
`W11-update-maintenance-clean-01` **8 PASS**, combined guest exit 0.
Their allowlisted archives are stored under the corresponding lowercase names
in the same evidence root. An initial wrapper exited before testing because the
older VM source overlay lacked the two test scripts; supplying those scripts
resolved this harness-only issue without changing product behavior.

This is **synchronous rollback evidence**, not interrupted-install/update or
power-loss acceptance. There is no transaction recovery/finalizer or frontend
update choice yet. Changed drivers, schema migrations, split volumes, active
VPN/downstream maintenance, GUI file-holder races and signed release gates are
still open. No Linux runtime or workstation networking was changed.

## W11 interrupted updater recovery — 2026-10-11

Added `recover-update.ps1`, shared protected update primitives and a dedicated
update lock covering staging through finalization. Recovery verifies the
canonical journal paths, protected metadata, actual old/new image hashes and
the existing desktop authorization. The installed-image verifier can inspect
retired program/GeoIP directories without rewriting the live installation record.
When a swap was interrupted, the services must still be stopped/disabled and the
independent native cleanup probe must pass before files move. A committed update
is kept; uncommitted swaps restore the old package. Terminal transactions and
metadata fragments move into private `update-history/<id>` rather than being
deleted, allowing another update without manually removing a pending directory.

`W11-update-recovery-02/gate` completed with guest exit 0 and **11 PASS**:
exclusive concurrent-update refusal; abrupt updater termination at prepared,
fenced, program-retired, program-swapped, GeoIP-retired, GeoIP-swapped,
metadata-written and committed boundaries; refusal/preservation of a foreign
journal target; and final native clean-state verification. Each child exited
through `Environment.Exit(99)`, bypassing updater catch/finally. Recovery ran
separately after that process ended. Assertions verified original service modes,
stopped state, full inventory, authorized SID and unchanged aggregate profile
hash. The missing canonical program/GeoIP paths were explicitly observed in the
two retired-directory cases. A partial metadata fixture was preserved in history.

The candidate changed its manifest/version and GeoIP attribution fixture, using
the same maintenance-enabled daemon. The earlier software-update `-02` gate,
not this one, proves replacement by a different binary. This gate does not
claim a state-schema migration or a new complete release build.

Before this run, the native installed-inventory regression passed 19 checks against the
refactored helper. The first interruption harness (`W11-update-recovery-01`)
stopped because `Start-Process`/refresh did not retain a usable exit-code result;
read-only inspection found no surviving child and a valid prepared transaction,
with original service modes. That transaction was recovered and retained. The
corrected harness owns a `Diagnostics.Process` handle from creation through
exit-code collection. No product failure was hidden by restarting a live child.

After all eight interruption cases, the unchanged synchronous-update regression
passed **10 checks**, guest exit 0, and `recover-update.ps1` finalized its verified
committed transaction. Final installed services were stopped with IRVIO Manual
and recovery Automatic; no pending software-update directory or maintenance
record remained. The profile and existing installed daemon stayed in place.
Exact script hashes matched the local source inputs.

Allowlisted evidence is archived under
`IRVIO_WINDOWS_LAB/evidence/w11-update-recovery-01` and `w11-update-recovery-02`;
archive SHA-256 values respectively:
`474e8b328b809a9ca2563f283ba24bfc3e351eac8e9e6716e5d7e0369d6ff23d` and
`66d8ba8c86cd780a7fba0d6d089bdab8168f704cee8c8fe028c7ce596cd90abe`.
These include the failed harness evidence, corrected scripts and bounded logs,
not profile/identity data or full package backups.

Remaining: power-loss/real-reboot interruption, interruption during recovery
itself, incomplete initial journal/partial-stop recovery, bounded history
retention, active-session/downstream cases, repair/uninstall, frontend integration
and signing/provenance. Refused unknown states remain preserved for diagnosis;
this gate is not blanket installer or full Windows-port acceptance.

## W11 recovery reentry and single-EXE update — 2026-10-11

Recovery now has development-only process interruption checkpoints and a verified
no-op after completed archival. `W11-recovery-reentry-01` passed **7 checks**:
interrupt recovery after GeoIP restoration, program restoration, metadata
restoration, rollback journal commit, service-mode restoration and archive move;
then rerun recovery and verify the installed image, profile aggregate, desktop
SID, original service modes and terminal evidence. The seventh check independently
inspected native cleanup. Each case first terminated a real updater after its
metadata write (exit 99), then terminated recovery at its checkpoint (exit 98).
This establishes process-level reentry, not storage power-loss recovery.

The development EXE now embeds eight allowlisted backend scripts and dispatches
between fresh install, pending-update recovery, update and same-package no-op.
The pre-elevation desktop SID cannot be changed by updating another user's
existing installation. Recovery requires the same candidate that owns the
pending journal. Unknown/manual installations, unrelated maintenance, damaged
inventory and profile/SID takeover remain refusals, not implicit repair.
Build metadata records all embedded backend hashes. Bundle tests require every
backend and reject unknown/unsafe entries; extraction and UAC identity handling
retain the existing protections.

`W11-setup-update-build-01`: native `go vet ./cmd/irvio-setup`,
`go test -race -shuffle=on -count=5 ./cmd/irvio-setup`, trimpath GUI build,
embedded manifest resource check and self bundle validation all passed.
`W11-setup-update-native-01` passed **4 checks** against that actual EXE:
wrong SID rejected before modifying a pending transaction; recovery from a
missing canonical program directory followed by update/finalization; repeat
of the identical installer as a verified no-op; native clean-state inspection.
Five standard-user activation/IDLE/Shutdown cycles then passed, followed by
another independent cleanup check. Profile hash and original service modes
were preserved, and no VPN connection was started by installation/update.

This EXE gate used the existing explicit development-unattended path. It does
not repeat the earlier interactive alternate-admin UAC proof for every update
case, nor exercise a fresh installation through the new dispatcher. The package
still uses the earlier maintenance-enabled daemon/UI and lab manifest/version;
it is not a signed release or full final-source provenance claim. Remaining
installer work includes fresh-dispatch regression, real reboot/power-loss,
incomplete initial journal and partial-stop recovery, bounded backup retention,
repair, profile-aware uninstall, active-session/downstream update, signing and
final release gates. Linux runtime and workstation network/proxy were untouched.

The built development EXE SHA-256 is
`99aac63a7870287b4982191344324077146c6f09523f6fa0ca0dcef5c1e9d9ce`;
all eight embedded backend hashes matched current source. Evidence and exact
inputs are archived under `IRVIO_WINDOWS_LAB/evidence/` in
`w11-recovery-reentry-01`, `w11-setup-update-build-01` and
`w11-setup-update-native-01`. Their allowlisted archive SHA-256 values are
`a131e5a25368906669227b9fa01d5df91a1e342f759749da0ed8440720ba3579`,
`86fadd9738e5251e95b428bdd5bc5ac6971e985590002a775a3a6d9d19d67a61`, and
`e44f85f6ef7345fecff0ba72d0bc375a8443dafcd8ebffdfd2ea48260edb431d`.
Final services were stopped (IRVIO Manual, recovery Automatic); no pending
update/maintenance record remained. Full package backups stay in private guest
history, not in the evidence export or Git.

## W11 retained-profile uninstall/reinstall — 2026-10-11

The new development backend fences the owned services, independently checks
native cleanup, removes only manifest-owned files and empty owned directories,
and removes the stopped SCM services/driver. Protected profile/configuration and
diagnostic history are retained. A completed receipt permits reinstallation for
the same authorized desktop SID; another user cannot inherit the profile. This
is not yet the single-EXE uninstall UX or the full-data-erasure option.

Native evidence changed three implementation details:

- The old Start Menu shortcut inherited delete permissions for additional lab
  users. Uninstall refused it before changing application files. New installs
  assign an explicit admin/SYSTEM-write, Users-read ACL. Only the exact verified
  lab shortcut was migrated; before/after SDDL and unchanged contents were checked.
- WMI `Win32_SystemDriver` returned no WinDivert after its binary was removed,
  while `sc query` and `sc qc` still showed its stopped SCM registration. The
  first backend therefore incorrectly declared completion. Service inspection
  now uses native OpenService/QueryServiceConfig/QueryServiceStatusEx and treats
  only ERROR_SERVICE_DOES_NOT_EXIST as absence. The exact leftover lab driver
  registration was verified and deleted; the failed evidence was retained.
- Reinstallation copied read-only dependency licenses, which ordinary
  PowerShell Remove-Item refused. Verified individual file removal now uses
  Force for attributes; hash/path/ACL checks remain, and no recursive removal
  was introduced in the uninstall backend. The interrupted transaction resumed
  through its original journal.

The installed-inventory regression passed **19 checks** against the refactored
manifest reader. Corrected interruption gate `W11-uninstall-retain-05` completed
with guest exit 0 and **6 PASS**: unknown-file refusal/preservation; abrupt exit
97 before file removal, after first file, after all application files, and after
first service deletion, each followed by separate resume, idempotent removal,
wrong-user refusal and same-user reinstall; and final native cleanup. The test
checks aggregate profile hashes, the original SID, an unknown retained data-file
sentinel, absent application objects/services after removal and full manifest
inventory after each reinstall. Both daemon services remain stopped on reinstall.

The isolated ownership fixture passed **9 checks**, including missing-file resume,
modified/unowned files, unknown empty directories, read-only files, weak ACLs,
foreign service-name rejection and unchanged final contents. Five standard-user
activation/IDLE/Shutdown cycles passed after the final reinstall, followed by
another independent native cleanup check. All three jobs exited 0. No CONNECT
or live downstream operation is claimed by these installer checks.

Earlier `-01` through `-04` attempts are not acceptance passes. This gate uses the
existing 507-file development payload, not a newly built/signed Windows release.
Workstation proxy/network configuration and Linux runtime were unchanged.

Still required: full profile erase with the existing native ownership safeguards,
uninstall frontend/Apps registration, repair, initial-journal/partial-stop and
real power-loss recovery, active session/downstream and GUI-holder cases,
bounded history retention, and the remaining full Windows-port acceptance plan.

Allowlisted archives for attempts `-01` through `-05` are retained under
`IRVIO_WINDOWS_LAB/evidence/w11-uninstall-retain/`. The final archive SHA-256 is
`f247b446c663aa5a56bbfcc3147fd46b783ee88db25571c619b10f9b7f8e219c`;
guest and host digests matched. No profile keys or package backup trees were
exported. Final script hashes matched the tested local source. The reinstalled
daemon is stopped/Manual, recovery stopped/Automatic; WinDivert is loaded after
the activation test, not left behind by uninstall. No pending uninstall,
maintenance or software-update transaction remains.

## W11 explicit profile erasure — 2026-10-11

`uninstall-desktop.ps1 -RemoveProfile` records an explicit erase choice before
mutation; omission still means preserve. Reentry must use the same choice.
The new candidate-daemon-only `--maintenance-remove-profile` operation accepts
no additional CLI arguments or arbitrary target path. It requires elevation,
canonical stopped/disabled desktop services and the existing maintenance checks.
Where runtime state exists, its exclusive product lock stays held through the
network inspection and profile deletion. The command does not start networking
or run recovery to hide an unclean state.

`productprofile.DeleteExistingContents` never creates/adopts an absent or legacy
profile. It accepts already empty state; otherwise the existing owner marker,
ACL/link/type checks and pin-all-files-before-deletion safeguards apply. The
existing Delete Profile implementation performs the deletion, with marker last.
Unknown, busy, replaced or unsafe files remain refusal conditions. Configuration,
diagnostic/update history and UI preferences are not deleted by this option.
Filesystem deletion is not a physical secure-erasure guarantee.

Native Windows `go vet` and `go test -race -shuffle=on -count=5` passed for
`cmd/irviod`, `internal/productprofile` and `internal/winfile`, including new
no-create/no-adopt/reentry/refusal tests. A trimpath daemon build passed. Affected
Linux vet and race/shuffle x5 for cmd/irviod and productprofile passed on the
mini-PC; no tests or VPN operations ran on the workstation.

`W11-profile-erase-01/native-02` passed **7 checks**: unfenced erase refusal,
busy runtime refusal, intent residue refusal, unknown file refusal, actual erase,
idempotent erase and unchanged installed/native state. The disposable profile
was populated with real Ed25519/AWG identities and SQLite using existing native
helpers under a fenced service, without network participation. The initial test
correctly refused an IDLE profile that did not yet contain an identity; it did
not erase anything and was replaced by this explicit populated fixture.

`uninstall-02` passed **6 checks**: abrupt child exit after erasure but before
application file removal; changed-choice refusal; resume/idempotent removal;
reinstallation without resurrecting the old profile; new identity creation;
and native cleanup. The first harness attempt passed unquoted Go test flags
through PowerShell and stopped before fixture creation/removal; it remains
recorded separately. Affected maintenance stop and independent cleanup gates,
and final-source native vet/race, passed after the successful reinstall.

The default retained-profile interruption regression passed **6 checks** with
the populated real identity/AWG/database fixture, including all four interruption
points and aggregate profile preservation. Five standard-user activation and
Shutdown cycles passed afterwards without changing that identity. A mixed
`--maintenance-remove-profile --data-dir ...` invocation was rejected before
mutation. Final independent native cleanup passed, with no pending uninstall,
maintenance or update transaction. All native jobs reported exit 0.

This is a development payload with a newly built daemon and
the earlier verified UI/dependencies, not a final-source signed release. Full
EXE uninstall UX, Apps registration, repair, initial metadata/power-loss cases,
active downstream/lifecycle scenarios and remaining Windows acceptance gates
are not implied by these results.

Allowlisted evidence is archived under
`IRVIO_WINDOWS_LAB/evidence/w11-profile-erase-01/`; archive SHA-256:
`20b4484f8c330acf7a65f4825cdc503e857d60dd1af4e60e05cb554af41545ee`.
Guest and host archive/source hashes matched. No profile contents or keys are
included. The tested trimpath daemon SHA-256 is
`c205047f14fd4e6fcf02acd7b9cd832e18a18d4eb6e1b36ac6dc6d494b92f514`;
development payload manifest:
`95434c601e19af2ef86e39c7b72e5e6b4a5b61809c9f678d13c6582ead40252e`.
The secondary VM retains this verified reinstalled development image with its
new disposable identity; daemon/recovery are stopped, WinDivert remains loaded
after normal activation. No user VPN connection was initiated by these gates.

## W11 single-EXE removal frontend — 2026-10-11

The development setup EXE now accepts public `--uninstall`. It asks whether to
keep (default) or erase the connection profile, then separately confirms removal
before requesting elevation. The original desktop SID and selected choice pass
through the existing UAC mechanism. The backend checks that SID under its update
lock against the installed, pending or completed removal record before mutation.
Zero arguments still install/update. No wire, VPN lifecycle or Linux changes are
part of this frontend increment.

Native Windows vet and race/shuffle x5 passed for `cmd/irvio-setup`, including
unattended-release refusal, invalid arguments, elevated-only internal intent,
SID/choice fidelity and all eleven required embedded backend scripts. Native
trimpath setup build, bundle verification and asInvoker manifest checks passed.
The actual development EXE passed **7 checks**: wrong-owner refusal without
profile/transaction changes; keep-profile removal; retained-profile reinstall;
erase-profile removal; recorded-choice refusal and idempotent erase; reinstall
without resurrecting the profile; independent native cleanup.

Three visible-dialog cancellation paths passed in the limited interactive
IrvioLab session: initial Cancel, keep then final No, erase then final No. The
screenshots show the distinct confirmation text. Each EXE exited 0 with the
installed record and full inventory unchanged and no transaction begun. This
session is not the installation's authorized desktop account; it proves only
pre-elevation cancellation, not actual alternate-admin UAC removal. Initial
combined mouse events / Escape did not close the dialog; the verifier correctly
refused to record a pass. Distinct mouse down/up events and explicit No/Cancel
completed the checks. No UAC confirmation or removal was performed by this gate.

Five subsequent ordinary-user activation/IDLE-EMPTY/Shutdown cycles passed from
the SYSTEM harness, followed by independent native cleanup. The first attempt
from an SSH administrator lacked the alternate-user process creation privilege
and failed before the service started; its failure log is retained separately.
The SYSTEM run changed only the disposable test user's password for its existing
alternate-user fixture, not the interactive IrvioLab account. The temporary
preview task and exact verified preview EXE copy were removed. No pending
maintenance/update/removal remains. The secondary VM keeps the reinstalled
development image with an empty connection profile; no VPN was connected.

Evidence: `IRVIO_WINDOWS_LAB/evidence/w11-setup-uninstall-01/`, with allowlisted
logs/results/source hashes and three screenshots, no profile contents or keys.
Archive SHA-256:
`b047dc74df12c4328cf95458ca5f2462c98fad6ca0bd9984be8ca2c16f17db3c`.
Guest and host hashes matched; tested source hashes match this worktree.
Development setup EXE SHA-256:
`4b5ec18f7be7425ad721153d1661bf310018a645354aecf4cdb63283b3bde3a8`.
Payload manifest remains
`95434c601e19af2ef86e39c7b72e5e6b4a5b61809c9f678d13c6582ead40252e`.
This is an unsigned development frontend with the previously verified payload,
not a final-source signed release. Apps & Features registration, retained
uninstaller, repair, interrupted fresh installation, active-session/downstream
maintenance and the outstanding Windows-port gates remain incomplete.

## W10 native server-preset / verifier replacement regressions — 2026-10-11

`TestWindowsProductServerPreset` checks the Windows-resolved headless preset:
canonical profile/MMDB/journal paths, TCP/4000, UDP/51820, bound STUN, DHT server,
Rendezvous and EXIT, automatic country resolution, dynamic production verifier
discovery, no test-address allowances and no desktop connect/protection intent.
This is a configuration test using supplied host facts, not proof of OS route,
STUN, GeoIP or server readiness on a deployed host.

`TestWindowsRuntimeReplacesLostVerifier` exercises production M11 request and
fresh TCP challenge transports on native Windows. Three verifier identities use
distinct loopback addresses to simulate independent hosts. The subject first
gets a two-verifier proof, then one verifier service closes. Normal automatic
refresh selects the replacement; the resulting valid signed proof contains the
surviving and replacement identities, excludes the lost verifier, preserves
NetworkGeneration and republishes Public Control readiness. No mocked challenge,
manual refresh, relaxed quorum or production timeout change is used. Loopback
independence is a fixture, not a claim of independent physical failure domains.

Native vet passed for cmd/irviod and internal/runtime. The preset passed
race/shuffle x5. Five Windows runtime tests (quorum/private distributor, native
control-policy requirement, failed request, failed challenge, replacement) each
passed race/shuffle x3: **15 executions**. Replacement test total durations were
6.13, 5.86 and 5.75 seconds with the test's 12-second proof TTL / 6-second renewal
lead; these are not production failover latency measurements.

Evidence: `IRVIO_WINDOWS_LAB/evidence/w10-native-verifiers-01/`;
archive SHA-256:
`ca25647c19e58ee5348cf28b4831131f97ff287be8b7c52010cd4edc6cfc1c36`.
Both newly added/extended tests are Windows-only; production code is unchanged.
These gates do not complete W10. The current installer ownership record accepts
desktop only; headless SCM deployment/maintenance, complete server readiness,
real second-client Internet traffic, restart and NAT/public-host cases still
require implementation/verification. Neither W10 nor overall Windows acceptance
is marked PASS.

## W10 headless EXE installation and native server lifecycle — 2026-10-11

The Windows setup frontend now accepts `--role server --bootstrap <v1-bundle>`.
It reuses productbootstrap Decode/Encode and the existing eligible IPv4 STUN
derivation check before elevation. All bundle entries remain in the canonical
code passed to the backend. The new fresh-install backend projects the verified
payload onto daemon, driver, GeoIP and licenses; it installs no UI, desktop SID
authorization or shortcut. The SYSTEM service command is literally the canonical
daemon with `--role server --bootstrap <bundle>`, no engineering overrides.
It has automatic startup/failure restart. Recovery is demand-start for server;
the main runtime recovers its journals under the existing product lock.

Shared inventory helpers accept a server record only through an explicit role
argument; their default remains desktop and rejects server state. Server metadata
hash-binds the protected bootstrap file. Strict command, account, driver, path,
file hash and ACL checks apply. Existing installations are refused instead of
silently converted or overwritten. This increment changes no Core/wire behavior.

Native setup vet and race/shuffle x5 passed. The final backend passed **18 desktop
inventory fixtures** and **18 server fixtures** (including role isolation, command,
bootstrap, driver, protected ACL and free/busy port checks). Initial real desktop
inventory and refusal to overwrite that desktop also passed before its disposable
installation was removed with the prior verified EXE. Its complete retained state
was moved outside canonical paths and is not part of exported evidence.

Two real-install failures were found and fixed in the new backend:

- Empty PowerShell port-query output was unrolled to null before `.Count` under
  strict mode; array capture now wraps the whole conditional pipeline. The error
  occurred before canonical server files or services were created.
- Newly copied critical files inherited ACLs; the native Exit loader correctly
  refused them with `unsafe Windows private state`. Server daemon/GeoIP directories
  and critical daemon/driver/GeoIP files now receive the same protected ACL contract
  as desktop. Inventory also rejects inherited critical-file ACLs. No loader check
  was relaxed. The failed installation was fenced, independently checked clean,
  then its exact owned services removed and all files/state retained for inspection.

`W10-server-setup-03/native` passed **6 actual EXE/lifecycle checks**: headless
SCM startup (213 installed payload files); daemon-owned TCP/4000 and UDP/3478,51820;
desktop-maintenance refusal; server-overwrite refusal; a real stop/start preserving
identity; final graceful stop with no adapter/route/DNS delta and zero owned
firewall rules. Independent `TestWindowsNativeProductIdleCleanup` passed: runtime
lock obtainable, no intent/WFP residue, network journals inactive. These EXE gates
use the explicit elevated development-only internal entry, not interactive server
UAC acceptance. Public argument/elevation fidelity is covered by native unit tests.

Both server starts connected to all three supplied production bootstraps, completed
DHT bootstrap, started STUN and the fixed socket mux, and resolved country **RU**
through the existing MMDB path. Public M11 inbound challenges failed with
`public control endpoint is unreachable`; the recorded Public Control endpoint
count remained zero. This is consistent with the VM's non-public NAT topology;
no successful M11/Rendezvous/selectable Exit or second-client proof is claimed.
The installer explicitly distinguishes SCM Running from full Exit readiness.

The secondary VM now holds the headless development server, deliberately
**stopped/Disabled** by the test teardown. Recovery is stopped/Manual. The primary
VM and workstation network/proxy were not changed. The original desktop state is
retained under `W10-server-setup-01/native/retained-desktop`; the failed server image
under `W10-server-setup-02/retained-failed-installation`. Those directories contain
private state and must never be exported with test results.

Evidence: `IRVIO_WINDOWS_LAB/evidence/w10-server-setup/`, allowlist only:

- Attempt 01 archive: `0a657592bb4d22b3581af6109063d2ac5d44ea8b87464261c63088a1791de035`.
- Attempt 02 archive: `a0b77b464524751f1db7249d58f9cee59c7af69bd1d80447b345559d750b6fd4`.
- Final 03 archive: `bcd4bfa0c13cc29cd3447046d0e752a61c236bc5b71b01d33c838567cb534d3f`.
- Failure diagnostics: `692b52c9e25956a52e171ea59885b88d884e5ce07ac53e627b20408d57c472b0`.

Guest/host hashes and final tested-source hashes match. Final development EXE:
`cd375ced04dbec1cc478dd377206a3ecdc031321fd62c02d330bfc5e0dddeca2`.
Payload manifest remains
`95434c601e19af2ef86e39c7b72e5e6b4a5b61809c9f678d13c6582ead40252e`:
the previously verified daemon/dependencies, not a new signed release payload.
Server update/uninstall/repair, incomplete-install recovery, real boot/autostart,
public/NAT M11 success, independent client traffic and the remaining W10/W12 gates
are still open. Overall Windows acceptance is not complete.

## W10 server maintenance fence — 2026-10-11

The native maintenance probe now recognizes the exact installed server command
using protected, bounded ownership metadata and the hash-bound canonical bootstrap
bundle. It still requires both canonical SYSTEM services to be cleanly stopped
and disabled, acquires the existing product lock, and rejects intent, WFP, tunnel
or active network journals. The desktop bare-command fence remains unchanged.
No production network protocol, discovery or protection semantics changed.

Shared maintenance helpers require explicit `-Role server`; default desktop calls
continue to refuse a server installation. Server journal schema 2 binds bootstrap
and manifest digests; desktop schema 1 remains compatible. The desktop update
recovery's synthetic validation layout now explicitly includes its desktop role.
Server recovery-service inventory permits Disabled only for explicit maintenance;
normal installation still requires demand-start. Modes restore without starting
either service. This implements the shared prerequisite, not server update/remove.

Native VM `W10-server-maintenance-02` passed `go vet ./cmd/irviod` and
`go test -race -shuffle=on -count=5 ./cmd/irviod`, plus **18 desktop inventory** and
**18 server inventory** fixtures. Attempt 01 failed only in the new unit fixture:
its atomic write used create-only mode for a replacement. It was corrected to
replace and to use the current subtest's testing handle; the failed log is kept.

Final `W10-server-maintenance-03` passed **15 real maintenance checks**: desktop
default refusal, native unfenced/running refusal, graceful running-server stop,
duplicate stop refusal, wrong bootstrap/role/service records, native clean-state
probe, exclusive restore lock, mode restoration without restart, partial-disable
fault/recovery, unchanged identity/installation, baseline route/DNS equality.
An additional **9 record fixtures** passed for both schemas and cross-role/type
refusal; all packaging PowerShell scripts parsed successfully. These do not claim
a new full desktop interrupted-update campaign or public server readiness.

The installed service binary remained the previously verified headless payload;
the newly built candidate daemon was used as the independent maintenance probe.
Its SHA-256 is
`83f7b78942748740cc064bee4c8df213c4bf24feac6b9769df305f8447f6ad8a`.
Final secondary VM state: IRVIO Stopped/Disabled, exit 0; recovery Stopped/Manual,
exit 1077. No force termination, profile removal or workstation networking change.
All job handles completed; no test remains running.

Allowlisted evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w10-server-maintenance/W10-server-maintenance-evidence.tar`
SHA-256 `1bb5a020afedc5c865fd6d23594e73906835fb6abab76ee3ce8196e3bdef1ecf`.
Guest and host archive hashes match, as do all seven tested source inputs.
The archive contains test logs/results and hashes, not identity keys/profiles.
Server update/uninstall/reentry, full public readiness/traffic, bind-51822 forensic
closure, signing and the other full Windows acceptance gates remain unfinished.

## W10 server software replacement — initial gates, 2026-10-11

The shared update/recovery backends now accept explicit `-Role server` while
retaining their desktop default and historical filenames. Server staging omits
UI, retains protected daemon/GeoIP files, preserves bootstrap ownership and the
entire profile, and refuses driver changes through this software-only path.
The existing journal, rollback, native maintenance fence and exclusive locks are
reused. New record construction preserves role-specific identity fields without
mutating the old record. Public server EXE update routing is not implemented yet.

On the secondary Windows VM, `W10-server-update-01` passed **10 synchronous
checks**, including three injected replacement failures/rollback, and **11
interruption checks**, including eight installer exit points, concurrent updater
refusal, foreign journal path refusal/preservation and final native clean-state
inspection. Before commit, recovery restored the old image; after commit it kept
the new image. Full profile digests, bootstrap ownership and original service
modes were unchanged. **6 role-record fixtures** passed for desktop/server owner
preservation, unchanged original metadata, cross-role refusal and bad manifests.

Distinct candidates were used, not the same image renamed:

- Original manifest: `95434c601e19af2ef86e39c7b72e5e6b4a5b61809c9f678d13c6582ead40252e`.
- Candidate A manifest: `5f6b4c08000898851e93df9309037a223fe15108e2af8e8a3e921ab99f243484`.
- Candidate A daemon: `83f7b78942748740cc064bee4c8df213c4bf24feac6b9769df305f8447f6ad8a`.
- Candidate B manifest: `51a8e6046dcfc0dd8e133ab3d892f403354d9faab2b564ff1ef773ce1b43e542`.
- Candidate B trimpath daemon: `a325d7049fde3733e1678e18e8bf28eb42c54db661d6a0b86173da2cb1fccdc2`.

Both use the same verified development dependencies and maintenance-capable
source; this is not a clean-source signed release build. The initial server
campaign operated on a stopped server and left candidate B installed/stopped.
No claim of active downstream maintenance, power-loss, fresh-install repair or
complete Windows/server acceptance follows from these results.

Evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w10-server-update/W10-server-update-01-evidence.tar`,
guest/host SHA-256
`27468385f944b0525636d415421d176aae44d5ff65cf9e41f2b950253fe13e46`.
Only direct test logs/results/hashes were exported, not candidate binaries,
profiles or recursive transaction backups.

Follow-on `W10-server-reentry-01` passed **7 reentry checks**: six recovery-process
exit points and final native clean state. It also passed **4 running-server
checks**: old server fixed sockets, graceful update of the running daemon, new
server restart with fixed sockets and unchanged identity/bootstrap, and native
cleanup with exact route/DNS baseline restoration. Candidate B was replaced by
candidate A in the running-server gate. Neither downstream-session maintenance
nor public M11/Exit readiness is implied.

Reentry/running evidence:
`IRVIO_WINDOWS_LAB/evidence/w10-server-update/W10-server-reentry-01-evidence.tar`,
guest/host SHA-256
`b771e503d087c66bc5d9bdf6fbba2c84769c2ff4fb288409433bb66dd1ea938f`.
Final server state in this archive is Stopped/Disabled, exit 0; recovery is
Stopped/Manual, exit 1077. The archive excludes profile and transaction trees.

`W11-update-role-regression-01` completed the affected desktop update gates.
Its harness first verified the stopped server ownership/native cleanup, removed
only its three exact SCM registrations, and moved complete server files/profile
into a protected retained directory without deleting keys. It then installed a
fresh disposable desktop and created a native identity/AWG/database fixture.
It passed **10 synchronous**, **11 interruption** and **7 recovery-reentry** checks
using the default desktop role and two distinct daemon images. A populated native
identity/AWG/database fixture was retained through every rollback/update; default
desktop SID authorization and service modes were preserved. The desktop was then
uninstalled with profile retention using the actual backend, and the original
server files/profile were moved back and their owned SCM registrations recreated.
The retained server's installation and identity hashes matched; an independent
native maintenance probe verified no residue after restoration. Server was left
Stopped/Disabled, recovery Stopped/Manual (both exit 1077 after registration).

Evidence:
`IRVIO_WINDOWS_LAB/evidence/w11-update-role-regression/W11-update-role-regression-01-evidence.tar`,
guest/host SHA-256
`100241300a34f664a3aa42b80c125a67e606a6ec68127c1c1780025938620c9b`.
Only direct logs/results/hashes are included. The protected retained desktop
profile and saved server SCM command (containing its bootstrap) are excluded.
The first VM and workstation networking/proxy remained untouched. These are
backend regression gates, not fresh-install interactive UI or power-loss proof.

### Server EXE update integration — native gate completed

The frontend now dispatches server intent to `install-or-update-server.ps1`.
Existing server updates require the exact installed bootstrap digest, use the
shared recovery/update backend, and preserve role/profile/identity. The wrapper
starts only automatic-start servers; Manual/Disabled modes remain unchanged.
An already running same-package server is not restarted. Confirmation/completion
text describes these semantics and does not equate SCM Running with Exit readiness.
The embedded backend allowlist is now 14 scripts. The fresh-server EXE fixture
was updated to expect same-package idempotence instead of unconditional refusal.

`W10-server-exe-update-01` native `go vet ./cmd/irvio-setup` and
`go test -race -shuffle=on -count=5 ./cmd/irvio-setup` passed. Both development
EXEs built successfully with native self-bundle/manifest checks:

- Candidate A EXE: `4e3f98296385d4f2fb77b74f6b6a7a28cfe7c4fa8698838d119f628624fdaa24`.
- Candidate B EXE: `98b7d432f143c830cfc9fac93746d4fe75b194d809169a385fc53036d86553b9`.

After desktop regression restored the server, `setup-server-update-test.ps1`
passed **8 checks** using the actual development EXEs: update with Disabled mode
preserved, same-package disabled idempotence, Manual retention, automatic-start
activation, same-package running PID unchanged, automatic restart after replacing
a running server, changed-bootstrap refusal before mutation, and final native
cleanup. Server identity/bootstrap remained unchanged throughout. Final secondary
state: IRVIO Stopped/Disabled, exit 0; recovery Stopped/Manual, exit 1077.

The gates use the explicit elevated internal development entry. They are not
interactive UAC acceptance, a repeat of the fresh-install gate with this frontend,
or a public M11/Exit/second-client proof. The bootstrap mismatch case invokes the
new wrapper directly; public Go bootstrap parsing has its separate native tests.

Evidence:
`IRVIO_WINDOWS_LAB/evidence/w10-server-update/W10-server-exe-update-01-evidence.tar`,
guest/host SHA-256
`6c2be89802dcfece43526d714eb304f4a875edc0d5972209467b45df164f821a`.
The five tested source hashes match the host worktree. No keys, bootstrap files
or recursive private data are exported. All jobs in these update campaigns are
terminal; no test remains running. Server uninstall/repair, incomplete fresh
installation recovery, Apps & Features, public server readiness, W12 bind-51822
closure and the remaining full Windows/signing/release gates are still open.

### Role-aware server uninstall backend — native gate completed

The existing uninstall backend now accepts explicit `-Role server`; its default
desktop behavior and schema 1 remain unchanged. Server schema 2 binds the existing
server installation record, including bootstrap digest, and requires no desktop
shortcut. Server removal skips UI payload entries and checks the exact hash-bound
server SCM command. The same native clean-state probe, maintenance fence, profile
erase operation and exact-file/service deletion transaction are reused.

`W10-server-uninstall-01` completed in the secondary Windows VM:

- **16 role fixtures PASS**: journals, cross-role/unknown schema/profile-type
  refusal, desktop/server file projection, exact server command, and refusal of
  unfenced or live services.
- **9 shared ownership fixtures PASS**: unknown/modified/unowned files and
  directories, writable ACLs, missing-file reentry and exact service-name checks.
- **14 native checks PASS**: both profile retention and explicit erasure,
  interrupted at removing/first-file/files-removed/first-service; changed profile
  intent refused; successful completion and repetition; lab baseline restored
  with native cleanup verified after each mode.

The harness preserved protected copies of the original program/driver/state
before each destructive mode. Retained-profile digests and bootstrap hashes stayed
unchanged; explicit erasure left the profile empty across reentry/completion.
After each mode it restored the original lab backup and recreated the exact owned
services. This is **not** a product reinstall proof and does not claim that the
backed-up lab identity was irrecoverably erased. Final state is IRVIO
Stopped/Disabled and recovery Stopped/Manual, PID 0 and exit 1077 after service
registration. Independent native cleanup passed; no workstation proxy/network
changes or workstation tests were performed.

Evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w10-server-uninstall/W10-server-uninstall-01-evidence.tar`.
Guest/host SHA-256:
`1bb351765dd15889f5d88b393cabb54621674757b7d32c60700df69c64b2335d`.
The three native-tested source hashes match the host worktree. Export includes
only allowlisted direct logs/results/hashes, not private backup/retained-state
directories, bootstrap contents or keys. The first export attempt miscounted a
PowerShell 5 JSON array; correcting only the exporter produced this archive without
rerunning the completed tests. All test/export handles are terminal.

Remaining gates include the server EXE uninstall workflow, server reinstall from
the retained receipt, affected full desktop uninstall/reinstall regression,
active-session/downstream removal, power loss, partial fresh installation repair,
Apps & Features, public server readiness and the other full-port acceptance work.
These backend gates do not imply overall Windows acceptance.

### Server EXE uninstall/reinstall and affected desktop regression

The development EXE now accepts `--role server --uninstall`. Profile choice and
the server-stop warning are shown before elevation; the internal elevated intent
preserves keep/erase and cannot be supplied by an unelevated child. The server
backend is selected explicitly without desktop SID or bootstrap arguments.
The existing 14-script embedded allowlist is unchanged.

The normal server install command can now consume a completed protected server
uninstall receipt. Reinstall validates retained bootstrap/manifest ownership,
rejects unfinished maintenance or mixed desktop state, serializes retained-state
replacement, and refuses profile contents reappearing after recorded erasure.
The deployment bootstrap is retained in both modes. A successful reinstall starts
the server automatically; keep preserves identity, while erase creates a new
identity on startup. Interrupted fresh/reinstall repair is still a separate gate.

`W10-server-exe-uninstall-01` completed native setup vet and race/shuffle count 5,
all PowerShell parser checks, **8 retained-server fixtures**, and actual EXE
self-bundle/asInvoker build checks. Development EXE SHA-256:
`40550cb5e178e2ce9f38d6926aabf502db19c923400ce6ccfc7ae5c8e4b34147`.

The actual elevated EXE passed **7 native checks**: desktop intent refuses server
without starting a transaction; keep/repeat; different-bootstrap reinstall
refusal; keep/reinstall preserving identity; erase/repeat with changed-choice
refusal; erased-profile reinstall with a different identity; and native cleanup.
Both reinstall modes reached SCM Running and owned TCP/4000, UDP/3478 and
UDP/51820. These checks do not establish public M11/Exit readiness. The disposable
server identity was intentionally regenerated by the erase gate.

The secondary then ran `W11-uninstall-role-regression-01`: it first retained the
complete stopped server installation outside canonical paths, installed a fresh
desktop and populated real identity/AWG/SQLite fixtures. The same EXE passed
**7 desktop uninstall/reinstall checks**. The backend passed **6 retained-profile
checks**, including four independent interruption/resume/reinstall cycles,
unknown-file preservation, wrong-user refusal and native cleanup. The desktop
was removed with its profile retained, then the server files/profile and owned
SCM entries were restored. Record/identity hashes matched the retained server;
native cleanup passed. Final state is IRVIO Stopped/Disabled and recovery
Stopped/Manual, PID 0, exit 1077 after registration. Test observation timeouts now
report a live PID rather than killing an installer and obscuring its state.

On the primary VM, `W10-server-uninstall-preview-01` verified **3 visible public
dialog cancellations**: first-stage Cancel, keep choice followed by No, and erase
choice followed by No. Screenshots confirm the server-specific text and default
keep/No selections. All exited 0 without new setup staging or service changes;
the temporary scheduled preview task was removed. The initial launch request had
no logged-in interactive session (`SCHED_S_TASK_HAS_NOT_RUN`); launch occurred
only after login and confirmation that no preview process existed. An initial
Esc went to the inactive desktop instead of the dialog, so cancellation was
subsequently performed through the visible button. These are cancellation proofs,
not an interactive affirmative UAC/server-install acceptance gate.

Allowlisted archives, guest/host hashes equal:

- `IRVIO_WINDOWS_LAB/evidence/w10-server-uninstall/W10-server-exe-uninstall-01-evidence.tar`:
  `8b55b8a9e2609163b9b3ad65dd5dbdb2cc8827ada98a8d4dfa1c5c802db87257`.
- `IRVIO_WINDOWS_LAB/evidence/w11-uninstall-role-regression/W11-uninstall-role-regression-01-evidence.tar`:
  `e3cf18b7f6923fe6214e527c7476a02a20b81cf4a8e7da6fe378ce512eac440b`.
- `IRVIO_WINDOWS_LAB/evidence/w10-server-uninstall/preview/W10-server-uninstall-preview-01-evidence.tar`:
  `d86ab41910e0681182f9c13fe21894e34020e93db310fb40fecfd8b81694a391`.

The tested source hashes match the worktree. Private retained desktop/server
profiles, keys and deployment bootstrap contents are excluded. All jobs are
terminal; neither workstation networking nor proxy was changed. No tests ran on
the workstation. Remaining full-port gates still include incomplete-install
repair, active-session/downstream maintenance, power loss, Apps & Features,
public Windows server readiness, W12 bind-51822 forensic closure, signing and
clean-source final regressions. Overall Windows acceptance is not complete.

### W12 — synchronous session-listener bind observations (2026-10-11)

Baseline: `6fcec79`, branch `windows-port`. This is diagnostic coverage, **not a
root-cause fix** for the original Global-cycle-81 TCP/51822 WSAEACCES. The previous
successful Global100 repetition still does not explain the original failure.

Source inspection narrowed the Exit-NAT hypothesis: `ReserveWindows` binds an
exclusive socket to the concrete physical source, not a wildcard, and allocation
occurs on an admitted downstream flow rather than eagerly during Exit startup.
The native regression used the actual reservation implementation and proved
that a listener on a distinct TUN source can coexist on the same assigned TCP
port. This does not establish what owned the original failing port.

On WSAEACCES/WSAEADDRINUSE only, the Windows session listener now reads IPv4 and
IPv6 TCP owner tables immediately after the failed listen. Each family has a
4 MiB allocation ceiling, one size query and at most two retrieval calls, with
eight retained matching rows. The original error remains discoverable through
`errors.Is`; no listen retry, port change, reuse option or recovery-policy change
was introduced. No query runs on successful startup or unrelated errors.

Observations contain only PID, numeric TCP state, address classification
(`wildcard`, `target`, `other-local`), result codes/counts and elapsed time. They
contain no remote endpoints, IP addresses, executable paths or peer identities.
The runtime emits short `SESSION_CONTROL_BIND_OBSERVATION` and
`SESSION_CONTROL_BIND_PORT_ROW` records, preserving evidence through the existing
192-byte sanitized-journal field limit without increasing that limit. Parser and
maximum-size log fixtures verify bounded rows, invalid/truncated tables, byte
order, original error preservation and short records.

The native table layout follows Microsoft's
[GetExtendedTcpTable documentation](https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-getextendedtcptable),
[IPv4 owner rows](https://learn.microsoft.com/en-us/windows/win32/api/tcpmib/ns-tcpmib-mib_tcprow_owner_pid)
and [IPv6 owner rows](https://learn.microsoft.com/en-us/windows/win32/api/tcpmib/ns-tcpmib-mib_tcp6row_owner_pid).
An empty observation is **not** proof that no conflict existed: sockets may close
before capture, and bound-only reservations may be absent. Native API execution
has bounded calls/memory, not a promised hard wall-clock deadline.

`W12-session-bind-diagnostic-02`, secondary Windows VM:

- `go vet` and `go test -race -shuffle=on -count=5` passed for sessioncontrol,
  runtime and localapi; normal and trimpath daemon builds passed.
- Ten repetitions of exclusive wildcard conflicts passed for both IPv4 and IPv6:
  real WSAEACCES retained, same-process listening owner observed, and release of
  the blocker permits the original tunnel listener without a product retry.
- Ten real physical-source NAT reservation/TUN-listener coexistence checks passed.
- Full native session-control suite passed with checkptr=2, including **100/100**
  fixed-port adapter/listener cycles, tunnel-bound SYN/cancellation, IPv4/IPv6
  Noise BIND/RENEW/RELEASE, physical-source rejection and interface identity fencing.
- Exact route/DNS/adapter baseline comparison was empty after cleanup. No test
  process or adapter remained; installed IRVIO stayed Stopped/Disabled and
  recovery Stopped/Manual, both PID 0. Neither installed daemon was replaced.

The mini-PC independently passed Linux vet and race/shuffle count 5 for those same
three affected packages. Common hook/runtime source hashes matched the workstation.
The Windows build used the existing lab source tree plus hash-verified overlays;
this is not the final clean-checkout whole-tree release gate.

Preserved orchestration failures: the first PowerShell native invocation split a
test flag; its corrected successor passed focused checks but the four-minute
fixed-port test budget expired during cycle 68. That process was terminal and
left no test adapter before the final suite used a 720-second harness timeout.
No product timeout was altered. The first Linux attempt selected an empty default
module cache with GOPROXY=off; the retry used the existing populated offline cache.

Allowlisted evidence and harness/source overlays are under
`IRVIO_WINDOWS_LAB/evidence/w12-session-bind-diagnostic/`; Windows archive
`W12-session-bind-diagnostic-evidence.tar` SHA-256:
`62ac43e0516886afe1a59b3e9f5aed8c41a6f07a19e736a88cee170943752c21`.
Guest/host archive and all five changed source hashes matched. Linux logs are in
the adjacent `linux/` directory; no profile, key or bootstrap payload was exported.

Windows normal binary SHA-256:
`1743e296fdfd29e1e9fc26b4893953a89480401757b4bfc782379db41a05c583`.
Trimpath SHA-256:
`9c3caa707d5cf10566e412c4e0851f26ed28d87eb42ed2506b2c48cfebd162f1`.
Both remain in `C:\IRVIO-Lab\W12-session-bind-diagnostic-02` on the secondary VM.

All jobs are terminal. Workstation proxy/networking was untouched, and no tests
ran on the workstation. Next forensic work needs a product-level recurrence with
the instrumented binary; the original W12 bind failure and full Windows Alpha
acceptance remain open.

### W12 — instrumented Global100 and clean-source Go gates (2026-10-11)

Baseline `c77ed9a`, branch `windows-port`. The primary Windows VM completed
**100/100 real Global CLIENT+EXIT cycles**, with no warmup cycles excluded, using
diagnostic daemon `1743e296fdfd29e1e9fc26b4893953a89480401757b4bfc782379db41a05c583`.
Each cycle checked NL country intent, CONNECTED, a 64 KiB HTTPS download, blocked
physical-source HTTPS, DISCONNECT, absent contribution/session and exact idle
route/DNS/adapter baseline. The daemon PID remained the same throughout.

| Measurement | Minimum | Median | p95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| Connect, ms | 9,857 | 10,637 | 13,050 | 25,496 |
| Disconnect, ms | 1,527 | 1,668.5 | 1,903 | 1,935 |
| Handles at idle | 526 | 564 | 569 | 569 |
| Threads at idle | 22 | 23 | 24 | 24 |

Total campaign time was 1,510,783 ms. All 100 idle samples had zero TCP sockets,
UDP sockets and FIN_WAIT2 connections. Total verified HTTPS payload was
6,553,600 bytes; all 100 targeted physical-WAN probes were denied. These probes
are not an exhaustive packet-capture proof for every possible leak tuple.

| Cycle cohort | Median handles | Median RSS bytes | Median private bytes |
| --- | ---: | ---: | ---: |
| 1–20 | 560 | 57,309,184 | 71,567,360 |
| 21–40 | 560 | 61,081,600 | 74,684,416 |
| 41–60 | 561 | 64,092,160 | 77,604,864 |
| 61–80 | 564 | 64,067,584 | 77,502,464 |
| 81–100 | 569 | 64,849,920 | 77,881,344 |

RSS peaked at 66,871,296 bytes and ended at 63,676,416; private bytes peaked at
80,654,336 and ended at 77,246,464. Handles ended at 566. Memory growth slowed
after the initial cohorts and was not monotonic; the modest handle increase
remains an observation, not proof of either an unbounded leak or its absence.
There was no idle socket accumulation or network-state residue in this run.

The synchronous bind diagnostics and independent observer saw **no recurrence**
of the original TCP/51822 WSAEACCES. The observer completed normally with no
captured blocked events. This does not close that original failure or identify
its cause. After the run, audit policy, original daemon binary/ACL and stopped
service state were restored; native cleanup, ordinary-WAN HTTPS and exact
network baseline checks passed.

Allowlisted Global100 archive:
`IRVIO_WINDOWS_LAB/evidence/w12-global-bind-instrumented-01/W12-global-bind-instrumented-01-evidence.tar`,
SHA-256 `0172631bf913bf4de04a79ae213b9358dd0d02fb5415e62f90fc9c87e9dd066e`.
No profile, key, bootstrap or executable backup was exported.

The secondary VM separately tested a clean `git archive c77ed9a` source tree,
archive SHA-256 `aea25094ef4cab60c30da84c10534b27a4b17a5c4dd1a4d648e08428411ab2b8`.
All seven stages passed with native Go 1.27.1 and the existing offline module
cache: `go vet ./...`, normal tests, shuffle count 10, race, race/shuffle count 5,
normal build and trimpath build. The exact temporary loopback fixture cleanup
delta was empty. Separately gated privileged tests are not implied by this
whole-tree result.

Normal daemon SHA-256:
`136ad646ffc76c3e076edbcdad0ae9802439cfaa189e1d38fae11060a305b7c4`.
Trimpath daemon SHA-256:
`9c3caa707d5cf10566e412c4e0851f26ed28d87eb42ed2506b2c48cfebd162f1`,
identical to the earlier overlay build's trimpath output.

Allowlisted clean-source archive:
`IRVIO_WINDOWS_LAB/evidence/w12-clean-gates-c77ed9a/W12-clean-gates-c77ed9a-02-evidence.tar`,
SHA-256 `a24fd67c68af5238ab98f09aea1d229755b41484ac627786d20b74e7386b819d`.
Guest and host archive hashes match. An earlier wrapper stopped before tests
because source and evidence paths collided under Windows case folding; a
distinct source/evidence pair was used. The first collector refused the valid
seven-stage JSON because PowerShell 5 wrapped its array; only the collector was
corrected, with no test rerun or modification of gate inputs/results.

No tests ran on the workstation and its proxy/network configuration was not
changed. These results advance W12; the full Windows acceptance remains open.

### W11 — complete interrupted server service setup (2026-10-11)

Baseline `c77ed9a`, Windows packaging-only change. After verifying the copied
payload, setup now commits a protected pending receipt before SCM creation.
Re-entry with the same package/bootstrap validates the full installed image,
existing service ownership and retained uninstall-receipt hashes. It then creates
missing services, applies existing server SCM policy and completes startup.
It does not overwrite a damaged payload or adopt a foreign service. Normal
maintenance/update/uninstall cannot treat an unfinished setup as completed.

The completed installation record keeps the existing schema and removes all
setup-only fields. Retained uninstall receipts are consumed after verified
startup; an interrupted final `.new` record is preserved under a protected unique
name rather than silently erased. An already-running server is not restarted.
No Go, wire, M11, ICE/AWG, TunnelLease or network selection code changed.

On the secondary Windows VM, `W11-server-pending-native-02` passed six injected
interruption points: verified record, kernel service, recovery service, main
service, SCM configuration and already-started daemon. The 32 assertions include
wrong-package/bootstrap refusal, corrupted-payload refusal before SCM mutation,
foreign service-command refusal, preserved incomplete-journal bytes, stable
identity/bootstrap, owned TCP/4000 and UDP/3478/51820, repeat-setup stable PID and
native cleanup after every iteration. The hooks throw controlled exceptions;
this is not arbitrary process-kill or power-loss coverage.

An earlier attempt `W11-server-pending-native-01` completed the first three
points, then failed with sc.exe 1639 when its harness tried to inject a foreign
command containing quoted spaces. The fixture now changes only the stopped,
disabled disposable service with the documented
[Win32_Service.Change method](https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/change-method-in-class-win32-service)
and verifies the exact command. The prior owned pending state was completed
through the normal backend and gracefully cleaned before the full six-point
retry. Original failure evidence remains preserved; no Core defect is inferred
from this harness quoting error.

The rebuilt development EXE embeds the verified current backend scripts.
`W11-server-pending-exe-native-01` independently passed completion after main
service creation and after daemon startup, with retained identity/bootstrap,
stable running PID, repeated setup and native cleanup. It uses the actual
embedded EXE's unattended development child path, not mocked services. It does
not establish interactive UAC acceptance or Authenticode release acceptance.

Development setup SHA-256:
`7c20485cfa8befc86739f1bb1e0496318c0315d770b1556c8b848efa0a883346`.
Embedded maintenance-fixture payload manifest SHA-256:
`5f6b4c08000898851e93df9309037a223fe15108e2af8e8a3e921ab99f243484`.
The payload is the existing server candidate A, not a claim of a refreshed final
release bundle. The EXE build and embedded-bundle verification passed natively.

The affected isolated suites passed **82/82** assertions: pending setup 25,
retained server profile 8, server installation 18, uninstall role isolation 16,
maintenance records 9 and update records 6. These run against disposable fixture
paths; the six-point and EXE tests above separately exercised canonical SCM.
Final secondary-VM state: IRVIO Stopped/Disabled, PID 0, exit 0; recovery
Stopped/Manual, PID 0, exit 1077 (never started). No daemon or Go test remained.

Allowlisted evidence:
`IRVIO_WINDOWS_LAB/evidence/w11-server-pending/W11-server-pending-evidence-01.tar`,
SHA-256 `3cef1e0edd804e32116767c78ae801cfd32344967894442a9d809b84d2e0a6a4`.
Guest/host archive hashes, internal file manifest and tested backend/harness
source hashes match. It retains the original harness failure and excludes all
real profile, key, bootstrap and executable payload data. No Linux runtime code
changed; Windows tests/builds ran only on the VM, with host proxy untouched.

Remaining installer gates include early payload-copy/initial-record interruption,
desktop partial-install repair, actual power loss, active-session/downstream
maintenance, retained uninstaller/Apps & Features and release signing. SCM
Running and bound ports do not prove public M11 quorum or usable Windows server
Exit readiness. Overall Windows acceptance remains incomplete.

### W11 — resume an interrupted server payload copy (2026-10-11)

Baseline `38a62f4`, Windows packaging-only change. Before touching application
trees, setup now commits a protected `server-copy.json` binding the verified
payload, bootstrap hash, signing mode and any retained uninstall metadata.
Re-entry requires the same package/bootstrap, no services and no conflicting
lifecycle state. It checks partial-tree ownership before copying; unknown files,
directories or reparse points are not adopted. A completed file with mismatched
hash/size is refused rather than silently replaced.

Each destination uses a reserved `.irvio-install-part` name, verifies size/hash,
flushes the temporary copy and renames it into place. A partial temporary copy
can be rebuilt from the verified payload. After full installed-image validation,
the existing pending-service record is committed and the copy receipt is consumed.
Re-entry also handles interruption between those two operations. Source payload
files, retained identity/bootstrap and runtime protection are not modified.

The first native attempt caught a real defect in this new code: Copy-Item carries
the source's read-only attribute, and the subsequent read/write open for Flush
failed on a Go dependency LICENSE file. The source and temporary file had the
same SHA-256, length 1,479 and attributes 33; its SYSTEM/Administrators-only ACL
was valid. The fix clears read-only only on the owned temporary copy, never on
the source. The retained interrupted installation then resumed successfully.

The 20 isolated copy assertions passed, including read-only sources and temporary
files, partial-copy publication, retry/idempotence, completed-file tampering,
foreign files/empty directories, package/bootstrap/signing-mode mismatch,
installation-record conflicts and reserved suffix rejection.

`W11-server-copy-native-02` completed nine points with 48 assertions: copy intent,
partial file, first published file, completed files, initial installation record,
pending-service record, kernel service, recovery service and main service. It
then stopped in a harness assertion that incorrectly counted all old preserved
journal fragments as newly created evidence. Product re-entry had correctly
preserved the previous fragments. The assertion now compares names/hashes before
and after: exactly one new fragment must contain the injected bytes, and every
previous fragment must remain unchanged.

After graceful cleanup of that terminal test's server, native-03 passed 15
assertions across completed-copy, configured-service and already-running-service
checkpoints. Together these jobs cover all 11 distinct interruption points;
this is not a claim that the first combined job passed uninterrupted. Their
checks include stable identity/bootstrap, fixed server sockets, repeated-setup
stable PID and native cleanup. The original failures are retained as evidence.
The queued followup-01 and fresh-01 jobs correctly refused to mutate canonical
state after their prerequisite failed; their successors use new evidence paths.

Followup-02 passed a real forced installer-process termination: the harness
intercepted only the first daemon Copy-Item operation, flushed 64 bytes into its
temporary file, held the file open and waited. The parent killed that exact child
(exit -1). No final daemon file or SCM service existed. The normal embedded EXE
then resumed the copy, installed/started the server, retained identity/bootstrap
and passed native cleanup. The barrier is harness-only; no production sleep,
daemon termination, timeout change or new network state was introduced.

That followup also passed the 102 isolated copy/setup/ownership/maintenance/update
assertions and the two real EXE service-stage resume checks. These are Windows
VM executions, not workstation tests. The EXE is a development build using the
existing candidate-A payload, not a signed release or refreshed final package.
Setup SHA-256:
`d8f5a53a4261aa5713da7c8ee429afa09d0c48d44d3985814f85295f75896700`.

Fresh-02 independently removed the owned installation while retaining its profile
outside canonical paths, verified absent application directories/services and
installed the real EXE from scratch. All six existing fresh-server assertions
passed: 213 installed files, automatic SCM startup, daemon-owned fixed ports,
desktop/server role isolation, idempotent setup, identity-preserving restart and
exact route/DNS/adapter/firewall cleanup (some assertions cover multiple facts).
The new identity differed from the retained one. Only that generated test profile
was explicitly erased; the original lab profile/bootstrap were restored through
normal reinstall and independently hash-verified. No private retained-state tree
is included in exported evidence.

All jobs are terminal. Final IRVIO is Stopped/Disabled, PID 0, exit 0; recovery is
Stopped/Manual, PID 0, exit 1077. Native maintenance cleanup passed after restoring
the original installation. Allowlisted evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w11-server-copy/W11-server-copy-evidence-01.tar`,
SHA-256 `269a3c5db5a9464883d235daee0f0b02f878b9f56b7d48062dcb87c01f6880a7`.
Guest/host archive hashes, the internal file manifest, tested source hashes and all
14 embedded installer backend hashes match the worktree.

The copy journal must already be committed: interruption during its initial
creation still requires inspection. Actual power loss, desktop partial-install
repair, active-session/downstream maintenance, Apps & Features and release signing
remain separate gates. Bound ports/SCM Running are not public M11/GeoIP/Exit or
second-client acceptance. No Core or Linux runtime code changed, no workstation
tests ran and its proxy/network settings were untouched. Full Windows acceptance
remains incomplete.

### W09/W11 — packaged UI app-local loader proof (2026-10-11)

Added `tools/windows/w09/ui-payload-load.ps1`, independently of the pending
decision about removing interrupted-installation recovery handlers. No product
code, installer state machine or workstation network settings changed.

On the secondary Windows VM, the verified candidate-A manifest
`5f6b4c08000898851e93df9309037a223fe15108e2af8e8a3e921ab99f243484`
supplied the real Flutter UI, bridge and app-local runtime. The harness copied
only its UI files to an isolated directory and verified their hashes. With PATH
restricted to Windows/System32 and Windows, the loaded module table placed
`msvcp140.dll`, `vcruntime140.dll`, `vcruntime140_1.dll` and
`flutter_windows.dll` inside that copied package, not the system/runtime/SDK
directories. This is an existing development candidate, not a final-source
release build.

The first SYSTEM/Session-0 attempt proved loading only; Flutter reported surface
creation errors in that noninteractive session. A limited-token interactive
attempt refused before launching because its CIM preflight did not see the
protected server installation. The harness did not loosen service ACLs.
The elevated interactive repeat (Session 1) passed module-path checks, created
a window, remained alive through the observation interval and reported Impeller
initialization without the Session-0 rendering errors. Its result explicitly
records elevation; this is not ordinary-user activation or full visual UX proof.

After the test, the owned UI/bridge processes were absent, IRVIO remained
Stopped/Disabled and recovery remained Stopped/Manual, both PID 0. The temporary
scheduled tasks were removed after completion. No connection was attempted.
Both successful loader results and the refused preflight remain in the archive.

Allowlisted evidence:
`IRVIO_WINDOWS_LAB/evidence/w09-ui-app-local/W09-ui-app-local-evidence-01.tar`,
SHA-256 `66ca50620d236f56f87e9cbbea6e245bddbe8034f59bd386a2abba728df389c8`.
Guest/host archive hashes, its internal file manifest and both tested source
hashes match. No profile, identity, bootstrap, payload binaries or raw private
state is exported. The export helper itself returned exit 1 after its final
no-process Get-Process lookup; the archive and loader verdicts were independently
verified rather than treating that helper's status as a product failure.

The package no longer merely assumes these DLLs will load app-locally: their
actual provenance is observed. Clean-host installation, non-elevated packaged
activation, signing and the full Windows acceptance matrix remain separate
gates. Interrupted-installation recovery changes were not altered in this task.

### W11 — connected update exposed and fixed recovery-service ordering (2026-10-11)

Baseline `894ca0f`; only the maintenance service-stop ordering changed. The
uncommitted partial-desktop-install feature is excluded from the frozen test
scripts and this fix. No Core, WFP security check, protocol or Linux runtime
changed. Tests ran on the secondary Windows VM, not the workstation.

The original connected-update gate established Global CLIENT+EXIT intent to NL,
downloaded 65,536 bytes through the tunnel and blocked the physical-source HTTPS
probe. The old installer then disabled both IRVIO and IRVIO-Recovery before
stopping the daemon. Teardown returned the existing WFP error requiring automatic
recovery while persistent filters exist. SCM reported exit 1066, service-specific
exit 1; a teardown marker remained. The updater correctly refused replacement
and retained its maintenance fence. This is a confirmed installer lifecycle bug,
not a discovery failure or justification to relax WFP validation.

`Stop-IRVIOForMaintenance` now disables and gracefully stops IRVIO first, checks
successful completion, and only then disables/stops recovery. Failure before that
point leaves recovery in its original automatic mode and preserves the journal.
Two isolated regressions enforce exact ordering and failed-stop behavior.

The failed VM state was recovered by restoring recorded SCM modes and using the
real daemon's Disconnect/Shutdown path. No protection, intent or route was erased
manually. Original failure logs/state were preserved before retry.

Native affected checks passed: stop-order fixtures **2/2**, existing maintenance
suite **15/15**, clean-state/refusal suite **8/8**. The real connected-update retry
passed all **5** grouped assertions:

- Existing client connected to NL, user transfer succeeded and physical-source
  user traffic was blocked before update.
- Normal updater stopped the active daemon cleanly, replaced the payload and
  preserved PeerID/AWG identity and authorized desktop SID. Replacement took
  40,266 ms, including package checks/staging/teardown; this is not tunnel latency.
- Read-only native cleanup and exact route/DNS/adapter baseline comparison passed;
  ordinary WAN worked again.
- Updated daemon started DISCONNECTED, retained bootstrap configuration, did not
  auto-connect, and connected with real user traffic after an explicit request.
- Final Disconnect/maintenance cleanup returned native policy and host network
  state to baseline; ordinary WAN worked.

Old payload manifest:
`5f6b4c08000898851e93df9309037a223fe15108e2af8e8a3e921ab99f243484`.
Updated development payload manifest:
`7ec6a406501724599cdb691a456e2e00dcf7735950e728503447d10a844fcf49`.
Updated daemon is the previously native-validated c77ed9a trimpath build,
SHA-256 `9c3caa707d5cf10566e412c4e0851f26ed28d87eb42ed2506b2c48cfebd162f1`.
Its UI/dependency files were reused from candidate A, with the production staging
validator and signatures; this is not a new signed release or full-source UI
provenance claim. The updated installer backend was invoked directly, not through
a newly rebuilt embedded EXE.

The original server installation was retained outside canonical paths throughout
the desktop test, restored through its normal installer and independently checked
for identity/bootstrap equality. Only the generated desktop test profile was
erased. Final services: IRVIO Stopped/Disabled/exit 0; recovery Stopped/Manual/exit
1077, both PID 0; no UI/bridge/daemon process remains. Private backup/removal trees
are not exported.

Evidence:
`IRVIO_WINDOWS_LAB/evidence/w11-connected-update/W11-connected-update-evidence-01.tar`,
SHA-256 `cd3b9db1829b7d87305cc0f90800b282c80e8d02f9b97bb432bef856476c8ca8`.
Guest/host archive hashes and all 36 manifest entries match; fixed helper/test
hashes match the source tree. Original failure events and the successful retry
are both retained. Active downstream leases, continuous teardown packet capture,
embedded-EXE repetition and other full-port gates remain unproven by this test.

### W11 — embedded EXE update with a live downstream client (2026-10-11)

Frozen installer source `b093883`; no further production code changed. A new
native development EXE embeds the fixed maintenance helper (SHA-256
`e2c58793cf9a19df7d172f22f4a086b0ed8a6c9c89596d89682b8cb92ec9f328`).
All 14 backend hashes are recorded in build metadata; bundle self-validation
passed. EXE SHA-256:
`9d182c8e918b3467b9e458525d25878be77159b88aa330b0a0a898297dfd01a2`.
Payload remains the c77ed9a daemon plus reused candidate-A UI/dependencies,
manifest `7ec6a406501724599cdb691a456e2e00dcf7735950e728503447d10a844fcf49`.
This is an unsigned development installer, not a release/signing acceptance.

The secondary VM's temporary desktop hosted Private VPN. The primary VM imported
a real one-use invitation, enrolled through the network, connected, downloaded
65,536 bytes and rejected a physical-source user probe. The host independently
reported one active client and 30,607 shared upload bytes. Both VMs use the
existing shared lab underlay; this is not a restrictive/mobile-NAT test.

After independent client proof, the host invoked the actual embedded EXE's
unattended elevated install/update path. It completed in 51,016 ms, preserved
host identity and the count of private memberships, left the daemon stopped,
and passed native clean-state and exact route/DNS/adapter comparison. Starting
the updated daemon produced DISCONNECTED, hosting false and zero clients; it
did not resume sharing automatically. Membership-count equality alone does not
prove that the client can reconnect later without another invitation.

Host side: **PASS** for this update/cleanup gate. It does not simultaneously
exercise an own upstream and downstream load, multiple downstream leases,
interactive UAC, or a continuous packet-capture oracle.

Client side: **FAIL at the final ordinary-WAN DNS probe; unresolved observation**.
The client reached NO_EXIT with private target intent after the host stopped.
Both ordinary and physical-source HTTPS attempts then timed out with zero bytes,
consistent with fail-closed behavior and no public-Exit fallback. Explicit
Disconnect/Shutdown completed, the disposable saved target was removed while
the previous saved-target count remained unchanged, services stopped with exit 0
and route/DNS/adapter deltas were empty. However, the immediate final curl failed
with exit 6: `Could not resolve host: speed.cloudflare.com`.

A later read-only followup found no cache-only A answer, obtained real A answers
from the configured QEMU DNS forwarder at 10.0.2.3, then downloaded all 65,536
bytes. `TestWindowsNativeProductIdleCleanup` also passed: no protection/intent or
active network journal residue. Nothing was manually flushed or removed to make
that followup pass. It does not explain the earlier failure: DNS cache/suppression,
upstream resolver timing and the older primary daemon remain hypotheses.
The original failed gate is not replaced by the successful later probe.

Primary client daemon SHA-256:
`05e677f1aca664743d21aa93356ad828bf6f710774518c2f9bfcf30fe1230d38`.
Next required diagnostic is a bounded reproduction using the current daemon,
with immediate resolver/cache/packet observations around NO_EXIT → Disconnect.
Do not add blind retries, loosen protection or claim a DNS fix without that
evidence. Full two-sided maintenance acceptance remains open.

The secondary VM's original server identity/bootstrap were restored and verified;
only its generated desktop-host profile was erased. Both VMs are stopped and
clean. Generated invitation transfer copies were removed from both guests and
the host temporary directory; none entered Git or exported evidence.

Allowlisted archives under `IRVIO_WINDOWS_LAB/evidence/w11-hosting-update/`:

- `W11-hosting-update-evidence-01.tar`: SHA-256
  `b903bec8b4f81bc586a530cb42caf13834ee88169b9d68e4a07893d3d9f4afbf`;
  15 manifest entries verified.
- `W11-hosting-client-evidence-01.tar`: SHA-256
  `35f913c000b63a83f0dd77a3926b5ef16c689fcd8fba9de93ee1b6efbcb956d9`;
  20 manifest entries verified, including the original failure and later followup.

Guest/host archive and tested harness hashes agree. No workstation tests or proxy
changes, no external deployment, and no whole-port PASS is claimed.

### W11/W12 — current-daemon downstream-update DNS repeat (2026-10-11)

Repeated the two-VM hosting-update experiment with the current clean-source
`c77ed9a` client daemon, SHA-256
`9c3caa707d5cf10566e412c4e0851f26ed28d87eb42ed2506b2c48cfebd162f1`.
The host again ran the actual embedded installer built from `b093883`, SHA-256
`9d182c8e918b3467b9e458525d25878be77159b88aa330b0a0a898297dfd01a2`.
No production DNS change or cache flush was made. Only the client lab helper
gained bounded optional DNS observations and monotonic timing markers.

Both sides passed this repeat. The host observed one active downstream client
and shared upload before update, completed replacement in 50,625 ms, returned
IDLE with no downstream clients, and passed native cleanup plus exact network
baseline comparison. That installer duration includes validation/copy/cleanup;
it is not a connection latency. Membership preservation here checks a count,
not a subsequent reconnection proof.

The client transferred 65,536 bytes through the private host before update.
With the host unavailable it retained private intent in NO_EXIT and both the
ordinary and explicitly physical-bound transfer probes failed with zero bytes.
Explicit Disconnect completed in 1,050 ms; the first ordinary-WAN HTTPS probe
after cleanup succeeded immediately, transferring 65,536 bytes in 217 ms.
Matching DNS-cache observations before/after Disconnect were empty. Exact
DNS/routes/adapters comparison and the separate native product idle-cleanup
gate passed. The original failed run remains a failure: its DNS cause is
**INCONCLUSIVE / not reproduced**, not fixed or proven to be a lab artifact.
The two client binaries also differ in build settings, so this is not a
controlled single-variable causal comparison.

External QEMU captures used a 256-byte snap length on the primary net0 and
isolated echo-oracle interfaces. In net0, the final primary-client A query for
the probe hostname went to its restored DHCP resolver (10.0.2.3), followed by
a successful two-address answer 7.970 ms later within the same capture clock.
No DNS packet for that hostname was found in the echo-oracle capture. The hub
capture also contains peer traffic, so analysis filtered the primary address.
Guest UTC and host capture clocks differ; no cross-clock subtraction supports
the timing claims above. These bounded captures are diagnostic evidence, not
a new exhaustive zero-leak proof; the harness verdict correctly does not claim
continuous packet capture. Raw captures remain private outside Git:

- `primary-private.pcap`: SHA-256
  `59ae207502055b5c1302ffeafaa1a913ace8de8b40ff2ba0f6143a8690d1b3f1`.
- `oracle-private.pcap`: SHA-256
  `615b36d88ab5e59eaca680e5d9204c1a5ee4151d1d81488709776053e5161d95`.

Allowlisted archives in `IRVIO_WINDOWS_LAB/evidence/w12-dns-disconnect-01/`:

- `W12-dns-client-evidence-02.tar`: SHA-256
  `f9ef1fa78d09c76f0b05672f455bb5b014620c070119cfdc56496958f417daed`;
  18 internal manifest entries verified.
- `W12-dns-host-evidence-02.tar`: SHA-256
  `e2f7d6a3b63d0e9e63e18be720abab811f2c6d5d216b28248f8a6dfef37773a6`;
  13 internal manifest entries verified.

The tested client-helper SHA-256 matches the source:
`50b12858aabbf217d08875071260bbc83a1977bdd8c793fb8749efcd9281bb19`.
Exports exclude invitations, profiles, binaries and raw captures. Generated
invitation transfer files were removed from both guests and the host temporary
directory. Both capture objects were removed. The primary original daemon and
its ACL were restored after native cleanup; the peer original server identity
and bootstrap were restored, with only the temporary host profile erased.
IRVIO services are stopped: primary Manual/recovery Auto; peer Disabled/recovery
Manual. No workstation networking, proxy or production deployment changed.

One current-daemon two-sided maintenance repeat is PASS. Multiple downstreams
with an active own upstream, the unexplained earlier DNS transient, original
rare bind rejection, and the remaining full-port acceptance gates stay open.

### W09/W11 — installed UI ordinary-token activation (2026-10-11)

The secondary Windows 11 Pro VM's stopped server installation was removed with
profile retention, its protected state moved aside, and the actual development
EXE installed a fresh desktop into the canonical Program Files/ProgramData paths.
The same `b093883` EXE and candidate-current manifest from the preceding gate
were used. This is the real embedded installation backend via its authorized
unattended development entry, not another interactive UAC acceptance test.
Installed services remained stopped until the UI launch.

New `tools/windows/w09/installed-user-launch.ps1` ran via a Limited interactive
scheduled task as IrvioLab, Session 1. The token's Administrator membership
check was false. IrvioLab is an administrator account with a filtered token,
not a separately provisioned standard account; existing two-account bridge
coverage remains separate. The helper checked the installed Start Menu shortcut
and launched its installed target with PATH limited to Windows directories.
MSVC runtime DLLs and Flutter loaded from `Program Files\IRVIO\ui`.

Four launches independently activated the real zero-argument daemon, remained
DISCONNECTED, and reported bootstrap configured=0/reachable=0. The elevated
coordinator only observed activation: it did not pre-start the service or alter
its DACL for the test. UI termination removed its bridge but left the same daemon
PID running. API Shutdown and fenced native cleanup passed after each launch.
No bootstrap was imported, no connection requested, and no network session is
claimed by this UI gate.

Visual inspection prevented a false broad PASS. The first three guest screenshots
showed a white window despite a running Flutter engine and functioning daemon
activation. A simultaneous QEMU framebuffer capture was completely black: the
guest display was asleep. A Shift key sent to this VM woke its display without
changing host or guest power policy. A fourth launch of the unchanged binaries
rendered the actual `Join IRVIO network` page, bootstrap text/QR inputs, sidebar
and Disconnected state; both QEMU and guest screenshots were inspected. Its
window appeared in 1,608 ms. This establishes the awake-display launch, not an
existing-window resume/hibernate proof. No renderer workaround was introduced.
The helper's machine PASS deliberately covers token/loading/lifecycle only;
rendered UI acceptance still requires visual inspection.

The final tested helper SHA-256 is
`f8db9afda4c615cf4afc42691f3775566ae26d9fe5706762bddbcc3554f7b831`.
Attempts 2–4 used its ten-second delayed screenshot; attempt 1 preceded that
diagnostic delay. Candidate-current contains the previously verified packaged
UI and current daemon; this is not a new final-source Flutter release build.

The original server identity/bootstrap were restored and verified after removing
only the generated desktop profile. Final IRVIO is Stopped/Disabled; recovery is
Stopped/Manual. Temporary scheduled tasks and UI/helper/daemon processes are
absent. The primary VM and workstation networking/proxy were not changed.
Full Home/Pro, actual standard-account installation, interactive uninstall UX,
power-transition, signing and final packaged-release gates remain open.

Allowlisted evidence:
`IRVIO_WINDOWS_LAB/evidence/w09-installed-user/W09-installed-user-evidence-01.tar`,
SHA-256 `5de6368830aedac911f93f19f8d5704f4ff07df25c68d9470e04374b1dbf4eee`.
Guest/host archive hashes and all 45 internal manifest entries were verified.
It contains token/window/module evidence, four guest captures, API observations,
native cleanup and restoration results, but no retained profiles or identities.
The successful guest capture was taken 17,336 ms after launch. The independent
QEMU capture `irvio-user-qmp-04.png` has SHA-256
`c525624dab3a8c0f5ec061575916c491373785182b31d71821367a69e6d2edbe`.
Earlier screen-off captures remain preserved; they are not silently replaced
by the successful awake-display image. No full Windows-port PASS is claimed.

### W08/W11 — fresh-source package and installed hibernate resume (2026-10-11)

Built the complete Windows daemon/UI/bridge and embedded development EXE from
clean Git archive `c1a71e00bf3657632e39a9e5520f8f38d804b5ae` on
IRVIO-Win11-Peer. Go 1.27.1 windows/amd64 and Flutter 3.47.4 (9584c6713b,
Dart 3.13.3) were used. Native daemon build, locked offline pub resolution,
Flutter analyze/tests/release build, pinned dependency staging and the embedded
EXE bundle check passed. The existing signed driver/MSVC dependencies, MMDB and
license inventory were reused; dependency manifests were unchanged from their
licensed payload baseline. This does not claim a signed IRVIO release.

The handoff directory is `/home/ubuntu/Work/IRVIO_WINDOWS_BUILD`; the VM build
directory is `C:\IRVIO-Lab\IRVIO-Windows-c1a71e0`. It contains a development
EXE installer, not MSI, plus clean sources, payload and installation/build
instructions. The exported payload's exact 507-file inventory and every hash
were independently verified on the host. No lab profile or identity is included.

- Source archive SHA-256:
  `24049c7576caa27d5aa34b165481963c8a036a22a4b3073b39caf5d01a78bb10`.
- EXE installer SHA-256:
  `e937f6ba0a27cfb3eec4cf3b838c2df4098734b3ed466ac24bd5db7f87e145d1`.
- Daemon SHA-256:
  `2c2b1a744a7cff2d7582287cb53392c7f7c3507aaa0dd77bad058f7a5ce8204f`.
- UI SHA-256:
  `67be6154b7ff6081dc6fcad6cd1cd31ad09d1471e0e2cf0451bd61c7088a451f`.
- Bridge SHA-256:
  `a1a30554ee7e23798e88a1c2b7af3f3192ef1f6e1c2695cd54de5f3b229c4171`.
- Payload manifest SHA-256:
  `267093d631a166576075f007c4682e8b8d831842481ea5ced4d83140abd7cca7`.
- Exported payload ZIP SHA-256:
  `9b08e4aae0adfff407d455ebebf4af8fc9c33e065599cf43b2d53115337c8173`.

The actual new embedded EXE installed a fresh desktop through its authorized
unattended development entry. The peer's original stopped server installation
was retained privately. New `tools/windows/w08/installed-hibernate.ps1`, tested
SHA-256 `19f03d1e9dbbb55baaea77d7e9cb81515eb7c4d1dbce915107211e3eaef2b98f`,
operated on canonical Program Files/ProgramData paths. Prepare observed the
service initially stopped, imported the original network bootstrap, requested
Global NL, reached CONNECTED and transferred 65,536 bytes. Its 4,272 ms
connection wait starts after the Connect API call returns; it is not the full
user-visible Connect latency.

The coordinator rechecked CONNECTED and issued `shutdown /h`. The peer QEMU
unit became inactive and its QMP/guest-agent sockets disappeared. The existing
shared-lab launcher restarted only the inactive peer VM/TPM. On resume,
Kernel-Boot event 27 reported BootType 2 with a newer record ID. Both SCM and
daemon retained their PID plus creation timestamp, proving preservation rather
than fresh service restart. Verify sent no Connect. Its first status observation
was already CONNECTED in 41 ms; this is not time from hardware wake or a measured
blackout duration. The post-resume transfer succeeded with 65,536 bytes in
389 ms. Three targeted physical-source HTTPS probes (before hibernate, after
resume, after successful tunnel traffic) all failed with zero transferred bytes.

Explicit Disconnect/Shutdown then restored the exact baseline routes, DNS and
adapter identities. Ordinary physical-bound HTTPS transferred 65,536 bytes in
159 ms; the fenced native maintenance probe found core state clean, no owned
adapters and no firewall rules. The harness verdict is PASS for this installed
desktop hibernate scenario, with `continuous_leak_capture=false`. It does not
prove no packets leaked during every transition interval, GUI resume rendering,
downstream survival, physical sleep/Modern Standby or Fast Startup.

Earlier evidence remains intact. The first installed-hibernate attempt using
the older candidate-current payload completed real resume/traffic but its final
PowerShell comparison wrapped a decoded JSON array as one object and failed.
A read-only follow-up found exact 25/25 baseline equality; the harness now
assigns decoded baseline to a variable before Compare-Object. Its prepared
repeat was cancelled before a second hibernation to prepare the requested
handoff package, with clean Disconnect/Shutdown and restored original fixture.
Neither that cancellation nor the earlier comparison failure is counted as
the new package's passing run. No production lifecycle change was made here.

Restoration initially called the uninstall backend with the fresh build's
user-writable payload directory. Its protected-probe ACL check correctly refused
that lab invocation, retaining the prepared uninstall journal. The coordinator
then used the actual EXE's same-user, same-profile-choice unattended uninstall,
which extracts into protected staging and completed the existing journal. No
ACL check or production code was relaxed. The generated desktop profile was
erased; the original server identity/bootstrap were restored and verified, with
a clean native probe. Final IRVIO is Stopped/Disabled and recovery Stopped/Manual;
no daemon/UI/bridge process remains. Workstation proxy/networking were untouched.

Allowlisted evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-hibernate/W08-installed-hibernate-evidence-03.tar`.
SHA-256 `37a8bab87d11c2e8593d82e80d68a2c6265826c16cfa54f1d426820ff4303609`;
all 19 internal manifest files and exact archive inventory were verified.
Profiles, bootstrap, keys and raw daemon logs remain outside the exported archive.
Full Windows-port acceptance remains open, including continuous transition-leak
coverage, Fast Startup, physical-device/edition matrix and release signing.

### W06/W12 — installed CLIENT+EXIT Disconnect with five leases (2026-10-13)

After the workstation reboot, both existing Windows 11 Pro VMs were restarted
without restoring snapshots or reinstalling IRVIO. Both main services remained
stopped/demand-start and boot recovery completed. The installed peer daemon is
the unchanged `c1a71e0` package, SHA-256
`2c2b1a744a7cff2d7582287cb53392c7f7c3507aaa0dd77bad058f7a5ce8204f`.
The original server profile remains protected in the earlier retained-state
directory; the desktop installation is deliberately kept for subsequent network
tests. Per the owner's updated priority, do not repeat installer campaigns for
unchanged packages: focus on runtime gaps, rerunning packaging checks only when
affected by changes or needed to repair an actual fixture problem.

Restoration found a lab-only DHCP ordering change: primary and peer received
10.0.2.16 and 10.0.2.15 respectively, reversing their old assignments. Guest-agent
MAC/IP observations identified each VM. The two localhost QEMU SSH forwards were
updated to the correct guests; no workstation routes, proxy, host firewall or
production IRVIO setting changed. The first repeat's physical-source probe used
the old address and failed before starting IRVIO. Its evidence is retained,
not counted as a network failure. The next repeat used the observed peer source.

`installed-downstream-disconnect.ps1` now tests the canonical installed desktop,
while `product-five-clients.sh` gains opt-in `IRVIO_W5_HOST_DISCONNECT=1`.
Five fresh client-only production Linux daemons ran in separate DHCP/macvlan
namespaces on the mini-PC, using the existing public bootstrap/discovery path.
They were not manually given Windows Exit endpoints. As in earlier five-client
tests, their isolated DNS launcher does not prove five native desktop DNS
lifecycles. The Linux installed daemon stayed DISCONNECTED during the test.

The Windows host connected to NL with contribution enabled; all five clients
selected its RU Exit. Ten full 1 MiB downloads and five full 1 MiB uploads
succeeded. All ten downstream egress observations matched the host's physical
egress, distinct from its NL upstream. Fifty Windows observations retained the
same own-session timestamp, CONNECTED/contributing state and upstream egress.
The host observed five current downstream clients before the coordinator
authorized Disconnect; Linux clients had not sent RELEASE yet.

Disconnect returned in **1,452 ms**. Its immediate status was DISCONNECTED,
contribution false, clients zero and completed inactive traffic counters.
Proto3/Go JSON omits `active=false`; the harness checks the optional property
without treating absence as a PowerShell StrictMode exception. Final shared
upload was 5,796,027 bytes, including plaintext protocol overhead. Exact Windows
DNS/routes/adapter comparison matched the pre-Connect baseline before Shutdown;
physical egress was restored. Shutdown and the independent native maintenance
cleanup probe passed. Services ended Stopped/Manual and Stopped/Auto respectively.

After confirmed host teardown, all five physical-source client HTTPS probes
failed with zero transferred bytes. Explicit client Disconnect and harness
cleanup then passed, with identical mini-PC host routes/DNS/nftables and no
fixture namespaces remaining (only its pre-existing management namespace).
The Linux systemd test unit ended with exit 0. Targeted probes do not establish
continuous zero leakage throughout the transition, and Global clients are not
forbidden from discovering a different legitimate Exit after host loss.

For transparency, the first run on October 11 had transferred the same probe
counts and observed host Disconnect in 1,486 ms, but the harness failed on the
omitted false boolean. Its later follow-up found no Local API pipe; that run's
final cleanup was not proved and its Linux coordinator timed out. The reboot
cleanup and current clean repeat do not retroactively turn that run into PASS
or identify why its pipe was absent. No production change was made for it.

Download timings in the successful repeat: min 1.222 s, median 6.125 s,
max 7.317 s. Upload timings: min 0.704 s, median 7.431 s, max 8.211 s.
These Internet probes include DNS/connection overhead; they are not a capacity
benchmark or proof that the previously observed DNS tail has disappeared.

Tested source SHA-256:

- Windows helper: `fd7b991c2afaac3d6fa38b8dad89ba5c2184c553581e0cb87019ba3992318c81`.
- Linux helper: `c3b22f4e4b8462d19cf05eb6eeeadb3c2bf611c103f36fbfe6a7689da14b3297`.
- Linux daemon: `d9a9dba106e2def44cb4c963ecdc4e8e77b9c5f6334b10ae876680e05eeb85f8`.

Allowlisted evidence in `IRVIO_WINDOWS_LAB/evidence/w12-installed-five/`:

- `W12-five-host-evidence-03.tar`, SHA-256
  `92065ddf67f53441a99e4cf8a31225d6b21c1509bdbb20214a07fca48c1874ce`;
  nine internal manifest files verified.
- `irvio-five-disconnect-export-02.tar`, SHA-256
  `43a2a37c2614a43d578539c37a3517080bd1784e6cb8fb124d9f402a86c7d525`;
  70 internal manifest files verified.

Exact archive inventories and the cross-host egress/session assertions were
verified independently. No keys, bootstrap, profiles, invites or raw daemon logs
are exported. This installed five-lease teardown gate is PASS, not full Windows
acceptance or remote-distributor withdrawal proof. No installer was run today.

### W08 — installed desktop adapter loss/return (2026-10-13)

The same installed `c1a71e0` desktop on the peer Windows 11 Pro VM was used,
without installation, binary replacement, or workstation networking changes.
`tools/windows/w08/installed-network-loss.ps1` drives the test through the
out-of-band guest agent. It resolves the physical IPv4 by the exact lab adapter
MAC, requires a stopped installation and a new protected evidence directory,
and re-enables the adapter in `finally` if an observation fails during the outage.

The first attempt, `network-loss-01`, failed its initial 120-second connection
wait **before any adapter fault was injected**. Sanitized daemon events show
three ICE attempts failing (one context cancellation, then two context deadline
errors), separated by approximately 64–65 seconds, followed by successful ICE
at 201.078 seconds and TunnelLease BIND at 204.990 seconds from the first daemon
event. The subsequent coordinator status observed CONNECTED without another
Connect. Candidate requests and fresh SRFLX refreshes continued. This is a real
initial-connection long tail, not an adapter-recovery failure or a proven lab
artifact; its root cause remains unclassified. No timeout or suppression policy
was changed. Explicit Disconnect/Shutdown and the native cleanup probe passed;
the failed attempt and protected raw logs remain preserved.

The second attempt, `network-loss-02`, completed the intended runtime gate:

- Initial CONNECTED was observed at 11.154 seconds from harness start, followed
  by a complete 65,536-byte HTTPS transfer.
- The adapter was disabled at 11.902 seconds and enabled at 42.392 seconds.
  Status progressed through NO_NETWORK and RECOVERING with contribution intent
  retained, then returned to CONNECTED without another Connect request.
- Enable request to observed CONNECTED was **14,475 ms**; the subsequent complete
  65,536-byte transfer finished at **14,887 ms**. These include adapter/DHCP and
  observation/probe overhead, not a precise outage duration.
- The daemon PID and creation time were unchanged. The recorded fresh SRFLX
  event used transport generation 3 after return, versus 1 before the fault.
  Fresh ICE, data-plane installation and TunnelLease BIND followed.
- Physical-source probes before and after the recovered connection returned
  curl exit 7 with zero bytes. The immediate adapter-return probe returned
  exit 6 (DNS unavailable), also zero bytes: it **does not independently prove
  WFP enforcement during that interval**. No continuous packet capture was run.
- Disconnect returned IDLE with contribution false. After Shutdown, exact
  routes/DNS/adapter inventory matched the baseline; ordinary physical HTTPS
  and the native maintenance cleanup probe passed. Services were left stopped,
  with the existing installation retained for subsequent runtime work.

This is PASS for the observed installed-desktop adapter-loss/recovery scenario,
not full network-transition acceptance. Source/gateway replacement, changed NAT
mapping, continuous transition leak capture and the initial long tail remain
separate coverage/diagnostic items. No production code or installer changed.

Helper SHA-256:
`b09dee407856cfcb47327ae2e70ab36bbb7ca308b80b321370a0f45d1cb96156`.
Allowlisted archive:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-installed-network-loss-evidence-01.tar`,
SHA-256 `a515b4d657717e48c96121b4dd02a1803718d9b8522edd8279731b280b967783`.
All 21 internal manifest entries and archive inventory were independently
verified. Exported state/event timelines omit PeerIDs and contain no bootstrap,
profile, private key, invite or raw daemon log.

### W03/W08 — installed source migration exposes teardown failure (2026-10-13)

Continued on the unchanged installed `c1a71e0` peer desktop; no installer ran.
The adapter-loss helper now optionally replaces its DHCP IPv4 with a reserved
address in the isolated lab /24, preserving the gateway. It requires the old
address absent and the new one Preferred, records local daemon sockets, and
restores DHCP in `finally`. Physical-source probes now use an A-record resolved
before Connect with curl `--resolve`; DNS unavailability is no longer their oracle.

`source-change-01` recovered without another Connect: 12,854 ms to observed
CONNECTED and 13,234 ms through completion of a 65,536-byte HTTPS download.
Disconnect/Shutdown, exact route/DNS/adapter baseline and native cleanup passed.
This first version did not record the detailed source/socket snapshot. Its
successful cleanup must not be generalized to the following failure.

`source-change-02` independently recorded replacement of 10.0.2.15 by the sole
Preferred physical IPv4 10.0.2.45, gateway 10.0.2.2. After automatic recovery,
daemon TCP listeners and established connections used 10.0.2.45, the old address
was still absent, and a complete HTTPS transfer passed. Fresh SRFLX was observed
for transport generation 4 versus generation 1 initially. All three physical
probes with pinned DNS returned curl 7 and zero bytes; this remains targeted,
not continuous leak evidence.

After DHCP restoration, explicit Disconnect returned Local API INTERNAL and
left DISCONNECTING, contribution false, no own connection. A second explicit
Disconnect also failed. SCM Stop then stopped the process with Win32 exit 1066,
service-specific exit 1; the retained daemon log recorded:

`WINDOWS_SERVICE_FAILED: Windows underlay grants are owned or awaiting cleanup`

This is an **open Windows lifecycle defect/suspect**, not a passing migration
acceptance gate or an installer issue. The exact failing cleanup owner/race is
not yet established. No protection was manually removed to produce a clean
result. Restarting the same binary adopted the persisted teardown intent and
returned IDLE without reconnecting; explicit Shutdown and the native maintenance
cleanup probe then passed. That recovery does not erase the failed Disconnect.

To localize the next reproduction, desktop Disconnect now logs its returned
cause via the existing sanitized logger, and Windows daemon cleanup errors gain
stage context while preserving `errors.Is` through `%w`. No lifecycle ordering,
timeouts, network selection or security policy changed. The existing retained
owner regression now also asserts the diagnostic cause is present.
Native Windows `TestProductStartup` tests and runtime `Test.*(Close|Cleanup|Retry)`
tests passed, as did the diagnostic daemon build. The new executable is staged,
not deployed or claimed to fix this issue:
`C:\IRVIO-Lab\W08-source-diagnostic-01\irviod.exe`, SHA-256
`1351993282868e1fab9011fffa8492c81db4e36cb118afeadb8ce99ef21f168b`.

The earlier initial-connection long tail has a plausible timing mechanism in
the shared selector: default FastConnectTimeout 1.5 seconds, AvoidDuration 60
seconds and ProviderRetry 5 seconds (`internal/exitselector/config.go`). Failed
non-busy candidates enter epoch-bound suppression in `selector.go`; suppression
expires in `suppression.go`. This is consistent with the observed 64–65-second
gaps but does not prove why ICE first failed, identify the actual selected peer
in the sanitized trace, or establish a Windows-specific selection bug. No
policy changes were made based on that inference.

Allowlisted source-migration evidence:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-installed-source-change-evidence-01.tar`,
SHA-256 `405a844193ffc90c064e327177d119d1edb2e11acc023dca5a9bb454aa8350e4`;
all 30 internal entries and exact inventory independently verified. Raw logs,
profiles, identity keys and bootstrap remain in the protected VM directories.
Current harness SHA-256:
`6802f2508b6e4c81731d416fd597b913738d35d05fede2a81979c6af7d9f2cd3`.

### W03/W08 — canceled migration and retryable route cleanup (2026-10-13)

Commit `bc09655` fixes two deterministic Windows-only defects found while
investigating the preceding source-migration teardown failure:

1. `WindowsUnderlayOwner.Advance` changed generation and entered draining before
   a native policy replacement. Cancellation before commit, after commit, or
   during post-commit inspection could leave it dirty/draining even when a
   fresh cleanup operation would succeed. Other lanes correctly rejected
   updates with `ErrWindowsUnderlayBusy`, obstructing teardown. Advance now
   completes withdrawal of predecessor grants with an independent three-second
   cleanup context, preserving the original cancellation for the caller.
   Cleanup failure still retains ownership and the dirty/draining fence; no
   policy removal or permissive fallback is introduced.
2. `windowsClientRoutes.Close` cached its first error in `sync.Once`, so a later
   Close could not retry a resolved native failure. It now serializes cleanup,
   retains failed resources and does not repeat successful closes. The directory
   owner is released only after the dependent permission/DNS/routes close.

New native regressions were run **before** implementation changes: all three
canceled-Advance boundary cases failed, and all three route-cleanup retry cases
(permission, DNS, routes) failed. The retained-fence-on-cleanup-failure test
passed before and after the fix. Afterward, targeted cancellation/route tests
passed with `-shuffle=on -count=10`; complete ordinary tests of `killswitch`,
`runtime`, and `cmd/irviod` passed; those three packages also passed native
`-race -shuffle=on -count=5 -timeout=300s`. The changed implementation files are
Windows-only; no Linux behavior or protocol change was made.

The peer installation was updated in place first to the diagnostic build and
then the fix, preserving its profile and unchanged UI/drivers. No fresh install,
uninstall, MSI campaign or installer fault-injection matrix was run. Diagnostic
source migration 1 passed fully (13,959 ms to CONNECTED, 14,319 ms through the
user probe); diagnostic migration 2 hit curl 6 for the immediate post-recovery
DNS lookup. Follow-up explicit Disconnect/Shutdown on that run succeeded.

Fixed daemon SHA-256:
`b83182f4d581830872a7c8d75b32ad4a86b124a5c5415b75024e962210c5dda8`.
Its development payload manifest SHA-256:
`19e3488db985e96b281d975719b7fa54db0ef3a04d6c5a414b02331fafe2aba0`.
The first live fixed run also observed CONNECTED but failed its immediate
post-migration hostname lookup (curl 6, zero bytes, 136 ms); later explicit
Disconnect and Shutdown succeeded with service exit 0. This is **not** a full
PASS for the live migration gate. DNS recovery timing and the original rare
teardown race require integrated follow-up; deterministic regression fixes
alone do not prove every observed runtime failure eliminated.

Live fixed runs 2 and 3 subsequently passed the complete unchanged source-change
scenario: 14,407/14,392 ms to CONNECTED, 14,785/14,725 ms through complete HTTPS,
then immediate Disconnect after DHCP restoration, exact network baseline,
Shutdown and native cleanup PASS. The installed daemon stayed the same process
through each migration. The failed first run remains failed: a later resolver
query succeeded in 493 ms, with tunnel DNS 1.1.1.1 and physical DHCP DNS 10.0.2.3,
but that follow-up occurred after DHCP restoration and does not explain the
earlier lookup failure. Aggregate integrated result is two full PASS and one
DNS-observation failure, with no repeated underlay teardown failure in these
fixed attempts; this is not full Windows acceptance or proof against every
cleanup race. The peer is left on the fixed development package, stopped and
clean, without auto-connect. The workstation proxy/network was untouched.

Allowlisted evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-source-fix-evidence-01.tar`,
SHA-256 `458fc2d85d0843c973025aaae65a539fbaafbcbf41ca894576e15fa610a82e9e`.
All 84 manifest entries and the exact archive inventory were verified; native
hashes of all four changed Go files match the committed workstation sources.
It includes pre-fix failures, post-fix native/race results, bounded runtime
observations and cleanup evidence, but not keys/profiles/bootstrap or raw logs.

### W08 — resumed lab and bounded DNS follow-up (2026-10-13)

Both Windows 11 VMs are running and reachable through Guest Agent after the
workstation reboot. The peer retains the installed `bc09655` daemon above;
no installer, MSI, uninstall or package update was needed for this follow-up.
Workstation networking and proxy were not changed.

The installed network-loss harness now accepts optional `-DiagnoseDNS`. On a
post-migration curl DNS error (exit 6), it captures interface DNS, route to the
tunnel resolver, relevant cache entries, a pinned-address tunnel HTTPS probe,
system A/AAAA and explicit-resolver queries before restoring the original
physical address. It does not flush the DNS cache or convert a later successful
probe into a passing original test. This instrumentation changes no production
behavior; its error branch still requires a live reproduction to validate fully.

Three bounded attempts were made, without repeating until all passed:

| Attempt | Outcome | Recovery to CONNECTED | Recovery through HTTPS |
| --- | --- | ---: | ---: |
| source-dns-1 | Full source-migration PASS | 11,381 ms | 12,277 ms |
| source-dns-2 | Initial CONNECT deadline, before fault injection | Not exercised | Not exercised |
| source-dns-3 | Full source-migration PASS | 14,763 ms | 15,206 ms |

The two completed migrations preserve the daemon process and pass exact network
baseline restoration and native cleanup. The second attempt received Rendezvous
candidate batches but recorded brokered ICE timeout/caller cancellation; it
exceeded the 120-second initial connection budget. Its explicit Disconnect and
Shutdown succeeded, with SCM exit 0. It is not a passing connection test or a
source-migration failure. Root cause of the ICE failures is not established.

The earlier DNS failure did not recur in the two migrations and remains open;
absence in this small sample does not establish a fix. Physical-source probes
were blocked, but these tests do not provide continuous packet-capture proof.

Allowlisted evidence is saved as
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-source-dns-evidence-01.tar`,
SHA-256 `9bfb7c677f4f8d798ca9e70a3b0e5571d15883e66cb667a4c6dfcdf94681a5ce`.
The final native cleanup check passed; services are stopped with their original
start modes restored. VM runtime logs and private profiles remain in the lab,
not in this archive.

### W05/W08 — isolate resolver recovery from NIC migration (2026-10-13)

Three native Windows experiments used the existing Wintun/WFP/DNS/route fixture,
not a new installation. The installed desktop daemon remained stopped. First,
the synthetic tunnel resolver answered IPv4 and IPv6 queries. A lookup then
failed while replies were withheld, followed by a 15-second hold. Replies were
restored and ordinary Windows resolver requests for the failed name and a new
name both succeeded without cache flushing. That initial experiment used the
fixture's final IPv6 DNS setting; a second explicitly switched to IPv4 DNS,
matching the current production resolver family. Both passed.

A third experiment withdrew the WFP user-tunnel permission while DNS remained
configured. Its negative control observed a failed lookup and zero DNS packets
at the TUN; protection remained installed. After restoring that same tunnel
permission, the old and fresh names resolved in 1.70 and 1.08 ms, each with a
real synthetic DNS reply. The IPv4 unanswered-server variant recovered in
4.33 and 0.53 ms. These are fixture observations, not production latency targets.

Each experiment passed native cleanup and exact route/DNS/adapter baseline
comparison. Ordinary `windns` package tests and native fixture builds also
passed on Windows. The owner service and test adapter were removed; no MSI,
product reinstall or production DNS/cache change was made. One initial harness
preflight selected an insufficiently protected parent directory and stopped
before network changes; the runs used the existing protected evidence parent.

`dns-check.ps1` now exposes `RecoveryResolver` and `RecoveryPolicy` modes. These
do not simulate interface replacement or the full installed data plane, do not
cover every resolver API/query type, and have no new independent physical-NIC
capture. They therefore neither explain nor close the intermittent curl-6
failure after source migration. They provide evidence against introducing an
unconditional system-wide DNS cache flush based solely on that hypothesis.

Allowlisted evidence:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W05-resolver-recovery-evidence-01.tar`,
SHA-256 `889fedabf01ee5149db73a85b07e74bb528f8fa52e1f0c04dc90ecbb3bc4fdd0`.
All 28 internal manifest entries were verified after transfer. Source hashes
identify the incremental fixture variants; the third is the retained policy
recovery regression. The next integrated investigation must capture route,
resolver and pinned-address user traffic while the changed source is still
active, before DHCP restoration can alter the evidence.

### W08 — source migration with an explicit uncached DNS gate (2026-10-13)

Three bounded repetitions on the unchanged installed daemon (`bc09655`, hash
`b83182f4d581830872a7c8d75b32ad4a86b124a5c5415b75024e962210c5dda8`)
passed full source migration, user HTTPS and exact cleanup. No installer or
package update was run. These ordinary hostname-based HTTPS probes can use
cached DNS; their success is not, on its own, proof of a new DNS transaction.

| Attempt | To CONNECTED | Through HTTPS | Explicit uncached DNS |
| --- | ---: | ---: | --- |
| source-dns-4 | 13,434 ms | 13,813 ms | Not separately exercised |
| source-dns-5 | 14,508 ms | 14,888 ms | Not separately exercised |
| source-dns-6 | 13,959 ms | 14,358 ms | Not separately exercised |
| source-dns-7 | 13,801 ms | 14,208 ms | PASS before/after migration, 55 ms each |

The fourth attempt used the new optional `-DNSProbe` lab helper. It calls native
`DnsQuery_W` through the existing x/sys binding with cache bypass, no HOSTS,
NetBT or multicast, and fully qualified name semantics. It checks returned A
answers, frees native results, reports monotonic query duration and bounds the
helper process at 20 seconds. It neither flushes the cache nor modifies DNS
configuration, and is not included in the product package. See the Microsoft
[query options](https://learn.microsoft.com/en-us/windows/win32/dns/dns-constants)
and [API ownership contract](https://learn.microsoft.com/en-us/windows/win32/api/windns/nf-windns-dnsquery_w).

Native helper vet and shuffled unit tests (10 repetitions) passed. Before using
it in the installed test, an ordinary public A lookup succeeded and a unique
`.invalid` name failed as expected. Its successful post-migration query occurred
with the replacement source still assigned, before the harness restored DHCP.
Both the HTTPS and separate uncached query must succeed for this optional gate;
a later diagnostic retry cannot override a failure.

All four runs passed unchanged-daemon identity, exact network baseline and native
cleanup. This strengthens the DNS observation but is not continuous packet
capture or proof that every resolver path is leak-free. The prior intermittent
curl-6 error remains unexplained and is not relabeled PASS. Further migration
cases can reuse the improved oracle without repeating installer testing.

Evidence archive:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-source-dns-evidence-02.tar`,
SHA-256 `a4245498f616598e9086fae95856ac6411b52f9d5d13536ce4f55333566cc352`.
All 71 internal manifest hashes passed independent verification. Native helper
source hashes match the workstation checkout; executable SHA-256 is
`5cd0c4ced927c0a55c83c72946c1f9864bce642f649498354c5d5af280fd3156`.
Both installed services are stopped, original start modes restored and final
native cleanup passes. Distinct-NIC/default-route migration and continuous
transition leak capture remain separate, unproven W08 gates.

### W08 — installed desktop moves between two real guest NICs (2026-10-13)

The peer VM was shut down cleanly and restarted from the same disk/TPM with an
additional emulated e1000e adapter. No product installer/update was invoked.
The separate lab launcher is `IRVIO_WINDOWS_LAB/scripts/start-dual-peer.sh`;
the original single-NIC launcher remains unchanged. Both VM NICs have separate
MACs, Windows GUIDs, ifIndexes, private subnets, gateways and virtual NAT paths:

| MAC suffix | Windows ifIndex | IPv4 | Gateway |
| --- | ---: | --- | --- |
| 52:21 | 6 | 10.0.2.15 | 10.0.2.2 |
| 52:22 | 11 | 10.0.3.15 | 10.0.3.2 |

The new `-AlternateAdapterMAC` harness mode checks both physical Internet paths
before Connect, validates the kernel initially prefers the primary, disables
that NIC and leaves it disabled until automatic recovery, user HTTPS and
uncached DNS pass on the alternate. It captures actual alternate source,
gateway and daemon sockets, checks unchanged process identity, then restores
the primary and requires Disconnect/Shutdown and exact native network cleanup.
The lab coordinator temporarily uses IPv4 interface metrics 10/50 and restores
the original automatic metrics afterward; no production configuration override
or extra introducer is supplied to IRVIO.

| Direction | To CONNECTED | Through HTTPS | Runtime cleanup |
| --- | ---: | ---: | --- |
| ifIndex 6 → 11 | 11,972 ms | 12,428 ms | PASS |
| ifIndex 11 → 6 | 12,495 ms | 12,877 ms | PASS |

Both ordinary physical probes passed before Connect. After failover, original
NIC disablement was still confirmed, and daemon TCP sockets used the alternate
source. Separate native uncached DNS succeeded before and after migration;
the first run's post-migration query took 53 ms. Physical-source HTTPS was
blocked while protected. These are targeted probes, not continuous leak capture.
Both runs used the unchanged installed `bc09655` daemon.

The first **runtime scenario passed**, but its outer lab cleanup initially
failed with Windows error 87: it attempted to enable automatic metric and set
an explicit metric in the same call. The saved metric snapshot was restored
separately and compared exactly. That orchestration failure is retained in
`dual-fixture-01/restoration.json`, not erased by the runtime PASS. The corrected
coordinator ran the reverse direction with exit 0 and exact metric restoration.
Overall assessment: runtime bidirectional NIC recovery PASS; first run has a
resolved lab-cleanup observation. No production fix was inferred from it.

The initial VM start also stopped at a preflight because its TPM process had
exited with guest shutdown; restarting the same TPM state resolved it. Neither
incident required reinstallation or affected workstation routing/proxy. The two
virtual WANs still share one host uplink/public connection, so this is not proof
of switching real ISPs or mobile/restrictive NAT behavior. Guest services end
stopped with original start modes; both NICs are enabled. Continuous transition
capture and the earlier intermittent DNS observation remain open.

Allowlisted evidence:
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-dual-nic-evidence-01.tar`,
SHA-256 `c53fe51be984b68b808b75e7aa261c095a0c827500bcf65a470ab27401a34621`.
All 47 internal file hashes passed independent verification. Both post-migration
uncached DNS queries took 53 ms. Harness SHA-256:
`77b9e73ff92d0b55acb7d52a74f95d8e2dd91eaa7282e5aa55412b3a616124b2`;
dual-NIC VM launcher SHA-256:
`5f2690570a540e3ba6fcdce91e9345f4aba65ab13583a21924d90170e311d384`.
The peer remains running with both NICs for subsequent network/lifecycle gates;
use the separate dual-NIC launcher if restarting this fixture. The general
shared-lab launcher still selects its original single-NIC peer configuration.

### W08 — independent IPv4 UDP capture across NIC failover (2026-10-13)

Reused the installed `bc09655` daemon and dual-NIC peer VM; no installer or
payload update ran. Both Windows VMs remain available after the host reboot.
The workstation proxy/network was not changed. The new lab-only `leakprobe`
sender opens sockets on both guest physical IPv4 sources and sends tagged UDP
probes every 100 ms. Independent QEMU `filter-dump` captures observe both guest
NICs, without modifying guest firewall permissions. Native Windows vet, shuffled
unit tests (10 repetitions) and build passed. Negative analyzer cases explicitly
reject injected leakage, missing controls, wrong run IDs and incomplete evidence.

The `dual-nic-03` run disabled ifIndex 6 and recovered via ifIndex 11 without a
new CONNECT or daemon restart. CONNECTED returned in 19,152 ms; completed HTTPS
in 19,653 ms. Native uncached DNS succeeded after migration in 52 ms. Original
network state, interface metrics and native resource cleanup matched baseline.

| Capture observation | NIC 0 source | NIC 1 source |
| --- | ---: | ---: |
| Protected attempts | 221 | 221 |
| Protected span | 21,999 ms | 21,999 ms |
| Maximum sampling gap | 102 ms | 112 ms |
| Baseline cleartext positive controls | 75 | 75 |
| Post-cleanup cleartext positive controls | 31 | 31 |
| Protected cleartext packets on either NIC | 0 | 0 |

Independent capture verdict: **PASS for these sampled IPv4 UDP probes during
this migration**. Protection coverage starts after initial CONNECTED/traffic,
before the injected fault, and ends before intentional Disconnect. It does not
cover initial CONNECT, intentional teardown, IPv6, all applications/protocols,
or independent public ISPs. Both virtual paths still share the host uplink.
The runtime harness retains `continuous_leak_capture=false`: only the separate
capture evidence supports this additional, limited result.

An orchestration observation is preserved: the original sender's PowerShell
process object reported a null ExitCode after Refresh. It is not recorded as
exit 0. Complete sender/capture evidence independently passed analysis; stderr
was empty and no sender process remained. A subsequent stop-phase smoke check
with the process Handle pinned before waiting correctly captured exit 0. That
check emitted no probes and does not retroactively prove the original exit code.
Both services finish stopped with original start modes; both NICs are enabled,
metrics restored exactly and both capture filters explicitly removed.

Allowlisted evidence archive (38 internal hashes verified):
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-continuous-nic-evidence-01.tar`.
SHA-256: `c9ecf864d5e4c3f3e8e57c8c4288f829cb5abe77cee48e26d0b53c71b9224d3d`.
Raw PCAPs remain private outside the archive; VM copies match host hashes:

- net0: `19edc05f4e981b32b558cc413b3e817922f34659c7ca86a91ea25f0cc0ddc538`
- wan2: `9df863c3412a3df01131eddca1e70235097ff1f3e217a83015b37acc09ed825d`

Helper SHA-256: `59b58e5d39183a0e0ff8305e3b6c213bb83e5856e27beda2fb23512db5983e2c`.
Harness SHA-256: `5b2966a01975b285e43c6367aac55cd77c78731115c3bdfd706aa6bffa42846d`.
All three helper source hashes match the workstation checkout. This result does
not close the earlier intermittent DNS error or broader W08/W13 acceptance gates.

### W08 — installed same-boot crash with independent UDP capture (2026-10-13)

The existing peer desktop installation ran `installed-crash.ps1` on unchanged
`bc09655` daemon SHA-256
`b83182f4d581830872a7c8d75b32ad4a86b124a5c5415b75024e962210c5dda8`.
No installer/update ran, no service recovery configuration was changed and no
workstation network/proxy setting was touched. The recorded SCM policy already
had restart delays of 1,000/3,000 ms. The harness parsed successfully in native
PowerShell and its installed-runtime invocation exited 0.

After one Global CONNECT to NL and successful HTTPS, the harness force-killed
the verified installed daemon process. The old process exited; SCM created a
different daemon without harness Start-Service or a second CONNECT. OS boot and
SCM process identity remained unchanged. The new daemon stayed alive through
the recovery/traffic proof and restored the same NL country intent.

| Measured from forced process termination | Observed |
| --- | ---: |
| New daemon observed Running | 1,923 ms |
| CONNECTED observed | 10,562 ms |
| Completed 65,536-byte HTTPS transfer | 11,061 ms |
| Subsequent uncached native DNS query | 54 ms query duration |

Independent packet oracle: **PASS for sampled IPv4 UDP across this crash**.
Each physical source produced 153 protected attempts spanning 15,200 ms; largest
sampling gaps were 102/105 ms. Both NIC captures contained 73 baseline and 31
post-cleanup positive-control packets for their corresponding source, and zero
protected cleartext probes on either NIC. Sender exit was observed as 0, with
empty stderr and no orphan process. Both capture filters were removed afterward.

Protection coverage begins after initial CONNECTED and ends before intentional
Disconnect. This does not prove initial-connect/teardown leakage, IPv6 or all
protocols. The runtime verdict still labels its own continuous capture claim
false; the separate capture-verdict supplies the bounded additional evidence.
Sanitized events retain both daemon incarnations but do not expose an adopted
flag: that field is null, not independent internal-adoption telemetry. The
process restart, unchanged boot, automatic reconnect and packet observations
establish the tested external recovery behavior.

Explicit Disconnect returned DISCONNECTED with contribution false; Shutdown,
exact network-baseline comparison and native cleanup all passed. Final IRVIO is
Manual/stopped, IRVIO-Recovery Auto/stopped; both have exit 0 and no process.
This adds installed crash evidence without repeating installation tests or
changing production behavior. Full Windows-port acceptance remains incomplete.

Allowlisted archive (27 internal hashes independently verified):
`IRVIO_WINDOWS_LAB/evidence/w08-installed-network-loss/W08-continuous-crash-evidence-01.tar`.
SHA-256: `4bfb27bc17a0607ca109781494554b960d8fce7caec5c4131abac7f7abad80b0`.
Harness SHA-256: `bcb0e15fdca385c042c68ff594e2f73d72b2c4e3fc8c8897571acd3df5e96e47`.
The previously tested leak helper remains
`59b58e5d39183a0e0ff8305e3b6c213bb83e5856e27beda2fb23512db5983e2c`.
Private raw captures are excluded; VM copies match the host PCAP hashes:

- net0: `b2d164bf89348b5687a4426ca143af7f50e517d4042d748cf2a58948dafc1465`
- wan2: `9eef304c300266b843e000c0a09cb9e2e30c05400ae2f18366e4d1b839fb6f9f`

### W11/W12 — refreshed clean-source development handoff (2026-10-13)

Built daemon, GUI/bridge and embedded development setup from a fresh Git archive
of `dfda72923e68741dd600cfdae4ce166e2b5ae186` on IRVIO-Win11-Peer, without
installing or updating the runtime fixture. This includes the production cleanup
fixes in `bc09655`; the preceding c1a71e0 handoff is retained, not overwritten.
Go 1.27.1, Flutter 3.47.4/Dart 3.13.3 and the existing native C++ toolchain were
used. Dependency manifests are unchanged; pinned signed driver/MSVC components,
MMDB and corresponding license inventory were reused from the verified payload.

Go build and whole-tree vet passed; locked offline pub resolution, Flutter
analyze, all 160 UI tests and Windows release build passed. Payload staging and
the embedded setup's bundle verification passed. Independent host validation
checked the exact 507-file payload inventory and every size/hash. Windows ZIP
entry separators were normalized for that comparison, without accepting duplicate
normalized paths. EXE, source archive and payload ZIP hashes match VM outputs.

Current handoff: `/home/ubuntu/Work/IRVIO_WINDOWS_BUILD/dfda729`.
VM build: `C:\IRVIO-Lab\IRVIO-Windows-dfda729`.
`IRVIO_WINDOWS_BUILD/CURRENT.md` points to the new subdirectory; older top-level
files remain historical. New `install.md` includes build/run instructions and
the existing Windows loopback regression fixture. It is an external companion
to the frozen source archive, not a claim that the archive includes later docs.

| Artifact | SHA-256 |
| --- | --- |
| Source archive | `a22246b1ea5bf6c134b68f7815df3998146f69771cca8758ac1881e048a34f15` |
| Development setup EXE | `20533e0689f73c5576845ec1e8a8e10f35b921eaab681b43e790d37bd2d3ef02` |
| Payload ZIP | `78ae7e16ba6854dde432576cca7911a72c23bfa67625103e95b33a6ce96645a3` |
| Payload manifest | `fe7b24a6296df756c66b40833c3eb4c72dba2f8b8c9bd3f80b7e4222dfad9835` |
| Daemon | `3a153faa75c30b4c3e8d0c51cddb9b7ad6ee95007373bb2e88e9894ff69451c9` |
| UI | `9cd1b386a5c465c99039e61f51681115eac3cc1528fe71f9cc14004580bc84d9` |
| Bridge | `a40287522b71ca4eb747dc8bd9dbd2471efdd3677218ada5b8d95d2976f2946e` |

`build-evidence.tar` SHA-256
`94ff919af7d0fe7cf2a4fa712b7be0ed72344d4c0e682189553410f0df9499a0`
contains 16 independently verified build-evidence files. All eight handoff
manifest hashes passed. This is still an unsigned IRVIO development EXE, not MSI
or public release acceptance. The newly built bytes have not been installed or
runtime-tested; prior runtime evidence applies to its explicitly recorded binary.

The first native `go test -count=1 -timeout=5m ./...` invocation exited 1 after
135,852 ms: M5C3 and encrypted M6 migration requested unassigned 127.0.0.2/.3
sources. This repeated the already documented fixture requirement because the
ad-hoc command omitted `tools/windows/w12/integration-check.ps1`. Preserve that
failed log/result under `IRVIO_WINDOWS_LAB/evidence/w12-dfda729`; it is not a new
production defect or a passing regression. No source ownership check, test
assertion or production timeout was relaxed.

A separate full native matrix is running via the existing fixture harness in
`C:\IRVIO-Lab\IRVIO-Windows-dfda729\native-full`. It temporarily owns only the
two loopback aliases and checks exact cleanup in finally. Shuffled/race results
and final fixture cleanup must be recorded after completion; neither this build
report nor package verification preemptively marks them PASS. No installer tests
or multi-hour network soak were added.

### W12 — final native matrix complete; reporting boundary (2026-10-13)

The same full fixture process completed with exit 0 at 11:32:36 UTC. Source is
the frozen dfda729 archive above, native Go 1.27.1/windows-amd64, CGO_ENABLED=1,
llvm-mingw/Clang, GOFLAGS=-p=2. No new installation or runtime campaign was run.

| Stage | Duration ms | Exit |
| --- | ---: | ---: |
| vet | 1805 | 0 |
| test | 127462 | 0 |
| shuffle ×10 | 390156 | 0 |
| race | 258573 | 0 |
| race-shuffle ×5 | 318744 | 0 |
| normal build | 3143 | 0 |
| trimpath build | 3289 | 0 |

`cleanup_delta=[]`; both temporary loopback aliases are absent. No IRVIO,
UI bridge or leakprobe processes remain. Services: IRVIO Manual/stopped and
IRVIO-Recovery Auto/stopped, both exit 0. Explicit env-gated privileged tests
were not enabled: this matrix does not replace their separately recorded gates.

Allowlisted archive: `IRVIO_WINDOWS_LAB/evidence/w12-dfda729/native-full-evidence.tar`.
SHA-256: `1d4d6750f5e8be2b69ac5115e30bdadce41fe2543fdc9399e3c8544f5a103e18`.
All 17 internal hashes independently verified. Initial export-only script used
an extra PowerShell array wrapper and incorrectly rejected the seven-row JSON;
correcting its JSON enumeration allowed export. It did not rerun or alter tests.
The earlier bare-test FAIL remains preserved as a fixture omission.

CGO normal build SHA-256:
`54b0643db2dfa07b34083b63b1bb63fadc82c64cb6d1f5438024b7a060b0c0ed`.
CGO trimpath build SHA-256:
`791aa7e18c8e74a9c84e26d048c80c2174063df9801e28264b263ca6ef3a4faf`.
These are regression build artifacts, not the CGO=0 packaged daemon above.

The user requested no separate laptop or further scenarios, only completion of
the existing tests and a report, then explicitly confirmed pausing the larger
Windows-port goal. The report is `windows-port-report-2026-10-13.md`; current
acceptance limitations remain in `windows-port-current-status.md`. No full-port
production PASS is claimed. Further work requires the user's next instruction.

### P1 follow-up — clock skew fixed in source; DNS observation still open (2026-10-13)

Scope: the user's explicit request after the report reopened these two P1 items,
not the entire deferred port. The following changes are based on `windows-port`
HEAD `23e7167`. No workstation VPN/proxy change, public
server deployment, installer rerun, or release publication was performed.

#### Clock skew / Private VPN

Two related defects were reproduced before implementation changes:

- BrokerCarrier exported a short caller deadline as absolute wall time;
  `validateSignal` compared it directly with a different machine's clock.
- Signed PREPARE/READY default to 1,500 ms lifetimes. Their expiry was compared
  without skew allowance; responder READY and initiator OFFER also copied remote
  absolute expiry into locally created signed payloads. Fixing the outer broker
  check alone would leave the signed chain broken.

RED: native Windows tests on the old implementation reject the broker envelope
at -2 seconds and reject full signed Private establishment at both -2/+2 seconds.
The zero-skew control succeeds. Original failures are retained in `clock/red.log`.

Changes in `rendezvous/service.go` and `sessionestablish/{transcript,initiator,responder}.go`:

1. Broker timestamps have a bounded five-second tolerance, including the upper
   future bound. Receiver processing remains capped at 20 seconds; caller
   contexts/stream deadlines are unchanged.
2. Signed messages accept at most five seconds of clock error (or the caller's
   stricter Policy.ClockSkew). This deliberately changes remote freshness
   validation; it is not a claim of perfect clock-free replay protection.
   Future-created messages are likewise limited to this smaller bound.
3. Convert verified remote expiry into a local deadline capped by the original
   signed lifetime. Do not copy a remote absolute expiry into a new local frame.
   The responder's local READY ownership remains at most 1,500 ms.
4. Signatures, target binding, ACL/admission, attempt/challenge checks, replay
   guard, wire fields and protocol IDs are unchanged. Expired local challenges
   and consumed OFFER attempts remain rejected; there is no timeout increase
   for the selector's local CONNECT attempt.

New regressions cover signed Private establishment at -2/0/+2 seconds, strict
zero-skew policy, stale/future bounds, signed-lifetime caps, consumed OFFER and
expired local READY, plus actual libp2p Private broker/registration channels with
opposite ±2-second clock offsets. The broker fixture uses an opaque echo handler;
the full authenticated transcript is tested separately, not falsely reported as
one installed end-to-end clock-shift campaign. A delayed handler verifies that a
100 ms local caller timeout is not extended by remote timestamp tolerance.

Native Windows results:

| Gate | Result |
| --- | --- |
| New clock/remote-deadline cases, shuffle ×10 | PASS |
| Complete sessionestablish + rendezvous, race/shuffle ×5 | PASS (3.666 s / 73.595 s) |
| Final sessionestablish tests including replay/local-expiry assertions, race/shuffle ×10 | PASS (4.005 s) |
| runtime, windns, exitselector ordinary suites | PASS; explicit env-gated privileged suites not enabled |
| Affected package vet | PASS |
| Native Windows trimpath daemon build | PASS |

An intermediate additional test wrongly expected a consumed OFFER to return a
cached response: production deletes its pending challenge after ANSWER and
correctly returns ErrExpired. That test assertion was corrected, not production
replay behavior; its failed `replay-race.log` and the subsequent passing v2 remain.

New daemon: `build/windows-p1/irviod.exe`, SHA-256
`22a36b8c612009fd863c576b2bbb77fad55c5a535075f8642f94146d9daecd20`.
It is built but not installed. Rollout must include receiving brokers and Exit
peers as well as clients; an old receiver retains the old strict checks. Existing
handoff installer/source archive stays frozen, not silently relabeled updated.

#### DNS after migration — investigated, NOT CLOSED

Source review confirms Windows upstream promotion waits for address readiness,
AWG/endpoint and BIND; installs routes; applies and reads back owned DNS settings;
grants user-tunnel permission; and only then completes promotion. DNS failures
propagate as promotion failure. This proves configuration ordering, not a
successful lookup by the Windows resolver at the CONNECTED boundary. No global
cache flush, arbitrary delay or speculative DNS production change was added.

The installed historical daemon `bc09655` remained unchanged for these probes,
so their results must not be attributed to the new clock-skew binary:

| Probe | Recovery to CONNECTED | Through HTTPS | DNS / outcome |
| --- | ---: | ---: | --- |
| p1-source-dns-1 | Not completed within 180 s recovery budget | Not reached | NO_EXIT / broker-ICE failures; not a DNS result |
| p1-source-dns-2 | 12,967 ms | 13,399 ms | PASS, immediate HTTPS and uncached DNS |
| p1-source-dns-3 | 14,796 ms | 15,270 ms | PASS, immediate HTTPS and uncached DNS |
| p1-nic-dns-1 | 136,935 ms | 137,282 ms | PASS traffic; uncached DNS 66 ms; slow recovery observation |
| p1-nic-dns-2 | 7,720 ms | 8,136 ms | PASS traffic; uncached DNS 59 ms |

NIC probes disabled the actual kernel-selected secondary NIC (MAC suffix 22),
recovering through NIC suffix 21. A first invocation requested the wrong primary
NIC and stopped at preflight before mutation; its wrapper initially masked that
error by writing to a not-yet-created evidence directory. The wrapper was fixed,
the correct initial NIC selected, and no product code changed for that issue.

All four completed probes preserve the daemon, require successful user traffic,
block the sampled physical-source HTTPS probes and restore exact network
baseline/native cleanup. These are not continuous all-protocol leak proofs.
The failed first probe restored DHCP, explicitly disconnected and shut down;
later baseline/native checks passed. During testing a bracketed guest-time
sample was at least about 0.95 seconds behind the host while W32Time was running;
that observation alone does not establish the cause of the slow/failed recovery.

The original curl-6 did not recur, so the on-failure DNS diagnostics branch did
not execute. Its root cause remains unproven. Four successful later DNS probes
are not a fix and do not close P1. Next useful evidence is a failing migration
with DNS cache/server/route state and pinned-address traffic captured before
restoration; the existing DiagnoseDNS harness is prepared for that capture.

Allowlisted archive: `IRVIO_WINDOWS_LAB/evidence/w12-dfda729/p1-evidence.tar`.
SHA-256: `62515a45e045483450569d3034326417f0f980c8a9b0c2b131bc9afbfcba1d83`.
All 70 internal file hashes verified; all seven tested source hashes match the
workstation files. Profiles, keys and private daemon logs are excluded. Final
IRVIO Manual/stopped and IRVIO-Recovery Auto/stopped, both exit 0/no process.
