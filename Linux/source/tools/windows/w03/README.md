# W03 native Windows network-state checks

Lab-only tools. Run as administrator in the disposable Windows VM, not on the
Linux workstation or a production Windows host. The probe uses the pinned AWG
Wintun backend and the production `networkstate.Monitor`; it does not implement
a VPN session or change its MTU default.

Build with the pinned Go toolchain from `C:\src\irvio`:

```powershell
go vet ./internal/networkstate
go test -v -shuffle=on -count=10 ./internal/networkstate
go test -gcflags=all=-d=checkptr=2 ./internal/networkstate
go build -o C:\IRVIO-Lab\W03\networkstate-probe.exe ./tools/windows/w03/networkstate
```

Copy the previously verified signed Wintun 0.14.1 amd64 DLL beside the probe.
Run `networkstate-check.ps1 -Output <new-evidence-path.json>`. An existing output
or test adapter causes refusal, not silent adoption or overwrite. Ensure the
isolated lab prefixes `10.253.242.0/24` and `fd87:7269:6f::/64` are unused.

The harness creates an owned Wintun adapter and high-metric test default routes,
leaving the real IPv4 management route preferred. It tests unrelated adapter
suppression, default addition, effective metrics, metric-only no-migration,
IPv4/IPv6 MTU, address/gateway changes, rename and adapter removal. JSON contains
the observed production snapshots; the harness's `state` query never rescans.
The final normalized snapshot must equal baseline, with no test adapter left.

Windows notifications are supplemented by a five-second reconciliation timer:
the tested build did not notify the monitor of an isolated IP MTU change.
Unchanged reconciliations refresh observation time but do not advance generation.

`netio-layout.c` is a read-only SDK layout oracle. Build with Visual Studio's
x64 developer environment and compare its output to `TestWindowsNativeScan`.
The pinned x/sys version declares an IP-interface row larger than the SDK row;
the backend deliberately uses single-row queries, not that table's array stride.
It reads fields before the differing trailing offload layout. Route-table layout
is separately checked by the oracle. No dependency update is required.

Unit tests cover partial registration rollback, cancellation failure retention,
callbacks during cancellation, the reconciliation goroutine lifecycle, table
join retry bounds, LUID/index reuse, and 100 real native subscription cycles.
Repeat as SYSTEM through the VM's independent QEMU guest agent for service-context
coverage. This is not Windows underlay binding, fail-closed, or full W03 acceptance.

## Underlay route and socket pinning

`internal/winroute` uses `GetBestRoute2` and concrete source/LUID/index validation.
It rejects local-host targets and caller-specified tunnel LUIDs. Excluded LUIDs
must come from runtime ownership; the helper does not guess which third-party
interfaces are VPNs. `Binding.Control` pins egress, while callers must bind the
source address separately and install the appropriate WFP policy.

Run native `go vet ./internal/winroute`, ten shuffled package test repetitions,
and checkptr=2. Set `IRVIO_WINROUTE_TARGET=192.168.23.2` to exercise the live HTTP
oracle; without it the external-target test explicitly skips. IPv4/IPv6 loopback,
source ownership, stale identity, and own-address rejection are still tested.

Build `./tools/windows/w03/routepin` as `C:\IRVIO-Lab\W03-route\routepin-probe.exe`,
with the verified Wintun DLL beside it. `routepin-check.ps1` needs a TCP HTTP
oracle on mini-PC port 49494 and a UDP echo oracle accepting `irvio-nat-` tokens
on port 49495. Reserve `10.253.243.0/24` for its owned Wintun adapter.

**Run this script through the independent QEMU guest agent as SYSTEM. It disables
and restores the Windows VM's physical NIC. Do not run it on the workstation or
through an SSH session whose survival is required for cleanup.**

The test installs a more-specific `/32` route into a blackhole Wintun adapter.
Unconstrained TCP/UDP must fail, with the UDP negative control captured at TUN.
Pinned TCP/UDP and an already-open UDP socket must still reach the oracle without
sending test packets into TUN. While the physical NIC is disabled, both new and
held pinned sockets must fail without tunnel fallback; after restoration they
must work again. The owned adapter/route are removed and ordinary reachability
must return. Every run requires a new JSON output path.

The probe also creates a production `socketmux.Mux` bound to the selected source,
sets the underlay routing-intent tag, and exchanges oracle datagrams through its
logical Data endpoint. Both a held mux and new mux instances must pass the same
route diversion, NIC loss and recovery checks with zero oracle packets at TUN.

Its physical TCP HTTP probe now uses `controlsocket.DialerForBindings`, the same
factory accepted by libp2p discovery. The callback returns the captured baseline
binding, never the unconstrained route after TUN insertion. New TCP connections
must work through route diversion, fail with the physical NIC down, and recover
after restoration without oracle packets appearing at TUN.

This proves physical socketmux integration, not full ProtectedMode or complete
runtime integration. Control-socket policy tags, WFP authorization, and ordinary
user-traffic leak tests remain separate gates. IPv6 physical-network pinning is
not inferred from IPv6 loopback success.

## Native socketmux regressions

Run `go vet ./internal/socketmux ./internal/awg ./internal/transportgen`, ten
shuffled repetitions, and checkptr=2 in the Windows VM. Repeat as SYSTEM with
explicit `GOMODCACHE`/`GOCACHE`; SSH-user caches are not SYSTEM defaults.

The common suites use real UDP/Pion/AWG, including IPv4/IPv6 encrypted inner
packets, three encrypted peers, key rotation, ICE coexistence and STUN refresh.
Their TUN is an in-memory channel, not the production Wintun backend; this does
not prove Windows host routing, mixed-OS VPN operation or TunnelLease readiness.

Windows-specific tests verify exclusive binding against SO_REUSEADDR takeover,
native interface option preservation, rejection of wildcard underlay/unknown
tags, and 100 dual-stack create/close cycles. The handle measurement constrains
Go scheduling and GC, warms the APIs, and allows four runtime handles; it is a
bounded per-batch check, not a proof of process-wide zero growth. Common tests
also check exact port release and closed readers/writers.

## Control-plane TCP and discovery

Run native vet, ten shuffled repetitions and checkptr=2 for
`./internal/controlsocket ./internal/discovery`; repeat three times as SYSTEM.
The Windows binding resolver is called for each new dial and must return the
runtime-owned generation's source/LUID/index. A failure is propagated without
ordinary-route fallback. Literal direct IPv4/IPv6 TCP endpoints are accepted;
unresolved DNS, relay/other protocols and endpoint substitution are rejected.
Libp2p must resolve DNS before invoking this transport factory; underlay DNS
policy itself is a later runtime integration gate.

`discovery.Config.ControlDialer` is mutually exclusive with Linux `ControlMark`.
The Windows explicit factory does not pretend to install SO_MARK or WFP policy.
A mark-only Windows launch fails clearly instead of ignoring the mark. Runtime
must provide the native factory and separately establish WFP authorization.

The Windows discovery regression starts two real nodes, authenticates the
bootstrap connection and completes a Kademlia Ping through the explicit factory.
A second client whose binding resolver fails must fail bootstrap even with the
same reachable bootstrap address. Existing Global/Private provider and recovery
suites also run natively; their filesystem fixtures now create a private child
directory instead of attempting to adopt a test framework's inherited-ACL root.

This does not yet port the full daemon composition or inbound listener
authorization. Native tunnel-only session control is described below; the
separate Windows DNS backend is documented under W05.

## M11 fresh TCP and private enrollment routing

`reachability.NewFreshDialerWithTCP` and `NewFamilyRequesterWithTCP` accept the
same explicit native TCP factory as discovery. Each challenge/request still
creates its own authenticated, non-listening host. Existing Linux marked
constructors remain available; Windows runtime must choose the explicit factory,
not the unmarked test path. Neither factory installs WFP policy on its own.

Run native vet, ten shuffled repetitions and checkptr=2 for
`./internal/reachability ./internal/underlay ./internal/rendezvous`; repeat three
times as SYSTEM. Rendezvous tests need the pinned mmdbwriter and netipx modules
in the offline module cache, including their transitive dependencies.

The Windows M11 regression starts three verifier identities and uses production
fresh dialers/requesters through native IPv4/IPv6 interface bindings. It repeats
proof acquisition despite existing IPv4 host connections, rejects duplicate or
insufficient verifier sets, and rejects both request-side and challenge-side
binding failure without falling back to existing connections. Loopback observed
IP policy is explicitly test-only; this is not public-server readiness proof.

Private enrollment tests include existing owner lookup, real registered-owner
relay and inner authenticated TLS, durable approved target, wrong locator and
stale registration refusal, and carrier byte/deadline limits. The algorithm is
shared unchanged, with native private profile fixtures. The libp2p underlay
challenge is also shared; the Linux raw marked challenge service is not yet a
Windows backend. Full runtime composition and mixed-OS VPN traffic remain open.

## Tunnel-only lease control sockets

Run through Guest Agent as SYSTEM, with the offline Go environment configured:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tools/windows/w03/session-control-check.ps1 -Evidence C:\IRVIO-Lab\evidence\w03-session-new
```

Reserve `IRVIO-Session`, `10.253.248.0/24`, and `fd77:248::/64`. The harness
requires the verified Wintun DLL at `C:\IRVIO-Lab\W05\wintun.dll`, refuses an
existing evidence directory/adapter, runs vet and ten shuffled checkptr unit
repetitions, then three native repetitions. It compares route/DNS inventories
and requires the fixture adapter to be gone. The physical route to the mini-PC
at `192.168.23.2` must exist, but no listener is required there.

`sessioncontrol.TunnelDialer` captures the named virtual interface's LUID,
index and GUID once. Each dial resolves a source on that interface, binds the
source, and applies the existing native unicast-interface socket option.
There is no hostname resolution or default-route fallback. Missing/down or
replaced interfaces, wildcard/loopback endpoints, wrong families and a physical
NIC masquerading as TUN are refused. Reconnect requires the same GUID: Windows
can reuse both LUID and index immediately after adapter recreation.

`ListenTunnel` binds only the specified concrete TUN address and pins replies.
This is not ingress authorization: runtime still owns WFP ingress policy,
adapter lifetime and Noise/lease authentication. It must close listeners and
sessions before disposing the adapter, and establish the inner control route
before BIND; this socket module neither installs routes nor opens WFP access.
The native [unicast-interface option](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ip-socket-options)
selects outgoing traffic; it must not be described as an inbound firewall.

Tests separately observe actual IPv4/IPv6 TCP SYNs at Wintun and test native
listeners with the existing Noise BIND/RENEW/RELEASE protocol. The latter uses
local kernel delivery to the TUN's address, not a remote peer or encrypted AWG
round trip. Identity replacement reuses numeric identifiers while changing
GUID, and the old dialer must fail. No changes to lease authority, RELEASE
ordering, expiry, wire format or Linux sockets are introduced.

These gates do not prove a full Windows VPN, mixed-OS lease renewal or ingress
isolation. The pending production composition must retain the existing order:
protection → AWG → control-only routes → authenticated BIND → user routes/DNS.
