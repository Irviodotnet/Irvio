# Windows product lifecycle gates

Run only in the disposable Windows VM as SYSTEM, through out-of-band management
(QEMU guest agent). ProtectedMode intentionally blocks ordinary SSH traffic during
parts of the test. An observation timeout does not terminate the running test.

Build the real `cmd/irviod` executable and its native Go test executable. Supply
the signed Wintun DLL, existing pinned WinDivert payload installed at
`C:\Program Files\IRVIO Exit Driver`, real DB-IP MMDB and attribution, and an
actual bootstrap bundle. No direct Rendezvous or Exit endpoints are configured.
The expected public egress IP is an assertion oracle, not a discovery input.
The fixture registers WinDivert as a demand-start dependency of `IRVIO`, so SCM
starts the verified driver again when the desktop service starts after reboot.
`IRVIO-Recovery` remains independent and does not start the Exit driver at boot.

```powershell
$fixture = @{
  Binary = 'C:\lab\irviod.exe'
  TestBinary = 'C:\lab\irviod.test.exe'
  DesktopSID = (Get-LocalUser IrvioLab).SID.Value
  BootstrapFile = 'C:\lab\bootstrap.txt'
  Country = 'NL'
  ExpectedEgress = '<expected Exit public IPv4>'
  Wintun = 'C:\lab\wintun.dll'
  GeoIP = 'C:\lab\country.mmdb'
  Attribution = 'C:\lab\ATTRIBUTION.md'
  WANProbe = '<reachable ordinary TCP host>:<port>'
}
./product-global.ps1 @fixture -Mode Crash -Evidence C:\lab\crash-01
```

`Connect` tests discovery, connection, DNS, ICMP, no-proxy HTTPS through the
expected Exit, Disconnect and Shutdown. `Crash` additionally kills the connected
daemon, asserts persistent WFP and blocked targeted TCP probes, then waits for
SCM automatic same-boot reconnection. The 8-second SCM restart delay is a test
setting to expose the fail-closed interval, not a product latency target.

For a real reboot, use three separate invocations with the same evidence path:

```powershell
./product-global.ps1 @fixture -Mode PrepareReboot -Evidence C:\lab\reboot-01
./product-global.ps1 @fixture -Mode Reboot -Evidence C:\lab\reboot-01
# After Windows and its guest agent are available again:
./product-global.ps1 @fixture -Mode VerifyReboot -Evidence C:\lab\reboot-01
```

The first step proves real traffic and leaves a connected session. The second
disables crash restart actions (not demand startup), kills only the exact fixture
daemon, and reboots with durable intent/resource residue. The last step checks
that boot recovery ended successfully, the main service never auto-connected,
WFP and intent are absent, ordinary WAN works, and a manual service start remains
IDLE with the bootstrap preserved.

Each fixture uses `C:\ProgramData\IRVIO`, `C:\Program Files\IRVIO-W08`, and the
`IRVIO`, `IRVIO-Recovery`, `WinDivert` services. Existing installations/evidence
are refused. Success removes only these services and archives its profile and
binary directories. Failures retain evidence and installation for diagnosis;
use out-of-band access rather than removing firewall rules indiscriminately.
The gated `TestWindowsNativeProductDiscardLabSession` is an explicit emergency
cleanup for this exact **disabled and stopped** fixture, not product acceptance.

Same-boot cleanup compares exact DNS/routes/adapter state. Across reboot it also
records raw deltas, but compares routes/DNS by interface name and stable adapter
GUID. The OS-owned Hyper-V Default Switch may regenerate its GUID and subnet;
only that named/typed adapter's recreation is excluded from reboot equality.
This does not assert general all-traffic leak freedom, downstream Windows Exit,
GUI, installer, sleep/hibernate or Private VPN acceptance.

## Installed desktop hibernation

`installed-hibernate.ps1` operates on a verified, disposable installed desktop;
it neither replaces the installation nor initiates the power transition itself.
Call `-Mode Prepare -DisposableVM` with `-Evidence`, `-API`, `-Scripts`,
`-PhysicalSource` and `-BootstrapFile`. It requires the daemon stopped, records
the network baseline, starts the service, imports bootstrap, connects and checks
tunnel traffic plus a blocked physical-source HTTPS probe. `-Country` defaults
to NL. Keep its evidence directory protected: API observations contain addresses.

After Prepare succeeds, independently confirm CONNECTED, request `shutdown /h`,
observe the VM power off, and restart that same VM without restoring a snapshot.
Once its guest agent is available, call `-Mode Verify` with the same arguments
and evidence path (BootstrapFile is unnecessary). No new Connect is sent.
Verify requires a new Kernel-Boot event with BootType 2 and the same SCM/daemon
process creation identities, checks traffic/protection, then Disconnect/Shutdown,
exact network-baseline equality and the native maintenance cleanup probe.

Use out-of-band VM management throughout. A failed verification retains state;
do not erase the profile or protection to manufacture PASS. The targeted transfer
probes are not continuous leak capture. This gate does not cover Fast Startup,
S3/Modern Standby, GUI rendering after resume or multiple downstream sessions.

## Installed desktop physical-adapter loss

`installed-network-loss.ps1` uses an existing stopped desktop installation without
running an installer. Supply `-DisposableVM`, a new protected `-Evidence` path,
`-API`, `-Scripts`, `-BootstrapFile` and the unique lab `-AdapterMAC`.
Run through the guest agent, not SSH: the test disables that adapter for 30 seconds
(configurable 15–60), then enables it in a `finally`-protected sequence.

It requires initial CONNECTED with real HTTPS traffic, observes degraded state,
and allows 180 seconds after adapter return for automatic recovery without another
Connect. It checks the same daemon instance, targeted physical-source blocking,
Disconnect/Shutdown, exact route/DNS/adapter baseline and native cleanup.
Recovery times start at the request to enable the adapter and include DHCP/link
availability and probe overhead; they are not exact blackout measurements.

Failures retain the connection intent and protected evidence for diagnosis; the
adapter is re-enabled, but use explicit Disconnect/Shutdown for cleanup. Initial
connection timeout is separate from an injected-fault failure. Targeted probes
are not continuous packet capture, and same-adapter return does not prove a changed
source address, gateway, interface, or public NAT mapping.

For a source-address migration instead, pass `-ReplacementSource` with an unused
address reserved by the lab coordinator in the adapter's existing IPv4 /24.
This variant requires DHCP initially, replaces the original address, preserves
the gateway and waits for a preferred new address. It records actual addresses,
routes and daemon local sockets, requires automatic recovery and traffic while
the original address is absent, then restores DHCP in `finally` before cleanup.
Do not use it on a production host or choose an unreserved address on a shared
LAN. Initial-connect failure happens before any source mutation.

For distinct-NIC failover, pass `-AlternateAdapterMAC` instead of
`-ReplacementSource`. Both adapters must already have distinct usable IPv4
sources and default routes; the kernel must initially prefer `-AdapterMAC`.
The harness first proves ordinary HTTPS on both physical paths, then keeps the
primary NIC disabled while requiring automatic recovery, HTTPS and optional
uncached DNS through the alternate. It records the new ifIndex/source/gateway
and daemon sockets, then restores the original NIC in `finally` before teardown.
This scenario starts recovery timing at primary disable; `-OutageSeconds` is not
used. It does not configure interface metrics: a lab coordinator must snapshot
and restore any metrics it changes, restoring AutomaticMetric separately from
an explicit InterfaceMetric. It proves distinct guest interfaces, not distinct
public providers when both virtual NATs share one host uplink.

Physical-source HTTPS probes now pin an A-record resolved before Connect using
curl `--resolve`, so a tunnel DNS failure cannot itself make the physical probe
look blocked. They remain targeted observations, not a continuous leak oracle.

`-DiagnoseDNS` collects bounded DNS/interface/route/cache state and a pinned-address
tunnel transfer after a post-recovery DNS failure, before restoring DHCP. It never
flushes caches or changes the failed transfer into PASS after a later retry.

For an explicit uncached DNS gate, build the lab-only helper in Windows:

```powershell
go build -o C:\IRVIO-Lab\dnslookup.exe ./tools/windows/w08/dnslookup
```

Pass `-DNSProbe C:\IRVIO-Lab\dnslookup.exe` to require successful native A queries
before the fault and after recovery. The helper uses configured Windows resolvers,
bypasses the resolver cache, ignores HOSTS/NetBT/multicast, and treats the name as
fully qualified; it does not change DNS settings or flush caches. Its process
exits unsuccessfully after 20 seconds if the synchronous native call is stalled.
The recorded executable hash and JSON results distinguish this gate from ordinary
HTTPS, which may resolve a previously cached name. The helper is not packaged
with IRVIO and does not replace an independent physical-NIC leak capture.

Native flag and memory ownership references:
[DNS query options](https://learn.microsoft.com/en-us/windows/win32/dns/dns-constants)
and [DnsQuery_W](https://learn.microsoft.com/en-us/windows/win32/api/windns/nf-windns-dnsquery_w).

## Continuous IPv4 UDP oracle during NIC migration

Build `./tools/windows/w08/leakprobe` natively as `leakprobe.exe`. Its sender opens
two UDP sockets bound to the two guest physical IPv4 addresses before Connect,
then emits tagged probes to documentation address `198.51.100.77:49199` every
100 ms. No listener at that address is needed: independent QEMU captures on
both guest NICs decide whether the cleartext datagrams escaped. Write return
codes alone never establish absence of a leak.

Start both QEMU `filter-dump` captures before the sender. Create an ASCII phase
file containing `baseline`, then launch:

```powershell
leakprobe.exe PHASE_FILE RUN_ID SOURCE_IPV4_0 SOURCE_IPV4_1
```

Redirect its stdout to an NDJSON file and pass the existing phase file as
`-TrafficPhaseFile` to `installed-network-loss.ps1`. The harness labels attempts
`protected` only after CONNECTED/initial traffic, before injecting the fault;
it labels `teardown` before explicit Disconnect and `after` after native cleanup.
The coordinator must set `teardown` before emergency cleanup on failure, retain
at least three seconds of `after`, write `stop`, and wait for sender exit. Pin
the `Start-Process -PassThru` object's Handle before waiting, do not Refresh it
after exit, and reject a missing ExitCode rather than treating null as zero.
The sender independently stops with failure after ten minutes.

Close the two capture filters, then analyze inside the Windows VM:

```powershell
leakprobe.exe check sender.ndjson NIC0.pcap NIC1.pcap RUN_ID
```

The checker requires complete sender sequence/phase order, at least five seconds
of protected attempts on each source, no gap over one second, and at least three
distinct baseline and after packets on each source's corresponding capture.
It matches packet tags to logged attempts and fails any protected cleartext
probe on either NIC. Missing controls, truncated captures, and unlogged packets
are not PASS. Keep raw captures private; export only allowlisted summaries and
hashes. This oracle covers sampled IPv4 UDP while CONNECTED/recovering, not
initial CONNECT, intentional teardown, IPv6, every application/protocol, or
real mobile NAT. The main harness's own `continuous_leak_capture=false` remains
unchanged: the separate capture verdict is required for that additional claim.

## Installed same-boot daemon crash

`installed-crash.ps1` reuses an existing, stopped desktop installation without
installing/updating files or changing SCM recovery policy. Supply `-DisposableVM`,
`-Evidence`, `-API`, `-Scripts`, `-BootstrapFile`, `-PhysicalSource`, `-DNSProbe`
and the oracle's existing `-TrafficPhaseFile`. It records the installed binary
and harness hashes and the actual service failure policy before starting.

The harness imports bootstrap, issues one CONNECT, proves initial HTTPS and
physical-source blocking, then force-terminates only the verified daemon PID.
It never issues Start-Service or CONNECT after that fault. SCM must create a
new daemon within a bounded observation window, restore CONNECTED and user
HTTPS plus uncached DNS. Unchanged OS boot and SCM process distinguish this from
a reboot; the recovered daemon must survive through the traffic check. Explicit
Disconnect/Shutdown must restore exact routes/DNS/adapters and pass the installed
native cleanup probe. The main verdict is not an independent leak-capture proof.

Use the two-NIC `leakprobe` workflow above around the run, including baseline and
post-cleanup positive controls. Capture and sender must remain active across the
process death/restart; label teardown before any emergency Disconnect. A failed
run retains diagnostic state for the out-of-band coordinator rather than forcing
network cleanup by killing unrelated processes or removing protective filters.
