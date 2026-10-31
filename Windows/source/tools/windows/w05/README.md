# W05 — native persistent WFP object store

Run only in an elevated disposable Windows VM. The `filterprobe` / `scopeprobe`
tests target only mini-PC TCP/49494 and UDP/49495 at `192.168.23.2`.
**The `protected-*` tests below install machine-wide protection and can block SSH.**
Out-of-band Guest Agent access is mandatory for these tests and reboot tests.

`internal/wfp` implements bounded, atomically replaced, owner-marked persistent
objects using native APIs. `killswitch.WindowsBackend` now connects it to the
existing Controller in native tests, not yet the full daemon composition.
Complete DNS/routes composition, product lifecycle and Windows CLIENT+EXIT readiness remain
separate gates. No new external library is required.

## Native interface DNS backend

`internal/windns` snapshots configured IPv4/IPv6 DNS independently, journals
before changing only the server lists on an explicit Wintun GUID, and restores
the original automatic/static configuration. It never persists DHCP-learned
servers as static settings. Suffixes, registration settings, profiles and other
interfaces are not overwritten. External DNS edits yield `ErrConflict` and
retain the recovery record; an absent GUID is not replaced with a recycled
interface index. Encrypted/profile interface DNS overrides are refused.

Use the existing `winfile` protected directory as the single canonical journal
location for a runtime owner. `Close` restores DNS; process-crash recovery uses
`Recover`. On any restore error the runtime must retain protection. Native calls
have no hard cancellation or atomic compare-and-swap against an administrator's
concurrent changes. This package does not install routes, change system-wide
DNS policy, flush caches, or claim NRPT/GPO/DoH/multiple-NIC compatibility.

The SDK oracle `dns-layout.cpp` validates 64-bit V1/V3 layouts and independent
address-family reads. On the tested Windows build, IPv6 reads require flag 1;
zero flags read IPv4 only. V3 detects interface encrypted DNS properties. Native
RPC calls are serialized on retiring locked threads rather than leaving their
thread-local resources on arbitrary Go worker threads.

From the guest checkout, with `go` in PATH and the signed Wintun DLL in
`C:\IRVIO-Lab\W05`, run only in the disposable VM:

```powershell
.\tools\windows\w05\dns-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-dns -Mode Backend
.\tools\windows\w05\dns-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-dns-packets -Mode Resolver
.\tools\windows\w05\dns-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-dns-quick -Mode QuickResolver
```

Run sequentially: the fixture owns `IRVIO-DNS`. Packet modes require Guest Agent
access, install full-machine WFP protection and create/remove the dummy owner
service themselves. Do not pre-create it. They test the ordinary Windows
resolver through actual IPv4/IPv6 Wintun DNS, plus an unresponsive tunnel
resolver. UDP/49495 to the mini-PC carries only six timing markers per repeat.
No external DNS exception is granted under protection.

For packet modes, start an independent QEMU `filter-dump` on guest netdev `net0`
before the script and remove that exact capture object afterward. Run:

```bash
python3 tools/windows/w05/check-dns-capture.py /path/to/physical-nic.pcap
```

The checker requires three complete marker sequences and a positive-control
physical DNS packet. It fails any outbound physical TCP/UDP port-53 packet from
the lab's `10.0.2.15` during the marked protection intervals. The QEMU underlay
has no IPv6; this is not an external IPv6 leak proof. PCAP timestamps must not
be compared directly with guest wall time. Raw PCAPs can contain background
traffic and stay outside Git.

Quick teardown deliberately omits the post-cancellation hold. Windows DNS
Client can continue an outstanding query after its caller cancels it; the
capture must distinguish packets after protection removal from protected leaks.
The optional hold in the other mode is diagnostic, not a production delay or
a proof that every outstanding query has terminated.

Emergency exact-owner cleanup through Guest Agent:

```powershell
$env:IRVIO_DNS_PROTECTION_CLEANUP='1'
C:\IRVIO-Lab\W05\dns.test.exe '-test.run=^TestNativeDNSProtectionCleanup$' '-test.v'
Remove-Item Env:IRVIO_DNS_PROTECTION_CLEANUP
```

Independently verify no `IRVIO-DNS` adapter, test WFP marker or fixture service
remains, and compare physical DNS with its baseline. The 100-cycle resource
gate measures DNS apply/restore, not complete product CONNECT/DISCONNECT.

## Build and fixture

`winroute.TunnelRoutes` now provides an owned route lifecycle using a protected
write-ahead journal. `OpenTunnelRoutes` requires the LUID of an exclusively
owned virtual interface and a canonical private journal directory. `Ensure`
installs the selected IPv4/IPv6 split-default pairs; a family change requires
`Remove` while protection remains active. `Close` removes owned routes and
releases the journal lock. `RecoverTunnelRoutes` cleans the recorded GUID,
including prepared/partially applied records, without substituting an ifindex.

Pre-existing routes are conflicts even when identical. Lookup considers other
next hops on the same interface/prefix, not just the desired on-link key.
Cleanup preserves changed metric/protocol/lifetime/flags and reports conflicts;
successful cleanup is not reported if journal persistence failed. A missing
interface clears its record; down interfaces cannot be reported installation
ready. The caller must retain WFP on any unresolved cleanup error.

One retiring native worker owns each complete operation (including journal and
rollback). Native APIs are not atomic against a concurrent administrator: a
byte-identical privileged replacement cannot be distinguished, and native calls
have no hard cancellation guarantee. The directory lock fences cooperating
owners using the same canonical path, not arbitrary directories or programs.

Run the owned route gate only through the disposable VM's Guest Agent:

```powershell
.\tools\windows\w05\route-owned-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-route-owned
.\tools\windows\w05\dns-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-routed-dns -Mode RoutedResolver
```

The first covers native lifecycle, foreign collisions/edits, adapter removal,
real child-process kill and 3×100 apply/remove cycles. The second composes WFP,
owned routes and DNS: off-link documentation-prefix DNS servers are reached
through Wintun; DNS and routes are restored before protection is removed.
Use the same independent physical-NIC capture/checker as the DNS instructions.
Neither is a full production daemon connection or Windows Exit test.

`-Mode RecoveryResolver` additionally returns the IPv4 tunnel resolver after a
3.5-second canceled lookup and 15-second hold, then checks both the same name
and a fresh name without flushing caches. `-Mode RecoveryPolicy` instead
withdraws the WFP tunnel allowance during that interval and restores it before
the two lookups. The negative control requires zero TUN DNS packets while
blocked, versus received but unanswered packets in `RecoveryResolver`.
Both modes preserve protection until DNS/routes are restored and require the
same disposable-VM/Guest-Agent precautions; neither simulates NIC replacement
or proves recovery of the installed daemon's full data plane.

The earlier isolated split-route experiment checks
native route ABI/readback, ordinary IPv4/IPv6 UDP reaching Wintun, and pinned
physical control UDP reaching the mini-PC while those tunnel routes are active.
It adds only four routes on its new adapter, never replacing physical routes.
Compile `route-layout.cpp` with the Windows SDK as the independent ABI oracle,
run the mini-PC `echo-oracle.py`, then use Guest Agent to invoke:

```powershell
.\tools\windows\w05\route-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-route-probe
```

The script compares all observed route identities/metrics/protocols before and
after and requires the fixture TUN to disappear. This is a routing feasibility
test, not a journaled production route manager, combined WFP leak proof, or
full VPN connection. No forwarding, DNS or physical interface settings change.

Use the installed Windows SDK/MSVC to compile `wfp-layout.cpp`; its output is the
independent ABI oracle for `TestSDKLayout`. From the Windows source checkout:

```powershell
go vet ./internal/wfp ./tools/windows/w05/filterprobe
go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 ./internal/wfp
go build -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\filterprobe.exe ./tools/windows/w05/filterprobe
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\wfp.test.exe ./internal/wfp
```

Refuse an existing `IRVIO-W05-Owner` service before creating this fixture:

```powershell
New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic
Start-Service IRVIO-W05-Owner
$env:IRVIO_WFP_NATIVE_TEST='1'
go test -gcflags=all=-d=checkptr=2 -v -shuffle=on -count=3 -timeout=5m ./internal/wfp
```

The dummy auto-start service exists only to satisfy the native provider lifetime
contract. It does not implement product boot recovery. A real installer must
associate persistent policy with the product's own auto-start recovery service;
an arbitrary existing system service must not be substituted.

## Full ProtectedMode backend experiment

`killswitch.WindowsBackend` compiles persistent IPv4/IPv6 ALE/transport default
blocks, loopback, canonical Safety Policy v0, optional Wintun interface permits,
DHCP/IPv6 neighbor maintenance and bounded control-plane grants. Grant updates
are fenced to an installation epoch; removal discards previous tunnel LUIDs and
endpoint grants. Existing NetworkGeneration fencing remains the caller's duty.

ALE control permits require the native executable identity **and** SYSTEM or
an enabled Administrators token. DHCP requires the `Dhcp` service SID and
system `svchost.exe`, not arbitrary UDP/68 or UDP/546 senders. Transport retains
tuple/interface restrictions; it does not silently pretend to match App/User ID.
Descriptors use `FWP_ACTRL_MATCH_FILTER` and exact native readback. Installer
executable ACLs remain required; administrator compromise is not a kill-switch
security boundary.

Prepare the same dummy service and independent mini-PC oracle above, then run
from an elevated disposable VM with working Guest Agent access:

```powershell
go vet ./internal/wfp ./internal/killswitch
go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 ./internal/wfp ./internal/killswitch
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\protected.test.exe ./internal/killswitch
Copy-Item C:\IRVIO-Lab\W05\protected.test.exe C:\IRVIO-Lab\W05\foreign-protected.test.exe
.\tools\windows\w05\protected-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-protected-run
.\tools\windows\w05\protected-crash-check.ps1 -Evidence C:\IRVIO-Lab\evidence\NEW-protected-crash
```

The signed Wintun DLL must be beside these test executables. The first script
creates/removes a temporary non-admin account with an ephemeral credential,
and checks the same EXE path under that real token. It also tests actual Wintun
IPv4/IPv6 UDP echo, Safety Policy suppression, NO_EXIT, grant revocation and
disconnect restoration. Scripts refuse existing evidence directories.

The second script kills a real protected process, checks retained WAN blocking,
adopts policy in a fresh Controller and restores WAN on Disconnect. It verifies
DHCP renewal on the QEMU guest's `10.0.2.15` adapter by an advanced lease timestamp.
That address is a lab assertion, not production configuration. These are not
full `irviod` ConnectIntent/reconnect, DNS/routes or new-boot cleanup proofs.

Emergency **exact test-owner only** cleanup through Guest Agent:

```powershell
$env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
C:\IRVIO-Lab\W05\protected.test.exe '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
```

Afterward verify the fixed test provider/sublayer are absent, remove the dummy
service, check there is no `IRVIO-W05-TUN`, and stop the mini-PC oracle. Do not
disable Defender, Firewall, Secure Boot, or the workstation proxy.

## Independent live oracle and security

Run `python3 -u echo-oracle.py` on the mini-PC in its management namespace.
Then run `filter-check.ps1` in the Windows VM with a new `-Evidence` directory.
It tests baseline, new/held TCP and UDP blocking, real process kill, native
readback and explicit cleanup. An unavailable oracle is a failed experiment,
not a successful block.

`access-check.ps1` creates a temporary non-admin identity, proves that it cannot
delete the provider/sublayer/filter, then removes that identity. The generated
password is never printed or saved. Root markers and keys are not credentials;
the native DACL is what prevents unprivileged modification.

`reboot-check.ps1` deliberately leaves this scoped policy installed and schedules
a **real VM reboot**. After Guest Agent returns, invoke it with `-AfterReboot`.
It checks a changed boot timestamp, retained enforcement, and successful cleanup.
Retaining policy across boot is a storage primitive proof, not the final Alpha
contract: product boot recovery must subsequently clear previous-boot protection
and return to IDLE without auto-connect.

## Ownership, rollback and diagnostics

Each test logs its provider/sublayer GUIDs. If an interrupted test leaves a
fixture, inspect those exact objects before using:

```powershell
C:\IRVIO-Lab\W05\filterprobe.exe remove-fixture '{provider-guid}' '{sublayer-guid}'
```

The tool still requires the expected test marker and service linkage. Do not
delete all WFP filters or reset Windows Firewall. After tests, remove the live
fixture with `filterprobe.exe remove`, stop/delete only the service created above,
and verify no matching test objects remain using native readback or `netsh wfp`.

Windows may assign a different sublayer weight when a requested weight is in
use. Replacement rejects that mismatch and rolls back; cleanup checks ownership
independently of priority. `Inspect` reads actual conditions and includes the
effective sublayer weight. BFE's automatic `INDEXED` flag is not a policy change;
disabled or unsupported rules are never reported as ready.

Management operations are serialized and executed on reusable locked native
threads, not packet-processing threads. Explicit context checks and a native
transaction wait bound preserve cancellation/rollback; process exit never
implicitly removes persistent protection. This is not a claim of bounded
latency if Windows RPC/BFE itself hangs.

## Scoped enumeration and concurrent probes

`Store` enumerates all native WFP layers inside its transaction, then selects
only its provider in each layer. Boot-time and disabled filters are included;
unexpected layers, owner markers and sublayers cannot silently disappear.
`ReplaceInstalled` performs the old readable-policy/existing-roots precondition
inside the write transaction; it cannot recreate absent protection. Native
post-commit verification remains a separate read.

Build the `internal/wfp` test executable with `-gcflags=all=-d=checkptr=2` and run
with `IRVIO_WFP_NATIVE_TEST=1` while the disposable owner service is running.
The suite compares against an independent unfiltered enumerator, spans multiple
pages, inserts unexpected-layer/boot-time fixtures, and repeats replacement
with resource sampling. `TestNativeStoreTimingProfile` logs measured stages;
it does not impose an artificial performance SLA. `access-check.ps1 -Binary
<wfp-test-executable>` adds a real temporary non-admin access check.

Run `probe-oracle.py` on the mini-PC with TCP/49496–49498 and TCP/49500–49504
free, then use `probe-check.ps1 -Binary <killswitch-test-executable> -Evidence
<new-directory>`. Each repetition includes 20 serial nonce/candidate pairs and
five batches of five distinct concurrent candidates, with the unchanged 750ms
probe deadline, followed by mismatch/timeout/generation checks. Closing a grant
is timed separately in the reported total: a successful dial does not imply the
complete operation including cleanup finishes in 750ms. A successful repetition
produces exactly 68 independent oracle records.

## Resource oracle

The resource test samples total handles, goroutines and handle types every ten
replacements. It allows at most two additional runtime thread handles and at
most four unexplained positive handle deltas. Only the measured Go Windows
per-M footprint is normalized: one Thread, two Event, two IRTimer and one
WaitCompletionPacket. Other kinds are not discounted. The installed Go source
`runtime/os_windows.go` (`semacreate`, `minit`, `mdestroy`) explains this footprint;
the oracle must be revisited if the toolchain/runtime changes.

This is bounded lab evidence, not a universal leak detector. Type enumeration
samples local handles below 0x10000 and is not atomic against runtime thread
creation/destruction. Initial strict total-count failures remain in evidence;
normalization is covered by negative synthetic Event/File/thread leak tests.
No policy behavior or process runtime setting is changed to obtain this result.

## Application/interface/socket scope

`Rule` supports a local interface LUID, exact local address/port and a native
application identity. `ApplicationIDForPath` calls Windows canonicalization;
this is a path identity, not a PID, file hash or publisher signature. Installation
must independently protect the executable and its parent directories. App ID
conditions are rejected at transport layers rather than silently discarded.

Build `./tools/windows/w05/scopeprobe` as `scopeprobe.exe` and copy the same bytes
to `other-scopeprobe.exe`. With the fixture owner service and mini-PC oracle
running, execute:

```powershell
C:\IRVIO-Lab\W05\scopeprobe.exe run C:\IRVIO-Lab\W05\other-scopeprobe.exe
```

The scoped test blocks only TCP/49494 and UDP/49495 to the oracle address. It
allows the first executable at ALE, plus the selected physical interface and
local source tuple at transport. Different-path executable traffic must fail;
the test includes already-open foreign flows, wrong-LUID/address/port controls,
reconciliation and restoration. UDP uses local port 49596, which must be free.
The eight rules use a distinct marker/provider; cleanup runs on all normal error
paths. An actual process crash still needs owner-scoped recovery, not a global
Windows Firewall reset.

Passing this probe does not authorize arbitrary daemon traffic, implement an
underlay policy compiler, or prove IPv6/forwarded enforcement. IPv6 native
readback is tested independently; IPv6 traffic remains a separate live gate.

References for scope: [per-layer conditions](https://learn.microsoft.com/en-us/windows/win32/fwp/filtering-conditions-available-at-each-filtering-layer),
[native application identity](https://learn.microsoft.com/en-us/windows/win32/api/fwpmu/nf-fwpmu-fwpmgetappidfromfilename0).

References: [WFP object management](https://learn.microsoft.com/en-us/windows/win32/fwp/object-management),
[filter lifetime and flags](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_filter0),
[transaction-scoped enumeration](https://learn.microsoft.com/en-us/windows/win32/api/fwpmu/nf-fwpmu-fwpmfiltercreateenumhandle0).
## Owned UDP transport checks

The runtime factory gate now uses `WindowsSocketPermissions` to install both
WFP and journaled inbound firewall scope. Its `Datagram` callback belongs to
`WindowsTransportSockets.Factory`; `Listener` supplies the corresponding TCP
authorization. Socket owners must outlive failed permission cleanup. Runtime
teardown drains socket owners, then the permission manager, then its shared WFP
owner; ProtectedMode removal is a separate final lifecycle operation. Never
use hosting-only mode as fallback for a failed protected-mode constructor.

Run `datagram-oracle.py` on the isolated mini-PC (`192.168.23.2`, UDP 49505/49506),
capturing stdout and stderr separately. Cross-compile killswitch and runtime
test binaries for Windows; execute these harnesses as SYSTEM in the lab VM:

```powershell
.\datagram-check.ps1 -Binary C:\IRVIO-Lab\killswitch.test.exe -Evidence C:\IRVIO-Lab\udp-gate
.\transport-socket-check.ps1 -Binary C:\IRVIO-Lab\runtime.test.exe -CleanupBinary C:\IRVIO-Lab\killswitch.test.exe -Evidence C:\IRVIO-Lab\transport-gate
```

These require the existing `C:\IRVIO-Lab\W05\filterprobe.exe` recovery fixture.
Run native gates sequentially; never share their WFP owner with another test.
Each evidence path must be new. The harness keeps recovery available if exact
owner cleanup fails. Check the independent oracle for forbidden packet tokens;
process exit alone does not prove fail-closed behavior. These are outbound UDP,
STUN and ICE Prepare checks, not unsolicited inbound or full VPN acceptance.

## Physical inbound Windows Firewall

For the composed runtime factories, use `socket-admission-check.ps1` with
`-Binary` (runtime tests), `-CleanupBinary` (killswitch tests), and a new
`-Evidence` directory. It needs the existing WFP recovery fixture. Start the
same independent `inbound-sender.py` before the gate and require six TCP and six
UDP successful application replies (three hosting and three protected cases).
Do not run concurrently with other privileged network gates. No manual broad
firewall exception is needed; remove the two QEMU forwards afterward.

`winfirewall.NewInbound` / `OpenOwnedInbound` own an application-, interface-,
source- and port-scoped TCP/UDP allow rule. They do not replace WFP protection,
disable Windows Firewall or overwrite administrator blocks. The caller must
retain the bound socket until rule cleanup succeeds. The existing durable
firewall journal also records physical scope and supports exact crash cleanup.

For the isolated user-network VM, temporarily forward workstation
`192.168.23.23:49507/UDP` and `:49508/TCP` to the same guest ports using QEMU
`hostfwd_add net0`. Start `inbound-sender.py` on the mini-PC **before** the gate;
it runs for 60 seconds with fresh connections and records application replies.
Run as SYSTEM in the VM:

```powershell
.\physical-inbound-check.ps1 -Binary C:\IRVIO-Lab\firewall.test.exe -Evidence C:\IRVIO-Lab\physical-inbound
```

Require three successful UDP echoes and three acknowledged TCP greetings at
the external sender, with unsuccessful probes before installation and after
cleanup. The gate rebinds listeners after rule removal so queued connections
admitted earlier cannot be mistaken for new firewall admission. Stop the sender
and remove only these two forwards afterward; no workstation route/proxy change
is needed. Run privileged firewall gates sequentially.

`IRVIO_WINDOWS_INBOUND_TEST=1` also enables `TestNativePhysicalCrashRecovery`:
the owner process is killed, live-journal theft is refused and recovery removes
the unchanged recorded rule. `IRVIO_WINDOWS_FIREWALL_TEST=1` retains the original
tunnel-control resource and crash tests, requiring the signed Wintun DLL beside
the test executable. Native stdout/stderr should be redirected through `cmd.exe`
to avoid PowerShell interpreting driver diagnostics as terminating errors.

### Shared daemon platform gate

Run `daemon-check.ps1` as SYSTEM with a cross-built runtime test executable and
the existing exact-owner protection cleanup executable. It enables
`IRVIO_WINDOWS_DAEMON_TEST=1` and checks two repetitions of native discovery/M12,
STUN, protected/hosting startup, journal ownership, forced generation rebind,
disconnect and persistent-identity restart. The temporary recovery fixture and
route/DNS/adapter baseline checks are the same as other W05 gates.

This exercises the daemon library, not the future product/SCM entry point or a
complete VPN session. An independent mini-PC STUN sender can observe UDP/3478
through a temporary QEMU forward; remove that forward after the gate. Do not
overlap this test with any other privileged WFP/firewall gate.
