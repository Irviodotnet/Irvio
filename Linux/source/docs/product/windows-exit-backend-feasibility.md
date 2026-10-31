# Windows Exit backend — packet-injection feasibility

Status: candidate for further validation, **not a production backend decision**.
Baseline: `fc7a4db`, 2026-10-09. Full Windows CLIENT+EXIT remains required.

## Why the simple WinNAT composition was rejected

The W06 experiment put the own-upstream route and five downstream flows on a
shared Wintun adapter. WinNAT forwarded those downstream packets into upstream.
Enabling forwarding on the physical interface also invalidated the tested
Winsock egress fence. TUN-only forwarding preserved socket pinning but still
misrouted downstream traffic. Disabling both forwarding flags did not establish
a working dual-role path. See the independent JSON matrix and validation report.

Changing all routes to bypass the tunnel would break the owner's VPN, not fix
CLIENT+EXIT. Dropping the misrouted packets would enforce a safety boundary but
would not make this a usable Exit. Neither workaround satisfies the goal.

## Alternatives considered

| Candidate | Current assessment |
| --- | --- |
| Plain WinNAT/shared routing table | Failed the measured dual-role scenario; not integrating |
| Arbitrary network compartments | No supported general desktop composition proven; the thread-switch API is documented as reserved |
| TCP/UDP userspace stream proxy | Potentially workable but changes TCP termination and needs separate ICMP/IP-semantics review; not selected |
| Custom WFP callout driver | Can target egress but adds a new driver/signing/maintenance pipeline; not implemented |
| Existing signed WinDivert, forward-layer packet injection | Promising bounded IPv4 UDP/TCP proof; further security/lifecycle/performance gates required |

Microsoft documents `SetCurrentThreadCompartmentId` as reserved, so its presence
in a DLL is not evidence that it is an appropriate public product API.
[API contract](https://learn.microsoft.com/en-us/windows/win32/api/netioapi/nf-netioapi-setcurrentthreadcompartmentid).

## Packet-injection candidate

The experiment uses the official WinDivert 2.2.2-A archive, not a modified kernel
driver. At `WINDIVERT_LAYER_NETWORK_FORWARD`, the pinned implementation passes
`Network.IfIdx` to `FwpsInjectForwardAsync0`. That native parameter identifies
the destination interface. This is different from ordinary outbound network
injection, whose interface fields are ignored.
[Pinned implementation](https://github.com/basil00/WinDivert/blob/v2.2.2/sys/windivert.c),
[Microsoft injection API](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/fwpsk/nf-fwpsk-fwpsinjectforwardasync0),
[WinDivert API](https://reqrypt.org/windivert-doc.html).

The prototype runs with WinNAT absent and forwarding disabled on both tested
interfaces. A `/32` route directs ordinary oracle-bound traffic into the own
TUN; explicit forward injection still reaches the mini-PC through physical WAN.
Five synthetic UDP inner sources share port 49000, use separate translated
ports, and recover their original inner destinations/ports. A raw TCP SYN,
handshake and HTTP request receives the real mini-PC HTTP response; reverse
translation preserves TCP sequence numbers and payload. There is no local TCP
stream proxy in this experiment.

Negative controls explicitly select the TUN (matching packets must appear
there) or a nonexistent interface (no physical success or TUN fallback). These
distinguish a real interface constraint from a coincidentally usable default
route. Successful UDP and TCP repetitions are still not full encrypted IRVIO
sessions, general-purpose NAT, Internet egress, or ProtectedMode acceptance.

## Proposed integration boundary, not yet connected to runtime

The bounded translator and native exclusive port reservation now exist in
`internal/exitnat`; see [component contract](windows-exitnat-engine.md). Packet
tests, native reservation tests and fuzzing do not close the driver/runtime
integration gates described here. No packet-driver dependency is enabled in the
production runtime by this component implementation.

The staged [driver trust boundary](windows-exit-driver-boundary.md) now pins the
vendor payload and validates an explicitly installed service without runtime
auto-installation. Empty-filter lifecycle tests do not prove packet forwarding
or cancellable send/receive; those are the next integration work.

The final bounded lab series passed three sets of 100 five-flow UDP lifetimes,
with stable native handles and goroutines. It exposed two prototype/lab issues:
reloading the vendor DLL leaked its per-thread native events, and QEMU's 1024
soft FD limit dropped high-churn SLIRP flows. The current lab wrapper retains
the module and terminates dedicated native-I/O worker threads; the VM process
now has a 65536 soft limit. Failed evidence is retained alongside the unchanged
traffic/deadline rerun. Neither fix establishes production NAT or VPN lifecycle
acceptance. See the validation report for measured resource curves.

The vendor's thread-local synchronous-I/O behavior must inform any future
wrapper design; do not repeatedly Load/Free this DLL on Go's reusable threads.
[Pinned DLL implementation](https://github.com/basil00/WinDivert/blob/v2.2.2/dll/windivert.c).

```text
AWG-authenticated plaintext
    -> active TunnelLease/source ownership + generation fence
    -> existing destination and Safety Policy rules
    -> bounded packet-level NAT mapping
    -> explicit physical-interface forward injection

matching physical reply
    -> flow/remote-tuple validation
    -> reverse translation
    -> existing AWG send path to downstream peer

own-client plaintext
    -> existing Wintun/Windows host path, separate from downstream forwarding
```

Keep AWG, ICE, signaling, TunnelLease, PeerID, selection and wire protocols
unchanged. The AWG dependency already checks plaintext source ownership against
the receiving peer; the backend must additionally respect active lease state,
revoke and generation fencing. Merely recognizing an inner source prefix is
not sufficient authority. Direct session-control traffic remains a distinct,
authenticated path with narrowly scoped local exceptions.

Forward-injected packets bypass subsequent WFP classification according to the
native API contract. Therefore **Safety Policy, host/LAN denial, source checks
and reply authorization must execute before injection**. A generic WFP permit
rule cannot be substituted for those checks. Own-host fail-closed WFP policy
must still cover ordinary traffic, existing sockets, process crash and reboot.

## Required gates before production integration

1. Explicit dependency/trust decision: official driver provenance, supported
   versions, licensing, DLL load protection, installer ownership, existing
   WinDivert coexistence and rollback. The vendor DLL is unsigned; the SYS
   signature validates. Do not describe the whole package as Authenticode-signed.
2. Run on a clean Windows image without the lab's Hyper-V feature set; Home and
   Pro are separate acceptance targets. No Secure Boot/Defender disabling.
3. Bounded NAT engine: port exclusivity/collisions, TCP/UDP/ICMP, checksums,
   fragmentation/reassembly, ICMP errors/PMTU, IPv6 or explicit fail-closed
   treatment, tuple expiry, duplicate/out-of-order packets and resource limits.
4. Active leases/revoke, per-peer source validation, private-target behavior,
   Safety Policy and host/LAN/downstream isolation at the injection boundary.
5. Physical NIC removal/reuse, route/source changes, existing flows, generation
   changes, packet queues, handler failures, driver/BFE lifecycle and crash.
6. Real encrypted Linux/Windows mixed sessions, at least five downstream peers,
   own upstream concurrently active, throughput/latency/CPU measurements and
   100 full Global/Private connect/disconnect cycles with resource accounting.
7. Package/install/uninstall and signed-binary provenance; never stop or remove
   another application's WinDivert service/handles just because names match.

The lab's fixed five-flow mapping and hand-written TCP test are **not** reusable
production NAT implementations. They deliberately target a private management
oracle; that is not authorization to weaken the product's LAN deny policy.
The experiment installs no new production dependency and changes no Core code.

Source archive: [official 2.2.2 release](https://github.com/basil00/WinDivert/releases/tag/v2.2.2).
Measured archive SHA-256:
`63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15`.
This measured hash records the fetched artifact; it is not an independent vendor
signature. Preserve the vendor license and attribution if the dependency is
later selected for redistribution.
