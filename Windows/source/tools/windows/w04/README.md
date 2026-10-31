# W04 native TUN lifetime checks

Run only in the disposable elevated Windows lab. Reserve `10.253.242.0/24`,
`10.253.243.3/32`, and `fd77:242::/64`; do not run these tests on the workstation.
Keep the verified signed Wintun 0.14.1 amd64 DLL outside Git.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tools/windows/w04/tunnel-check.ps1
```

Override `-Source`, `-Evidence`, `-Wintun`, `-Label`, or `-Count` for the local
lab. Use a new label for each run to preserve earlier evidence. The script runs
vet, ten unprivileged shuffled repetitions, then an explicit test executable
beside the signed DLL with checkptr=2 and privileged tests enabled. The native
tests skip unless `IRVIO_WINDOWS_TUN_TEST=1`. Repeat through QGA as SYSTEM with
explicit offline `GOMODCACHE`/`GOCACHE`; this is not a Windows race gate.

The production owner creates its own Wintun adapter and one long-lived ring.
Borrower Close wakes blocked reads, waits for admitted I/O, and closes only its
cancellation handle and event channel. Final owner Close ends the ring and
removes its adapter. This follows the existing DeviceOwner contract without
changing AWG, ICE, TunnelLease or generation semantics. Names already owned by
an interface are refused, not adopted. MTU remains 1280 by default.

Native IP Helper calls configure IPv4/IPv6 MTU and addresses. Startup waits
boundedly for DAD rather than advertising tentative addresses as ready. Dynamic
host addresses retain ownership across partial failures, with rollback and
explicit removal. No shell command implements production configuration.

Checks include:

- One borrower, close/read/write races, partial startup cleanup, failed address
  installation/readiness and failed rollback ownership, invalid buffer bounds.
- Real kernel UDP packets through Wintun in both address families and across
  three borrowed generations; dynamic address addition/removal; unchanged LUID,
  interface index, address readiness and MTU after borrower Close.
- Production AWG/socketmux with a native Wintun client and a channel-TUN test
  peer: IPv4/IPv6 UDP replies, real encrypted handshakes, three device/key/mux
  generations and bounded AWG Close. ICE endpoints are fixture-selected; this
  does not prove ICE discovery, a mixed-OS VPN, Internet forwarding or leases.
- 100 borrower cycles, repeated adapter create/configure/close, handle counts,
  and no remaining owned adapter. Resource diagnostics include OS threads and
  goroutines; `IRVIO_TUN_HANDLE_DIAGNOSTIC=1` optionally samples local handle
  types through read-only native queries, never object names or foreign PIDs.
- Native default-route and DNS snapshots before/after must match. The harness
  does not change the physical NIC, proxy, firewall or default routes.

The IP-interface round-trip uses the SDK's one-byte offload fields; the pinned
x/sys row has incompatible trailing fields. Its 168-byte layout is tested
against the SDK oracle from W03, and native MTU readback is required.

References: [Wintun API](https://git.zx2c4.com/wintun/about/?h=0.14.1),
[IP address creation and readiness](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-createunicastipaddressentry),
[IP interface updates](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-setipinterfaceentry),
[lab object-type query](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntqueryobject).

Full Windows route/DNS policy, ProtectedMode/WFP, Exit forwarding, service crash
adoption, runtime composition and end-user sessions remain separate gates.

## Staged installer with real Linux session control

`client-dp-check.ps1 -NetworkBind` runs the Windows production ClientDataPlane
against `test/cmd/irvio-test-client-dp-peer` on an authorized disposable Linux
host. Reserve `10.253.239.0/24`, TUN names `IRVIO-DP` (Windows) and
`irvio-dp-lab` (Linux). The peer refuses overlapping host routes and existing
TUN names. It creates only a TUN host address, a /32 peer route, bounded sockets
and an in-memory lease. It does not enable forwarding or change default routes.

Build the helper as a normal user, then run it elevated with these environment
variables in a private lab network:

```text
IRVIO_DP_PEER_LISTEN=<Linux-private-IPv4>:<free-TCP-port>
IRVIO_DP_PEER_NONCE=<fresh-random-64-hex-characters>
```

Set the same variables only in the Windows test process environment. Do not put
the nonce in source, reports or shell history. The helper accepts one connection
within 60 seconds and has a 150-second overall lifetime. Keep an independent
out-of-band VM console and capture Linux routes/links before and after the run.
The fixture coordinator supplies public keys, a lease and an observed UDP
mapping over the nonce-authorized lab TCP connection; this is **not** a product
bootstrap or ICE protocol. Identity private keys stay in each process's RAM.

Build `internal/runtime` as a Windows test executable with checkptr=2, place it
next to the verified Wintun DLL and use the existing W05 disposable service
fixture binary. In the elevated Windows lab:

```powershell
tools/windows/w04/client-dp-check.ps1 `
  -Binary C:\IRVIO-Lab\client-dp-v1\irvio-client-dp-bind-v2.test.exe `
  -Evidence C:\IRVIO-Lab\client-dp-v1\network-bind-new-run -NetworkBind
```

Each repetition needs a newly started Linux helper and a fresh evidence path.
The Windows harness removes its exact WFP/service objects and compares route
and DNS snapshots. Confirm the Linux helper exited successfully and restored
routes with no `irvio-dp-lab` adapter/listener remaining.

The gate proves pre-BIND user UDP denial, explicit refusal of a wrong lease
session token over an authenticated Noise channel, real BIND through AWG,
successful inner UDP after promotion, RENEW, RELEASE and zero active leases on
the Linux authority. The authority fixture allocates an active lease directly;
M12 provisional activation, rendezvous/discovery, ICE selection, Exit NAT,
Internet routing and the complete Windows product lifecycle are not covered.
