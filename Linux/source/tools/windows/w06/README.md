# Windows W06 dual-role routing feasibility

## Full product Global upstream / downstream observation

Use a disposable Windows SCM product fixture with the frozen normal daemon,
real bootstrap bundle and MMDB. Before CONNECT, record physical egress with
`curl.exe --noproxy "*" https://api.ipify.org`. Connect Windows to a different
country through the Local API and record its new egress. Both addresses must
be distinct. Do not pass a downstream endpoint or candidate directly to clients.

Start `product-dual-role-observe.ps1` through out-of-band management, with
`-API <test-product-api.exe> -Evidence <new-directory>` plus
`-ExpectedUpstream <upstream-egress> -PhysicalEgress <baseline-egress>`.
`-ExpectedClients` accepts 1–5; a one-client pass is not the five-client gate.
The observer requires CONNECTED + contribution, unchanged `connected_since`,
correct own HTTPS egress, the requested downstream count and its return to zero.
It only observes; the separate client harness controls admission/disconnection.

### Product traffic counters

Before downstream admission, run `product-traffic-check.ps1 -API <helper.exe>
-Evidence <fixture>\own-traffic`. It checks a real 2 MiB own download and 1 MiB
own upload, requiring both own counters to grow while SHARED remains unchanged.
These are IP-byte counters, not application-only byte totals.

Run the five-client harness with `IRVIO_W5_UPLOAD=1` to add a 1 MiB upload per
client after its two 1 MiB downloads. Capture the observer under
`<fixture>\five-measured`, the final active API traffic in
`host-final-traffic.json`, and the post-Disconnect traffic in
`disconnected-traffic.json`. Then run `product-traffic-verify.ps1 -Evidence
<fixture>`: it checks five admissions, zero final clients, one complete session,
monotonic counters and retained totals after Disconnect. Its own-download and
shared-upload bounds assume an otherwise quiet fixture; unexpected background
traffic requires investigation, not silently widening the bounds. Client probe
success/egress and native host cleanup remain independent required gates.

Windows counts accepted plaintext IP bytes at the bridge: own upload into AWG,
own download successfully written to TUN, and downstream upload successfully
forwarded by the lease/Safety-Policy-gated NAT backend. The exact authenticated
upstream TCP TunnelLease endpoint is excluded in both directions. Encrypted
transport overhead, downstream replies and downstream control are not included.
This is session traffic information, not billing or a count of application bytes;
the Linux conntrack/firewall and Windows bridge measurement points differ.

On Linux, use the real product daemon/API to select the Windows Exit's country.
Prove the actual selected path by correlating Windows lease/client admission,
client status and egress, not by assuming the country uniquely identifies it.
Run native DNS, HTTPS and bounded downloads, then DISCONNECT and reconnect.
Each downstream egress must equal physical Windows egress, never own upstream.
Keep Windows connected across downstream RELEASE and probe its upstream again.

During protection, a Windows host HTTPS probe explicitly bound to the
physical source must fail without a response body; after explicit DISCONNECT
the same probe must succeed with baseline egress. This is a bounded negative
control, not a universal packet-level leak audit. Use QGA rather than weakening
protection if SSH management through the physical interface becomes blocked.

Finally compare routes/DNS/adapters and native WFP/intent cleanup before removing
only the owned fixture services. Preserve raw NetworkManager route-metric changes
on Linux and compare again after its normal connectivity check if necessary.
Do not hide a failed cleanup by manually rewriting host routes.

Run only in the disposable, elevated Windows lab. The workstation is not a VPN
test host. These are synthetic IPv4 UDP probes, not full IRVIO connections.

## Owned ICMP errors

`icmp-error-check.ps1` temporarily owns the pinned driver service in the isolated
VM, tests exact TCP/UDP quotation filters and native inbound ICMP capture, then
runs the NAT regressions and checks route/DNS equality. It refuses existing
product or driver services. Compile both test binaries with checkptr enabled:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\exitnat.test.exe ./internal/exitnat
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\windivert.test.exe ./internal/windivert
& .\tools\windows\w06\icmp-error-check.ps1 -ExitTestBinary C:\IRVIO-Lab\exitnat.test.exe -DriverTestBinary C:\IRVIO-Lab\windivert.test.exe -SourceIPv4 10.0.2.15 -Evidence C:\IRVIO-Lab\icmp-new-run
```

ICMP types 3/11/12 quote only existing lease-owned TCP/UDP flows; no flow or
timer is created/refreshed. Router source, MTU, padding/extensions and disabled
UDP checksums survive reverse translation. Quoted fragments/options, malformed
errors, old tokens/generations and revoked/expired leases are denied. Native
capture matches the exclusive protocol/source port, physical interface and
source address; it is not a general ICMP interception rule.

Echo uses the separate nonce-owned translator described below; a UDP reservation
does not reserve an ICMP identifier against the host's ping processes. Component
tests do not prove end-to-end PMTU discovery across a real product tunnel.
The quote length rules follow [RFC 4884](https://www.rfc-editor.org/rfc/rfc4884.html);
native filtering uses [WinDivert byte-offset fields](https://reqrypt.org/windivert-doc.html#filter_language).

## Rejected native echo API candidate

`echo/` and `echo/nativeecho/` are lab-only experiments, not product dependencies.
`IcmpSendEcho2Ex` pins source correctly under split routes and supports bounded
request ownership, but this VM attributes its ICMP to System. An exact
executable-scoped WFP permit fails. Removing the executable condition while
retaining the privileged-account condition also permits a real standard-user
process to send echo. Do not integrate this as an Exit bypass or add a broad
ICMP exception to ProtectedMode.

Reproduce on the isolated SYSTEM QEMU VM with physical address `10.0.2.15`:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\echo.test.exe ./tools/windows/w06/echo
& .\tools\windows\w06\echo-identity-check.ps1 -TestBinary C:\IRVIO-Lab\echo.test.exe -Evidence C:\IRVIO-Lab\echo-identity-new-run
```

This gate uses the SLIRP gateway `10.0.2.2`, so it needs neither external ICMP
nor a change to the hypervisor host. It creates one disposable non-admin user,
starts an actual child process with that primary token, and checks: baseline
success, deny-only failure, privileged-only permit success for both SYSTEM and
the standard user, then denial after withdrawal. Three repetitions, WFP
inspection, route/DNS/adapter equality and account/service cleanup are required.
**Experiment PASS means the unsafe candidate was reproducibly rejected**, not
that Windows Exit echo or full ProtectedMode passed. Child `ECHO_BLOCKED` test
failures in the captured output are expected negative controls.

The separate `IRVIO_ECHO_ROUTE_LAB=1` probe needs signed Wintun beside its test
binary and a working physical public ICMP oracle. `IRVIO_ECHO_WFP_LAB=1` retains
the original failing executable-isolation gate and diagnostic variations. It
requires the isolated `IRVIO-W06-EchoOwner` fixture service; do not run it on an
installed product host. Native buffer/cancellation tests are independently
gated by `IRVIO_NATIVE_ECHO_TEST=1`; a skipped gate is not evidence of success.

## Forwarded echo feasibility without a host ICMP exception

`echo-injection-check.ps1` measures the alternative driver-injection path in
the same isolated SYSTEM VM. It does not enable echo in the production NAT.
Build the test binaries with `-gcflags=all=-d=checkptr=2`:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\echo-injection.test.exe ./internal/windivert
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\echo-ordinary.test.exe ./tools/windows/w06/echo
& .\tools\windows\w06\echo-injection-check.ps1 -DriverTestBinary C:\IRVIO-Lab\echo-injection.test.exe -EchoTestBinary C:\IRVIO-Lab\echo-ordinary.test.exe -Evidence C:\IRVIO-Lab\echo-injection-new-run
```

The existing physical forward sender injects a synthetic request to the QEMU
gateway. Its receive filter matches physical interface, source/destination,
echo type/code, identifier/sequence and a fresh random 128-bit payload nonce.
An ordinary host echo must work while this capture is installed. Under both
target-only denial and the full production NO_EXIT policy, injected requests
must still return while a real standard-user native echo process is denied.
No ICMP permit, public ICMP dependency, host ping setting or workstation route
change is involved. Filter negatives include nonce/tuple mismatch, fragments,
options, truncated packets and wrong direction/interface/protocol.

The harness requires three repetitions, explicit WFP cleanup in a separate
process, route/DNS/adapter equality, and disposable driver/account/service
removal. It refuses existing product installations and driver ownership.
Success demonstrates a usable injection/receive primitive, **not** lease-owned
echo forwarding; use the additional backend gate below. ICMP echo error quotations,
end-to-end PMTU and complete product acceptance remain open. A minimal ICMP error quote
does not contain the payload nonce; never capture it using an unreserved
16-bit echo identifier as if that proved exclusive host ownership.

## Lease-owned echo backend

The Windows production backend now attaches an exact nonce/tuple/interface
capture to each admitted echo request. Limits are 128 outstanding requests
globally, 16 per lease, and the existing shared NAT capacity. Requests expire
after at most five seconds. Original identifiers, sequence and payload are
restored; only payloads shorter than 16 bytes require temporary padding.
Replies consume the capability once. Revoke, expiry, cancellation and network
generation changes fence delivery, and failed native closes remain owned for retry.

`echo-backend-check.ps1` tests the actual Windows backend, then five real AWG
transports with Core lease activation/renewal/release and channel TUNs. These
fixtures coordinate endpoints/keys; they do not prove DHT/ICE or a complete
installed product session. Three repetitions plus 100 encrypted lifecycle
cycles after bounded resource warmup are required. The harness refuses an
installed product or existing driver, removes its driver and compares host state.

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\echo-backend.test.exe ./internal/exitnat
& .\tools\windows\w06\echo-backend-check.ps1 -TestBinary C:\IRVIO-Lab\echo-backend.test.exe -SourceAddress 198.51.100.15 -OracleAddress 198.51.100.2 -Evidence C:\IRVIO-Lab\echo-backend-new-run
```

The recorded oracle is an isolated QEMU SLIRP segment (`restrict=on`), with a
second e1000e NIC and no added guest default route or DNS server. Its documentation
prefix is intentionally not a private destination prohibited by Exit Safety
Policy. Wait until the static source address is Preferred before preflight;
DAD still in progress is not a translator failure. No Safety Policy override,
host ping sysctl change or workstation route change is needed. This proves
native packet translation, not Internet reachability to that documentation IP.

## Runtime generation composition

The runtime generation tests compose one real AWG with the plaintext bridge and
lease-owned NAT. The unprivileged `TestWindowsExitGeneration*` tests use channel
TUNs and an in-memory physical packet oracle. They verify five downstream peers
alongside one own upstream, signed binding fences, renewal/revoke isolation,
failed duplicate installation, stale construction and retryable teardown.

For the native gate, place signed Wintun beside the runtime test executable and
use the pinned vendor payload from the existing module fixture:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\generation.test.exe ./internal/runtime
& .\tools\windows\w06\exit-generation-check.ps1 -TestBinary C:\IRVIO-Lab\W06\generation.test.exe -VendorDirectory 'C:\Program Files\IRVIO Exit Driver' -Evidence C:\IRVIO-Lab\W06\generation-new-run -PublicDNSOracle 1.1.1.1 -ReuseLabPayload
```

The fixture refuses existing driver/control-rule ownership, temporarily owns
the pinned driver service, and checks adapter/route/control-rule cleanup. Three
native repetitions use real Wintun, AWG and WinDivert for five simultaneous
synthetic clients and six public DNS replies each, with an independent physical
oracle preflight. Keys/endpoints and Core lease values are test-coordinated;
this is not discovery/ICE/BIND, full desktop protection or daemon readiness.

## Plaintext bridge component

`internal/exitbridge` has non-privileged real-AWG separation/address-reuse tests.
Run its race suite on the mini-PC and its checkptr suite on Windows. The native
fixture requires the isolated elevated VM and the already verified signed
Wintun DLL beside the executable:

```powershell
go vet ./internal/exitbridge
go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=20 ./internal/exitbridge
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\bridge.test.exe ./internal/exitbridge
& .\tools\windows\w06\bridge-check.ps1 -TestBinary C:\IRVIO-Lab\W06\bridge.test.exe -Evidence C:\IRVIO-Lab\W06\bridge-new-run -Count 3
```

Reserve `10.253.246.0/24` and `10.253.247.0/24` for this fixture; verify they do
not overlap another lab network. Preserve each run's output separately. The
test owns one adapter, validates bounded deletion, and uses an echo callback
instead of real Internet NAT. It proves Wintun/AWG demultiplexing with five
downstream peers plus own upstream and real kernel TCP control exchange, not
Noise BIND or full CLIENT+EXIT product readiness. The harness temporarily permits
only its executable, TCP/7890, local TUN address `10.253.246.1` and remote lab
prefix `10.253.246.0/24`; it removes that exact rule and verifies cleanup. Running
the native test directly without its scoped firewall allowance can time out.
This lab rule is not the still-required product WFP ingress integration.

## Independent oracle

The mini-PC oracle listens on `192.168.23.2:49495`, echoes only synthetic
`irvio-nat-` tokens, and records the received source. Run it in the management
network namespace where that IP exists. Do not disable Defender or host firewalls.

Build `./tools/windows/w06/natdual` on Windows as
`C:\IRVIO-Lab\W06\natdual.exe`; place the verified, signed Wintun DLL beside it.
The probe uses the production Wintun owner/borrower and socket binding packages.
Its single TUN has separate Exit/client prefixes, `10.253.240.0/24` and
`10.253.241.0/24`. It injects five distinct downstream source addresses/ports.

## Controlled matrix

Every invocation needs a **new** output filename. Never overwrite old evidence.

```powershell
$s = 'C:\src\irvio\tools\windows\w06\natdual-check.ps1'
$d = 'C:\IRVIO-Lab\evidence\w06-natdual'
& $s -NoNat -NoForwarding -VerifyPinning -Output "$d\pin-none.json"
& $s -NoNat -PhysicalOnly -VerifyPinning -Output "$d\pin-physical.json"
& $s -NoNat -TunOnly -VerifyPinning -Output "$d\pin-tun.json"
& $s -VerifyPinning -Output "$d\nat-both.json"
& $s -TunOnly -VerifyPinning -Output "$d\nat-tun.json"
& $s -NoForwarding -VerifyPinning -Output "$d\nat-none.json"
& $s -OnLink -VerifyPinning -Output "$d\nat-onlink.json"
```

`-ExplicitExternal` additionally tries WinNAT's external prefix with the bound
physical address `/32`; this OS rejected it with error 87. An unsupported API
configuration is not a traffic pass. The simpler normal-prefix probe remains
independently reproducible.

The script refuses pre-existing NAT, lab adapters, firewall rules and test
routes. It records the physical forwarding setting before changing it and
restores it. A specific oracle `/32` route simulates an own-upstream path without
replacing the VM default route or management routes. `-OnLink` varies its next
hop. Only the exact owned route, NAT, firewall rule and adapter are removed.

Stages are initial physical reachability, baseline NAT, upstream route plus an
ordinary-traffic negative control, connected/re-pinned/unconnected/mux underlay
probes, fresh downstream flows, then route removal and restored downstream flows.
Tokens distinguish repeated host requests; downstream phases use disjoint ports.
The bound socket option is read back from Winsock. A matching numeric index alone
is not accepted as evidence of correct egress.

## Reading results

`lab=PASS` means the experiment executed and its controls completed. It does
**not** mean Windows EXIT or anti-multihop passed. Read `observation` separately:

- `PHYSICAL WAN RETAINED`: all five replies, no matching upstream captures.
- `DOWNSTREAM ENTERED UPSTREAM TUNNEL`: at least one matching outbound packet.
- `DOWNSTREAM BLOCKED OR LOST`: no verified working physical forwarding path.
- `NOT RUN`: only socket pinning was measured, or setup failed.

`cleanup=true` verifies the original default routes and physical forwarding,
and absence of owned adapter/NAT/firewall residue. `-VerifyPinning` requires zero
underlay captures: strong-host interfaces must work, physical forwarding must
be explicitly rejected by the binding guard. It is not a WFP/crash/held-socket
security proof.

The unguarded baseline is production commit `ee1efed` plus this lab probe. Its
native forwarding/socket failures are retained in external evidence. The guard
does not make WinNAT dual-role safe; a different proven Exit routing design is
still required. See `docs/testing/windows-port-validation.md`.

## Separate packet-injection candidate

For QEMU/SLIRP labs, set the VM process soft `NOFILE` limit to at least 65536:
each distinct guest UDP tuple can retain a host socket. A 1024 limit reproduced
packet loss after otherwise successful Windows injection; do not hide that with
longer product timeouts or retries. Preserve failed runs under separate names.

`forward-injection-check.ps1` tests official WinDivert 2.2.2-A in the disposable
VM only. Download the archive from the official GitHub release to
`C:\IRVIO-Lab\irvio-windivert-2.2.2-A.zip`; the script verifies its frozen hash,
checks the SYS signature and refuses any existing WinDivert installation.
The DLL is unsigned; it is hash-matched to the fetched vendor archive. Neither
vendor binaries nor test credentials belong in Git.

```powershell
go vet ./tools/windows/w06/natdual
go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 ./tools/windows/w06/natdual
go build -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\natdual-inject.exe ./tools/windows/w06/natdual
& .\tools\windows\w06\forward-injection-check.ps1 -Stress -Evidence C:\IRVIO-Lab\evidence\w06-inject-new-run
```

Keep the signed Wintun DLL beside the probe. In addition to the UDP oracle,
start a lab-only HTTP server at `192.168.23.2:49494`; a 404 response is sufficient
for the raw TCP handshake/request proof. Never serve repository files or keys.

The candidate uses no WinNAT and no kernel IP forwarding. It verifies five
physical-injected UDP requests, five concurrent translated UDP tuples with
identical inner source ports, a raw TCP/HTTP exchange, wrong-interface and
nonexistent-interface controls. `-Stress` adds three warmups and 100 five-flow
NAT lifetimes per repetition, sampling process handles/goroutines/heap; these
are not 100 production VPN lifecycles. The script runs three repetitions by
default and verifies owned driver-service/rule cleanup afterward.

Only synthetic oracle traffic is handled. No Core, AWG or runtime dependency
is changed. The packet parsing, fixed five-flow table and hand-written TCP
fixture are deliberately bounded lab code, not a shippable NAT engine.
Read `docs/product/windows-exit-backend-feasibility.md` before proposing an
integration. Full source validation, Safety Policy, lease lifecycle and crash
protection must not be inferred from a working injection primitive.

## Staged lease-owned translator and native port reservations

`internal/exitnat` is a separate bounded translator with the existing mandatory
destination and Safety Policy rules. It is not connected to the runtime/driver.
Build its native test executable with checkptr instrumentation, then run on the
disposable Windows VM's concrete physical source address:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\exitnat.test.exe ./internal/exitnat
& .\tools\windows\w06\exitnat-check.ps1 -TestBinary C:\IRVIO-Lab\W06\exitnat.test.exe -SourceIPv4 10.0.2.15 -OutputDirectory C:\IRVIO-Lab\W06\exitnat-new-run -Count 3
```

The source address above is specific to this QEMU lab; choose the actual test
NIC address elsewhere. The script refuses an existing output directory. It
does not change routes, forwarding, protection or firewall policy. It exercises
OS-exclusive source/protocol ports, collisions, non-listening TCP and 100 cycles
of ten reservations per repetition, including concurrent idempotent closes.
It neither injects packets nor proves a complete VPN resource lifecycle.

## Pinned driver module gate

Run elevated as SYSTEM on the disposable VM after verifying/extracting the
official vendor payload as above. This is a lab harness, not the installer.

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\module.test.exe ./internal/windivert
& .\tools\windows\w06\module-check.ps1 -TestBinary C:\IRVIO-Lab\W06\module.test.exe -VendorDirectory C:\IRVIO-Lab\W06-divert -Evidence C:\IRVIO-Lab\W06\module-new-run
```

The harness refuses an existing driver service and creates a private payload
directory under Program Files. It first proves that runtime does not auto-install
the missing service. It then installs/starts the explicit owned kernel service,
runs three sets of 100 empty-filter probe cycles, and stops/deletes only that
exact service. The verified payload directory remains for later lab runs;
`-ReuseLabPayload` explicitly permits reuse without rewriting it or its ACL.
Negative cases exercise altered DLL/SYS, unsafe ACL, concurrent writer, unsigned
signature and wrong service path. No packets are captured or emitted.

## Native overlapped packet I/O

With a bounded UDP echo oracle running on the private mini-PC address/port, use:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\io.test.exe ./internal/windivert
& .\tools\windows\w06\packet-io-check.ps1 -TestBinary C:\IRVIO-Lab\W06\io.test.exe -VendorDirectory C:\IRVIO-Lab\W06-divert -Evidence C:\IRVIO-Lab\W06\io-new-run -SourceIPv4 10.0.2.15 -Oracle 192.168.23.2:49495 -ReuseLabPayload
```

The existing module harness installs/removes only its exact lab service. The I/O
wrapper additionally requires three successful cancellation/close stress runs
and 15 verified physical UDP round trips; environment-gated skips cannot count
as PASS. Replies must reach their exact capture handles and not the reserved host
sockets. Wrong source and pre-canceled sends are rejected. No broad firewall or
route changes are made. This does not combine the packet translator, real AWG
peers, own upstream VPN, or public Internet and is not a CLIENT+EXIT verdict.

## Translator + native driver bridge

On the disposable Windows VM with the verified vendor payload, use an explicitly
chosen reachable public IPv4 DNS resolver (the example is Cloudflare):

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W06\backend.test.exe ./internal/exitnat
& .\tools\windows\w06\backend-check.ps1 -TestBinary C:\IRVIO-Lab\W06\backend.test.exe -VendorDirectory C:\IRVIO-Lab\W06-divert -Evidence C:\IRVIO-Lab\W06\backend-new-run -SourceIPv4 10.0.2.15 -PublicDNSOracle 1.1.1.1 -ReuseLabPayload
```

The harness proves refusal without the installer-owned service, then three runs
of five synthetic active leases sharing inner UDP/30000. Each run first checks
the DNS oracle independently over bound underlay, then obtains five matching
public DNS answers through the translator/capture bridge, revokes leases and
checks quarantine/cleanup accounting. The generic bridge tests also cover
bounded queue overflow, shutdown while dispatch is blocked, capture failure,
partial allocation and retryable cleanup. No production blocklist exception is
introduced. Actual AWG admission, TCP/ICMP forwarding, own upstream protection,
full backend resource stress and daemon composition remain separate gates.

The same harness now also requires `TestNativeEncryptedLeaseNAT` to pass three
times. Five loopback AWG clients and one Exit transport share the production
obfuscation values and use Core allocation/activation/renewal/release with the
new resource adapter. It requires 14 encrypted DNS answers per run: five before
renewal, five afterward, four after releasing one client. RELEASE overlaps a
bounded burst of policy-denied TCP plaintext. A spoofed inner source must not
allocate an extra NAT mapping. The 42 public answers supplement, not replace,
the original 15 unencrypted bridge probes.

Only keys/endpoints and channel TUNs are fixture-owned here; no DHT, ICE selection,
broker signaling, in-tunnel Noise BIND or native Wintun integration is claimed.
An ordinary component test run with the native environment unset skips the
public driver test and cannot be counted as that native acceptance.

### Encrypted resource lifetime

`TestNativeEncryptedResourceLifetime` is a separate explicit opt-in profile,
not part of ordinary native probes. Set `IRVIO_WINDOWS_EXIT_RESOURCE_PROFILE`
to `memory` or `native`, and use the existing owned driver fixture:

```powershell
$env:IRVIO_WINDOWS_EXIT_RESOURCE_PROFILE='native'
$env:IRVIO_WINDOWS_NAT_PORT_SOURCE='<VM physical IPv4>'
$env:IRVIO_WINDOWS_NAT_DNS_ORACLE='1.1.1.1'
& .\tools\windows\w06\module-check.ps1 -TestBinary C:\IRVIO-Lab\W06\exitnat.test.exe -VendorDirectory C:\IRVIO-Lab\W06-divert -Evidence C:\IRVIO-Lab\W06\resource-new-run -ReuseLabPayload -TestPattern '^(TestNativeEncryptedResourceLifetime|TestResourceEnvelopeRejectsNonRuntimeGrowth)$' -RunningCount 3 -TestTimeout 5m
```

Each repetition requires 20 consecutive warmup cycles without a new resource
highwater, within a maximum of 100 warmup cycles, then 100 measured cycles.
Continuous growth fails warmup rather than becoming an accepted baseline.
There are five real AWG clients per cycle and explicit lease/peer/backend cleanup. The `memory`
mode uses the same encrypted lifecycle with an in-memory reply source. Native
mode requires real public DNS replies; network failures are not resource PASS.
No forced GC, finalizer cleanup or production timeout change is used.

The encrypted fixture's DNS application sends at most three copies of a query,
500 ms apart while awaiting a response, and reports `dns_retries`. This models
UDP loss without changing Core or native I/O retry behavior. Exact inner tuples,
DNS IDs and successful response flags remain mandatory; only validated prior
replies from that same client can be discarded as duplicates. Backend errors,
invalid replies and native send errors fail immediately. Unit coverage injects
loss/duplicates and proves that exhaustion is bounded and errors are not retried.

The resource oracle freezes per-type highwater values over the warmup, then
permits at most two additional Thread handles, four Event handles, four IRTimer
handles and two WaitCompletionPacket handles, at most 12 total handles and two
goroutines. All other observed object classes must remain within warmup bounds.
These are bounded scheduler allowances, not attribution of every handle to Go
or proof of arbitrary-duration leak freedom. Local handle-type enumeration is
bounded; raw total handles, heap samples and the complete type maps are retained.
Native Wintun, own upstream traffic and full product lifecycle remain separate
acceptance requirements.

### Native Windows Firewall control exception

The bridge fixture now installs its TCP allowance through `internal/winfirewall`,
not a PowerShell allow rule. The native COM owner constrains application, actual
TUN interface, control IPv4/port and downstream pool, verifies readback, and
refuses changed same-name rules. It neither disables firewall profiles nor
overrides explicit administrator blocks. The GUID owner and captured scope must
eventually join the runtime recovery journal; this primitive alone does not
implement crash/reboot recovery or Windows product readiness.

```powershell
& .\tools\windows\w06\bridge-check.ps1 -TestBinary C:\IRVIO-Lab\W06\bridge.test.exe -Evidence C:\IRVIO-Lab\W06\native-control-new -Count 3
& .\tools\windows\w06\bridge-check.ps1 -TestBinary C:\IRVIO-Lab\W06\bridge.test.exe -Evidence C:\IRVIO-Lab\W06\admin-block-new -BlockControl
```

`-BlockControl` is a negative oracle: it adds an exact lab-owned TCP block,
requires the expected control handshake timeout, then removes only that block.
Both modes check for adapter, route and firewall residue. Neither is a Noise
session-control or complete Windows Exit test.

`internal/winfirewall` native lifecycle tests require
`IRVIO_WINDOWS_FIREWALL_TEST=1`, an elevated isolated VM, signed `wintun.dll`,
and unused `10.253.245.0/24`. They require 20 stable warmup cycles within a
100-cycle ceiling, then 100 measured rule install/remove cycles, idempotence
and foreign-scope preservation checks.
Ordinary tests do not install persistent firewall rules.

### Durable control-rule ownership

Use `OpenOwnedControl` for runtime composition. It holds an exclusive protected
journal, writes exact scope before rule installation, and clears intent only
after verified removal. Failed removal/clear keeps the owner retryable.
`RecoverControl` acquires the same lock and cleans only matching recorded scope,
including when the old TUN has disappeared. It does not change ProtectedMode,
ConnectIntent, boot semantics, or start any network session.

```powershell
& .\tools\windows\w06\control-recovery-check.ps1 -TestBinary C:\IRVIO-Lab\W06\winfirewall.test.exe -Evidence C:\IRVIO-Lab\W06\control-recovery-new -Count 3
```

This fixture requires unused `10.253.244.0/24`. Each repetition kills a real
child owner twice, with and without the parent-owned TUN remaining. It verifies
live-owner exclusion, durable intent, foreign-rule preservation, replacement
ownership, idempotent recovery and cleanup. A removed adapter is returned by
Firewall as a GUID; an existing friendly name must resolve to that exact GUID.
The regular bridge fixture also uses the journaled owner now. These are not
full daemon-crash, real-reboot or Noise BIND acceptance tests.

## ExitServer composition

Build `internal/runtime` as a Windows test executable and place signed Wintun next
to it. In a disposable SYSTEM VM with no existing IRVIO control rules or driver:

```powershell
go test -c -o C:\IRVIO-Lab\W06\exit-server.test.exe ./internal/runtime
& .\tools\windows\w06\exit-server-check.ps1 -TestBinary C:\IRVIO-Lab\W06\exit-server.test.exe -VendorDirectory C:\IRVIO-Lab\W06-divert -Evidence C:\IRVIO-Lab\W06\exit-server-new -PublicDNSOracle 1.1.1.1 -ReuseLabPayload -Count 3
```

This composes the real common ExitServer, M12 control listener, allocator,
Wintun/AWG/NAT, five downstream leases, public DNS and selective RELEASE. The
fixture supplies ICE endpoints and directly invokes lifecycle activation; it
does not claim discovery/BIND, protected own-upstream or full daemon acceptance.
The harness owns the temporary driver service and checks adapter/route/firewall
cleanup. Plain runtime tests additionally exercise staged own-upstream and
failure/rotation ownership using channel TUN and injected host dependencies.

## Exit control replies during own recovery

`exit-control-check.ps1 -Binary <killswitch.test.exe> -Evidence <new-directory>`
requires the existing disposable `IRVIO-W05-Owner` service fixture and a signed
Wintun DLL beside the binary. It injects real TCP SYNs through Wintun and checks
kernel SYNACKs from the tunnel-bound control listener, without mocking firewall
or packet APIs. `-WANOracle` also requires the mini-PC echo oracle on
192.168.23.2:49494/TCP and :49495/UDP, and `foreign-protected.test.exe` beside the
binary; it verifies allowed baseline/restored traffic and blocked physical WAN
while the Exit control grant remains live.

The harness checks exact-owner WFP cleanup and adapter/route/firewall-rule
residue. Its prerequisite service is owned by the outer runner, which must
remove it afterwards. This is a WFP/control component gate, not real ICE,
TunnelLease activation, full daemon or CLIENT+EXIT acceptance.

## Five product clients through a Windows Global CLIENT+EXIT

`product-five-clients.sh` runs on the dedicated Linux mini-PC as root in PID 1's
network **and mount** namespaces. It creates five disposable macvlan/DHCP
namespaces on an explicitly selected LAN interface; the LAN must permit these
addresses. The existing installed daemon must be DISCONNECTED. It does not
run on the workstation or alter its proxy, routes or firewall.

Supply these environment variables explicitly:

- `IRVIO_W5_API`: built `test/cmd/irvio-test-product-api` helper.
- `IRVIO_W5_DAEMON`: frozen production Linux daemon.
- `IRVIO_W5_DNS_LAUNCHER`: existing isolated mount/runtime DNS lab launcher.
- `IRVIO_W5_WAN`: mini-PC physical LAN interface, not its management interface.
- `IRVIO_W5_COUNTRY`: the Windows contributor's observed country.
- `IRVIO_W5_DHCP_HOOK`: executable `product-five-dhcp.sh` from this directory.
- `IRVIO_W5_RESOLV_CONF`: explicit namespace resolver file.
- `IRVIO_W5_BOOTSTRAP_STORE`: existing public daemon bootstrap store.

First establish the Windows product Global session in an isolated VM. Observe
it concurrently with `product-dual-role-observe.ps1 -ExpectedClients 5` and a
sufficient bounded deadline. The Linux clients use real discovery/ICE/AWG/lease
paths but legacy client-only CLI and isolated DNS, not five desktop presets.
The harness records a direct baseline, two concurrent 1 MiB rounds per client,
DNS/egress probes, explicit releases and host cleanup comparisons. Performance
must be judged from recorded timings, not merely the 120-second probe deadline.

Evidence stays in a new `/tmp/irvio-five-product-*` directory. Export an explicit
allowlist only; `client-*/data` contains private identities and must not be
published. Before/after DNS files must be nonempty: equality of two empty shim
outputs is not native DNS cleanup evidence. The five clients do not change the
host resolver and do not validate native Linux resolver lifecycle semantics.

For the installed-package host Disconnect gate, set
`IRVIO_W5_HOST_DISCONNECT=1`. After all five clients finish traffic probes they
keep their leases active and write `clients-probed` in the new private lab
directory. The coordinator must independently verify this marker before
releasing the Windows host's `clients-probed` marker. Clients wait at most
180 seconds for their `host-disconnected` marker; create it only after the host
gate actually passes. They then check five physical-source HTTPS probes are
blocked, explicitly disconnect, and perform the existing cleanup comparisons.
Ordinary traffic may legally recover through another eligible Exit; this mode
does not forbid Global failover or claim that no other RU Exit exists.

`installed-downstream-disconnect.ps1` runs on a disposable canonical desktop
installation, not the earlier `IRVIO-W08` fixture. Prepare requires the daemon
stopped, records network state and physical egress, then imports bootstrap and
connects CLIENT+EXIT with a distinct upstream egress. Observe records the same
own-session timestamp and upstream egress throughout downstream work. After the
coordinator marker it requires exactly five current clients, calls Disconnect,
and checks IDLE/non-contributing state, zero clients, inactive counters, exact
network restoration and ordinary physical egress before Shutdown/native cleanup.
Both sides' evidence is required. This is targeted traffic/protection evidence,
not continuous leak capture, a public server readiness test or full DHT withdrawal
observation from every remote distributor.

Download records now include curl's cumulative DNS/TCP/TLS/first-byte timings
and ports. Subtract adjacent timestamps for stage duration; total minus DNS is
not body transfer time. `IRVIO_W5_CAPTURE_TCP=1` captures bounded-length HTTPS
headers inside each client namespace. Capture files are forensic lab artifacts,
not public release payloads. External QEMU capture is owned by its separate
runner and must be explicitly detached after the campaign.

`dns-pair-probe.ps1` measures direct Windows A/AAAA pairs without IRVIO installed.
Use explicit `-Evidence <new-path> -SourceAddress <physical-IP> -Resolver <DNS-IP>`.
It sends ten pairs with zero gap and ten with a diagnostic 20 ms gap, with an
800 ms receive budget per pair, and records received/missing IDs without retry.
`dns-pair-probe.py --source <IP> --resolver <IP>` provides the independent Linux
control (JSON lines on stdout); run it in the mini-PC's real host namespaces
with its installed daemon DISCONNECTED. Neither probe modifies resolver settings.
Missing replies are measurements, not success. These probes must not be turned
into a production DNS delay or retry workaround.

## Native source-owner performance and fencing

Measure the existing per-packet source-owner check without enabling a service:

```powershell
$env:IRVIO_BENCH_SOURCE='10.0.2.15' # Actual source on the disposable VM.
go test -run '^$' -bench '^BenchmarkNativeBinding$' -benchtime=2s -count=3 ./internal/winroute
```

The benchmark compares complete ForSource lookup, live binding validation and
the older net.Interfaces/Addrs primitives. It does not modify host networking.
ForSource must still obtain fresh ownership data on every call; no benchmark
result authorizes a cached security decision or skipping duplicate ownership.

The opt-in `TestNativeSourceOwnerLifecycle` requires an elevated disposable VM
with no running product and a signed Wintun DLL beside its test executable:

```powershell
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\winroute.test.exe ./internal/winroute
$env:IRVIO_SOURCE_OWNER_NATIVE='1'
& C:\IRVIO-Lab\winroute.test.exe '-test.run=^TestNativeSourceOwnerLifecycle$' '-test.count=3' '-test.v' '-test.timeout=120s'
```

It attempts two owned adapters with the same isolated IPv4 source, requires
either kernel duplicate-address rejection or ambiguous lookup rejection, then
verifies the remaining owner and rejects the binding after its removal.
Synthetic table tests separately require duplicate rejection in both orders.
Compare native routes, DNS and
adapters before/after. Do not execute alongside throughput measurements: adding
an adapter intentionally changes the host network generation.
