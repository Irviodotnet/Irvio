# Mixed-platform Private product gates

Use a disposable Windows SYSTEM product fixture, signed Wintun/pinned WinDivert,
the real bootstrap bundle and existing Linux product API. Workstation networking
must remain untouched. Linux probes must run in the daemon's host namespace, not
an isolated SSH management namespace. Windows SSH network-logon tokens are not
authorized Local API clients; use the SYSTEM guest agent or an authorized local
user. Keep invitation files private and out of Git/evidence exports.

Build `test/cmd/irvio-test-product-api` for each OS and `cmd/irviod`'s native
Windows test executable. The helper uses only existing authenticated API methods:

```text
irvio-test-product-api private-host <socket>
irvio-test-product-api private-invite-file <socket> <new-private-output-file>
irvio-test-product-api private-import-file <socket> <invitation-file>
irvio-test-product-api private-enroll <socket> <returned-host-peer-id>
irvio-test-product-api private-connect <socket> <saved-host-peer-id>
irvio-test-product-api private-connect-host <socket> <saved-host-peer-id>
irvio-test-product-api private-state <socket>
irvio-test-product-api traffic <socket>
irvio-test-product-api private-revoke <socket> <member-peer-id>
irvio-test-product-api disconnect <socket>
irvio-test-product-api private-remove-target <socket> <saved-host-peer-id>
```

`private-cycle-check.ps1` exercises a saved private upstream together with private
hosting in the disposable W08 installation. Supply the helper, saved target,
fresh evidence directory, pre-install route/DNS/adapter baseline and physical
source address. The default is ten warmup cycles followed by 100 measured cycles.
Each cycle checks real HTTPS bytes, a blocked physical-source HTTPS probe,
session traffic, full Disconnect and exact idle network restoration; it records
monotonic timings and process/socket resources. This is not a complete packet
leak oracle or an automatic resource-growth verdict. Compare the recorded
distributions before accepting the resource gate.

For the separate Global lifecycle gate, replace `-Target <peer>` with
`-GlobalCountry NL` (or another available country). The same harness uses the
already-imported bootstrap, verifies Global country selection and contribution
intent without Private hosting, and retains the same traffic, Disconnect and
resource oracles. Contribution intent is not independent proof of registration
on every Rendezvous or acceptance of downstream traffic; those remain separate
mixed-client gates. Never change modes inside one evidence directory.

Unrelated saved targets may remain: their initial count must survive every
Disconnect. Idle daemon active TCP/UDP sockets are a failure, not only a recorded metric.
The failure captures endpoint/state details and five follow-up TCP samples;
`FIN_WAIT_2` is recorded separately from live sockets, alongside the already
excluded `TIME_WAIT`. The controlled Windows run proved that its `FIN_WAIT_2`
entry retained the old OwningProcess value even after that process exited.
Do not exempt `CLOSE_WAIT`, listeners or established connections. This oracle
does not by itself prove that all application handles were released; inspect
the campaign's handle/resource distribution too. Preserve earlier failures.

Measure guest/server clock alignment before the campaign, particularly after
VM resume. Windows Time being started is not evidence of synchronization: save
its last successful sync and an independent bounded offset measurement. The
existing 1.5-second initial attempt deadline is transmitted as absolute time;
a client clock lag exceeding that budget can reach the broker already expired.
Do not extend protocol lifetimes or relax validation to hide a lab clock fault.

A rejected CONNECT remains a gate failure even if automatic recovery succeeds.
The harness records that recovery before Disconnect and tolerates only bounded
API lifecycle-busy replies. Do not replace failed runs with later passing output.
Private hosting does not mean public Exit contribution; the public contribution
flag must remain false for this combined private intent.

`observe-session-port.ps1` is a bounded, read-only companion for the intermittent
Windows `10.6.17.1:51822` bind rejection. Run it in a separate Windows PowerShell
job before the lifecycle harness, with a new evidence directory. It samples
IPv4 TCP owner/state transitions every 25 ms, OS TCP exclusions every two seconds,
and Security events 5155/5159 for that source port. Matching audited denials cause
up to five WFP filter snapshots. The caller, not the observer, owns any temporary
enabling/restoration of **failure** auditing for Filtering Platform Connection;
do not change success auditing or restore unrelated audit policies. Guest SYSTEM
can read the Security log. No audit entries or firewall rules are deleted.

Creating `<Evidence>/stop` ends observation; `-Seconds` is bounded to at most
3600. Join the observer and inspect its exit state/result, not just `ready.txt`.
This sampler is not an ETW trace: IPv6, bound-only sockets and short-lived
conflicts can be missed. No event or a later absent exclusion proves neither
the cause nor absence of the transient conflict. Collected endpoints/filter
data are diagnostic artifacts, not material to publish unreviewed.
`observe-session-port-test.ps1 -Observer <script> -Evidence <new-dir>` verifies
opening and closing a temporary loopback TCP listener in a disposable Windows VM;
it does not exercise or validate WFP-denial event generation.

For Linux-host / Windows-client, create an auto-approve invitation on the Linux
host. Transfer it to a protected Windows lab file, along with a text file
containing the host's observed public egress IP. Import the actual bootstrap
bundle into the Windows fixture. Then, as SYSTEM:

```powershell
Start-Service IRVIO
$env:IRVIO_WINDOWS_PRIVATE_INVITATION_FILE='C:\lab\invitation.txt'
$env:IRVIO_WINDOWS_PRIVATE_EGRESS_FILE='C:\lab\expected-egress.txt'
& C:\lab\irviod.test.exe '-test.run=^TestWindowsNativeProductPrivateClient$' '-test.v' '-test.timeout=5m'
```

Start the demand service immediately before the probe: a disconnected product
with no API clients deliberately stops after its idle timeout. The test imports,
enrolls, verifies approval did not auto-connect, connects twice to the exact
saved target, checks native DNS/no-proxy HTTPS/1 MiB download, and disconnects.
It leaves the service/profile available for the reverse-direction gate.

Observe the host's downstream count and traffic while the Windows test runs;
both machines may share an external IP, so egress-IP equality alone is not proof
of forwarding. Use the API's sampled timestamps; shell `date` formatting differs
between lab hosts and is not a monotonic timing oracle.

For Windows-host / Linux-client, reverse invitation roles with the same helper.
Check exact-target CONNECTED, the client's kernel route to the tunnel, native
DNS, HTTPS and a bounded download. Disconnect/reconnect without another invitation.
Then revoke the active Linux member from Windows: host stays HOSTING, downstream
count becomes zero, Linux loses the private path without public fallback, and
explicit Disconnect restores ordinary WAN. Remove the disposable saved target
and revoke the fixture's previously granted membership on Linux.

Also test ICMP. The initial run exposed missing echo support; the later nonce-owned
translator fixes echo. The product PMTU gate additionally found that the bridge
rejected already-translated ICMP errors; its regression is
`TestBridgedICMPErrorDelivery`. Full Exit parity still requires the remaining matrix.
Complete cleanup must inspect WFP/intent and routes/DNS/adapters before any
emergency cleanup. NetworkManager may temporarily penalize a physical route
after a fail-closed interval; preserve the raw difference and use its own
connectivity recheck, never silently rewrite the route to force equality.

## Product ICMP, UDP and TCP PMTU oracle

QEMU user networking can deny external echo independently of IRVIO when the
hypervisor has no unprivileged ping sockets. Do not weaken the production LAN
ban or change workstation sysctls merely to make the test green.
`qemu-icmp-oracle.py <new-private-directory>` supplies a bounded synthetic router
on `203.0.113.77` (error source `203.0.113.78`) outside the Windows guest.
It also serves a bounded TCP fixture at `203.0.113.79:33435`. It reads QEMU's
mirrored Ethernet stream and returns only matching probes from `10.0.2.15`.
Other traffic is neither logged nor changed.

Attach its two Unix sockets through QMP `chardev-add` (socket clients), then:

```text
object-add: qom-type=filter-mirror, id=irvio_echo_mirror_filter,
            netdev=net0, queue=all, outdev=irvio_echo_mirror
object-add: qom-type=filter-redirector, id=irvio_echo_inject_filter,
            netdev=net0, queue=tx, indev=irvio_echo_inject
```

The character devices connect to `mirror.sock` and `inject.sock` in the new
directory. No host interface/route/firewall modification is involved. Verify
ordinary guest echo to this oracle before using the VPN. Do not choose a target
inside another guest adapter's connected subnet. On completion, delete both
filter objects, remove both character devices and wait for the helper to exit.
Only synthetic event counts/sizes/times remain in `events.ndjson`.

After a real bootstrap/invitation/exact-target Private connection, run on the
Linux client in its actual host namespace:

```bash
ip route get 203.0.113.77 # must select the IRVIO tunnel
ping -M do -s 1200 -c 3 203.0.113.77
python3 udp-pmtu-probe.py
```

The UDP probe first sends a 1200-byte DF IP packet to port 33434. The oracle
returns a minimal valid ICMP type 3/code 4 quotation with MTU 1000. Acceptance
requires the originating client socket to receive `EMSGSIZE`, Linux to learn
PMTU 1000, a subsequent oversized send to fail locally, and a 900-byte UDP
payload to succeed. This does not modify the tunnel's configured MTU.
Use a fresh connection/route cache for repeats, and preserve pre-fix evidence.

For TCP, verify `ip route get 203.0.113.79` selects the tunnel, then run
`python3 tcp-pmtu-probe.py` in the same client namespace. Its connected socket
must initially report a PMTU above 1000. The oracle advertises MSS 1460 but
rejects DF IP packets larger than 1000 with ICMP fragmentation-needed. It only
acknowledges contiguous bytes and returns a success line after receiving all
8192 expected bytes. The probe requires that response and socket PMTU 1000;
the event log must show large rejected packets followed by smaller TCP packets.
This is not a general-purpose TCP server: it has one fixed payload, a 16-flow
limit and no claims about arbitrary TCP interoperability or throughput.

This is a full product path against a controlled router, not proof that QEMU
can send public Internet ICMP. Native Internet DNS/HTTPS/download checks remain
separate. Echo error quotations, fragmentation, IPv6 and dual-role
coverage remain additional acceptance work.
# Completed-cycle resource analysis

After the campaign process has exited successfully, run
`python3 tools/windows/w07/analyze-private-cycles.py <cycles-evidence-directory>`.
The analyzer requires the completed verdict and all rows, excludes warmup,
reports nearest-rank p90/p95, first/last-20 medians and linear slopes, and rejects
missing/duplicate rows, failed probes and active idle sockets. It handles the
UTF-16 JSON emitted by Windows PowerShell. It does not independently prove WFP
cleanup or absence of a resource leak; verify the terminal runner, final cleanup
and distributions separately. FIN_WAIT_2 counts remain visible in the report.

Parser regression: `python3 tools/windows/w07/analyze-private-cycles_test.py`.
