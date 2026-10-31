# Windows Exit packet translator — staged implementation

`internal/exitnat` is a bounded packet-level translation component with a staged
Windows driver bridge, **not yet connected to daemon/AWG runtime**. It does not make Windows EXIT ready. The
existing Linux nftables backend keeps the same mandatory deny prefixes, moved
unchanged into a shared source file; Safety Policy still comes from the existing
`safetypolicy.V0` definition.

## Authority and packet path

Only the existing Core resource activator may create an opaque engine lease
handle. The handle carries the exact lease identity, session token, inner source
and expiry. Renewal cannot change ownership; revoked, expired and replaced
handles cannot regain authority. No wire fields, selection or lease protocol
were added. This component does not authenticate a descriptor by itself.

Outbound packets pass structural/checksum checks, exact lease-source validation,
mandatory host/LAN/management/downstream denial and Safety Policy **before** a
port is reserved or the emitter is called. Replies require the exact current
physical path generation, translated destination, protocol and previously
contacted remote IP/port. Neither direction may fall back to an ordinary host
socket or an own-upstream route.

Mappings are endpoint-independent for a lease/protocol/inner source port; each
remote tuple still has separate filtering/expiry state. Two peers using the
same inner port receive different owned external ports. Sequence numbers,
acknowledgments, options and payload remain opaque TCP bytes, not a terminated
TCP stream. IPv4 and transport checksums are recomputed; hop count decreases.

TCP timeout classification observes ACKs in both directions. A SYN/ACK alone
does not establish the flow; one FIN does not shorten a half-closed connection.
Both FINs must be acknowledged before using the closing timeout. An RST only
retires state when its sequence matches observed acknowledgment state (or an
opening rejection acknowledges the observed SYN); other RSTs do not modify
mapping lifetime. This is not a full replacement for end-host TCP validation.

Default idle times are two minutes for UDP, four minutes for opening/closing
TCP and two hours four minutes for established TCP. These use the minimum
timeout guidance in [RFC 4787 §4.3](https://www.rfc-editor.org/rfc/rfc4787.html#section-4.3)
and [RFC 5382 §5](https://www.rfc-editor.org/rfc/rfc5382.html#section-5); they do
not establish compliance with the rest of those RFCs. A lease's authority
expiry always takes precedence.

## Bounded ownership

The defaults bound active leases to 64, total flow/retired-resource capacity to
4096 and active remote tuples per lease to 256. Dropped traffic allocates no
mapping. An external port stays exclusively reserved during a two-minute
quarantine after the last associated flow is retired. Failed native resource
cleanup remains accounted for and can be retried; it is not reported released.

Revoke waits for already-admitted synchronous sends, then stops all subsequent
traffic. The internal lock also fences path changes. Emit and Reserve are
trusted platform seams: they must perform bounded operations, honor context,
avoid re-entry and never enqueue an untracked send beyond their return. Native
interface identity and route/source revalidation belong in that backend, not
in a claim inferred from the path's numeric interface field.

The owner must call `Sweep` periodically and feed each network generation's
current local addresses/management prefixes to `Reconcile`. Reconcile invalidates
all previous-path mappings before accepting traffic on the new path. No
background goroutine, persistent state or external telemetry is created here.

## Native Windows port ownership

`ReserveWindows` validates the source's current strong-host interface and exact
LUID (`Path.Interface`), pins the socket to that interface, and binds an ephemeral
port with `SO_EXCLUSIVEADDRUSE`. The native handle is non-inheritable. TCP sockets
are never placed in listening/connected state; UDP sockets never send or read
application traffic. The packet driver, not these reservation sockets,
must capture and translate replies before delivery to the host stack.

Each reservation balances its own Winsock startup reference. Close is serialized,
idempotent and retryable; partial native allocation failures return ownership to
the engine when cleanup cannot complete. This prevents a port disappearing from
resource accounting before it has actually been released. Native tests exercise
same-source/protocol collision including `SO_REUSEADDR`, rebind after close,
non-listening TCP, non-inheritance and repeated concurrent cleanup.

This implements source-specific reservation, not a wildcard claim over every NIC.
It follows [Microsoft's socket exclusivity rules](https://learn.microsoft.com/en-us/windows/win32/winsock/using-so-reuseaddr-and-so-exclusiveaddruse).
Reservation alone neither permits traffic through WFP nor validates the later
driver's injection path. `tools/windows/w06/exitnat-check.ps1` runs the gated tests
on an explicitly selected disposable Windows NIC.

## Gates still required before connecting Windows runtime

- Port ownership during actual driver capture/injection and competing host traffic.
- Verified driver loading/ownership and bounded cancellation; physical NIC
  identity/reuse, stale queues, crash and BFE recovery.
- ICMP echo/error translation, PMTU/TTL responses and bounded fragment handling.
  This first implementation refuses IPv6, IP options, fragments and other
  protocols; it must not advertise support or silently route them elsewhere.
- Complete TCP retransmission, reordering, wraparound, simultaneous-open and
  adversarial sequence corpus, plus performance/throughput measurements.
- AWG plaintext demultiplexing, exact pre-activation session-control exceptions,
  real resource-activator/renew/revoke integration and statistics.
- Five real downstream peers while own upstream is active, Global/Private
  lifecycle and full 100-cycle resource/security tests.

The pure packet tests use in-memory reservation/emission seams. Native execution
of those tests on Windows validates architecture/checkptr behavior, not driver
injection, OS socket exclusivity, anti-multihop or a working Exit. Do not replace
these outstanding gates with a client-only Windows release verdict.

## Staged driver bridge

`Backend` owns one fixed physical generation, the translator, a physical sender,
exclusive port/capture pairs and a bounded 128-packet reply queue. A generation
change requires closing this backend before constructing its replacement; it
cannot silently follow a new default route. Runtime composition must still
prove that the supplied path is physical rather than an own-upstream interface.

The engine mints an opaque `ReplyToken` for each port reservation. The capture
reader retains this exact token; `Inbound` checks it under the engine lock.
Queued replies cannot cross into a replacement lease after quarantine/port
reuse, even with identical remote and local transport tuples. This fences old
userspace queues, not indistinguishable late network packets captured by the
new reservation; the existing port quarantine remains necessary.

The capture reader never calls the engine and never waits for queue capacity.
Overflow drops the reply without reinjection. The dispatcher alone enters
`Inbound`; port cleanup joins only its reader, avoiding the engine-lock/reader
join deadlock. Capture failure stops further forwarding and exposes `Err()` to
the owner; it does not fall back to ordinary sockets. Close cancels dispatch and
reads before resource teardown; failed cleanup stays owned and retryable.
All driver and downstream emitters must honor cancellation and finish their
native buffer ownership before returning. A hung callback/kernel is not covered
by a false hard-bounded teardown claim.

`NewWindowsBackend` composes exclusive Winsock reservation, exact-port WinDivert
reply capture and physical forward injection. Capture is installed before the
first outbound packet; its handle closes before the port reservation is released.
Mandatory source/lease/host/LAN/Safety checks precede native injection. The native
bridge test uses five synthetic active leases sharing one inner UDP port and a
public DNS oracle, without exceptions to the mandatory destination deny list.
This proves real UDP translation and reply delivery, not encrypted downstream
admission, a ready Exit, TCP/ICMP parity or dual-role leak protection.

## Core lease resource adapter

`LeaseResources` implements the existing Core activator, renewer and revoker
interfaces. The caller supplies exact, already-authorized AWG peer install/remove
operations; no new lease protocol or admission policy is introduced. Forwarding
is enabled only after peer installation and a second validity check. Expiry or
cancellation during installation rolls back. Partial peer cleanup remains
tracked, blocks conflicting address reuse and consumes the configured capacity.
Renewal fences the complete previous lease revision; stale RELEASE cannot remove
a renewed peer. Revoke fences plaintext/NAT first, then removes the AWG peer.

Lifecycle operations are serialized separately from plaintext source lookup.
The peer-removal hook may wait for an AWG receive worker; that worker must still
be able to look up the revoked source and finish with a drop. Holding the packet
table lock while joining that worker would deadlock. The focused regression
models this wait in both RELEASE and shutdown. Reply delivery also receives a
two-second cancellable context, so a cooperative downstream emitter cannot keep
the engine lock indefinitely when its AWG reader stops.

`Outbound` is a trusted internal seam for plaintext that has already passed
AWG's cryptokey source validation, not an API for raw host/unauthenticated packets.
It selects only the active lease for the exact inner source; the NAT engine
rechecks the opaque handle after the lookup lock is released. The adapter is
not an authentication mechanism and must never create a lease on first packet.

The encrypted fixture composes real AWG transports, socketmux, this adapter,
Core lifecycle and the native bridge. Five test clients use channel TUNs and
test-coordinated loopback endpoints/keys; Windows NAT reaches a real public DNS
resolver. This is not yet the Windows daemon's production TUN path. Still needed:
native Wintun plaintext demultiplexing (including own upstream), exact in-tunnel
control exceptions, reply-buffer ownership/fencing across AWG replacement,
actual M12/Private authorization hooks and Windows Exit session-control wiring.
