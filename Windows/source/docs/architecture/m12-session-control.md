# M12 signed establishment and direct session control

M12 separates introduction from long-lived ownership. Rendezvous carries a
bounded end-to-end signed establishment transcript. Once ICE and AWG establish
the direct path, authenticated TCP+Noise inside that AWG tunnel owns lease
activation and maintenance. No application or maintenance bytes use
`ice.Conn`, and no additional UDP listener is created.

## Signed establishment

New direct and brokered establishment use the same transcript and validation.
The direct carrier is `/hydra/session-establish/1.0.0`; brokered messages travel
as opaque `SIGNAL` payloads over `/hydra/rendezvous/1.0.0`. The legacy
`/hydra/ice/1.0.0` path remains an explicit regression/compatibility path and is
never a silent fallback after brokered authentication fails.

Each `SignedSessionEstablish` signs the exact protobuf payload bytes with the
existing Hydra Ed25519 identity under the M12 session-establishment domain.
The common payload binds:

```text
protocol version and message kind
sender and recipient PeerID
random nonzero 128-bit AttemptID
owner-local random token
predecessor SHA-256 hash
created_at and expires_at
complete kind-specific body
```

Verification authenticates the signing key to the claimed PeerID, validates
the expected opposite PeerID, checks expiry/skew, validates the exact
predecessor chain, and hashes authenticated source bytes. A Rendezvous
`RemotePeer` is never substituted for the signed end peer.

The transcript is:

| Step | Authenticated bindings |
|---|---|
| PREPARE | Client and Exit PeerIDs, exact country intent/purpose, Exit descriptor ID/version, NetworkGeneration, runtime generation |
| READY | PREPARE hash, Exit signed descriptor and AWG epoch, fresh 256-bit challenge, Exit NetworkGeneration |
| OFFER | READY hash and challenge, both identities, ICE offer/credentials/candidates/roles, both AWG epochs, AWG profile hash, requested inner families, Client signed descriptor |
| ANSWER | OFFER hash, ICE answer, provisional TunnelGrant, exact Exit inner control endpoints, AWG profile hash, activation deadline |
| DECLINE/CANCEL | same attempt/predecessor authority and a bounded typed reason |

The ordinary M5/M10 validators remain authoritative for ICE descriptions,
roles, candidate counts, AWG key bindings, obfuscation profile, authorization,
and generation promotion. PRFLX is learned only by Pion connectivity checks;
it is not manufactured in signaling. The AWG endpoint still comes only from
the actual selected candidate pair.

The per-message wire limit is 48 KiB with a 40 KiB signed payload limit. The
broker additionally caps a whole attempt at 16 messages and 64 KiB. A default
transcript lifetime is 20 seconds, READY is bounded by the 1.5-second broker
policy/caller budget, and a lower layer never extends M9's remaining recovery
deadline.

## Replay and provisional allocation

PREPARE creates one bounded, expiring pending responder context and no full VPN
resource. READY is not capacity reservation. OFFER is accepted only with the
matching current attempt, owner token, challenge, predecessor, descriptor/AWG
binding, identities, and generations.

The Exit then uses the existing M10 allocator to create one provisional lease
whose session token is deterministically bound to the authenticated attempt
owner context. Exact duplicate payloads return the stored answer. Reusing the
same request identity with altered bytes is a conflict and cannot allocate
again. Provisional activation expires after at most 30 seconds and converges on
the existing M10 revoke/quarantine path. Cleanup failure never returns an
address to the allocator.

CANCEL can affect only its matching pending attempt. After authenticated BIND
and promotion, broker stream closure, late CANCEL, expired owner receipt, or
Rendezvous loss has no authority over the active lease.

## AWG bootstrap order

The client validates ANSWER, creates the new ICE agent/session, obtains the
actual selected pair, and verifies the Exit AWG key and epoch against the signed
descriptor. The staged dataplane then performs:

```text
install exact inner session-control routes
configure the real AWG peer and endpoint
wait for usable encrypted path
connect TCP over the inner address
complete libp2p Noise authentication
BIND the provisional lease
activate the lease on the Exit
install user default routes
promote the M9/M10 runtime session
```

Failure before promotion closes the staged AWG/ICE state and allows provisional
expiry/revocation. User routing is never installed before authenticated BIND.

## Noise carrier

The protocol is `/hydra/session-control/1.0.0`. M12 creates a normal TCP
connection across the existing AWG tunnel and applies the pinned libp2p Noise
transport directly to `net.Conn`; it does not create another libp2p host, DHT,
WAN listener, or UDP socket. Noise uses the protocol ID as its prologue.

Outbound `SecureOutbound` verifies the configured Exit PeerID. Inbound
`SecureInbound` authenticates the Client PeerID; no mutation is accepted before
that identity completes a matching BIND. PeerID written in a request is never
an authorization substitute. Frames are length-prefixed and capped at 16 KiB.

Supported operations are:

```text
BIND / BOUND
RENEW / RENEWED
STATE_QUERY / STATE
RELEASE / RELEASE_ACCEPTED
AWG_ROTATE / AWG_ROTATE_READY / AWG_ROTATE_COMMIT / AWG_ROTATED
ERROR
```

Every mutation carries a random request ID, lease ID, owner/session token, and
expected revision. Exactly one mutation runs per lease. A byte-identical retry
is checked for idempotency before revision rejection and returns the stored
result; altered reuse is rejected. `STATE_QUERY` resolves an uncertain reply.
REVOKING or REVOKED state cannot be resurrected by replay or by an older TCP
channel. `RELEASE_ACCEPTED` acknowledges ownership acceptance, not completion
of kernel cleanup. RELEASE delivery is intentionally best-effort after the
complete authenticated frame is written: loss of its response does not block
local teardown, and the Exit's bounded expiry converges through the same
revocation/quarantine path without releasing an address early.

The Exit returns authoritative remaining validity. The client subtracts request
elapsed time and a safety margin to form a conservative monotonic deadline; it
does not restart a full TTL when a delayed response arrives. The default active
lease TTL is the shared M10 policy (300 seconds for M12 production), with
renewal policy centralized in the existing renewer. Tests inject short TTL and
renewal intervals without changing the state machine.

If the inner TCP connection is lost while AWG remains alive, the reconnecting
client opens a fresh tunnel-only TCP+Noise connection and repeats BIND against
the same lease/revision. It neither obtains a new lease nor contacts Rendezvous.

## Planned AWG key rotation

An Exit operator requests a planned rotation with `SIGHUP`; the signal handler
delegates to the production runtime rather than mutating a device in `main`.
The runtime first reserves the exact active `(Client PeerID, lease ID, session
token)` relationship. This reservation fences new admissions before the local
AWG identity is durably advanced and a higher key epoch is published in a new
signed `NodeAnnouncement`. A failed identity write cancels the untouched
reservation. A failure after the durable epoch advances remains explicitly
fenced and requires reconciliation; Hydra never admits another relationship
against ambiguous old/new key ownership.

The Exit attaches the new signed binding and epoch to the next successful
`RENEWED`. The Client verifies the unchanged Hydra PeerID, signed announcement,
strictly newer epoch, and rollback registry before sending `AWG_ROTATE`. That
request carries the SHA-256 digest of the exact signed binding, the proposed
epoch, and the current authenticated lease ownership. The Exit validates it
without mutating the device and returns `AWG_ROTATE_READY` on the old path. The
Client stages the new peer with no AllowedIPs while the old peer remains the
sole cryptokey-route owner, then writes an exact `AWG_ROTATE_COMMIT` over that
old path. Only after reading the matching COMMIT does the Exit change its local
private key and arm a strict fresh-handshake baseline. The Client initiates and
the pinned AmneziaWG device owns the bounded retransmission timer; the Exit
waits passively. Hydra does not run a second external initiation loop because
that would race the pinned device timer and duplicate its retry policy. After
the fresh handshake, the Client atomically transfers AllowedIPs to the new peer
and removes the old peer. `AWG_ROTATED` and its renewed lease revision travel
over the new encrypted path.

The complete READY/COMMIT/handshake operation shares one caller deadline. Its
replay cache keys the original request bytes: exact retries cannot apply the
device or lease mutation twice, while altered/reordered COMMIT fields fail
before mutation. Lease expiry is fenced while the bounded key mutation runs;
success atomically renews the same lease and failure enters the existing safe
revocation/quarantine semantics rather than exposing a half-current lease.

The pinned AmneziaWG `SetPrivateKey` implementation recomputes every peer's
static-static secret and expires current keypairs. Staging and final transfer
retain the selected ICE endpoint, `HydraBind`, Mux port, TUN, and TunnelLease;
only the final serialized UAPI operation removes the old peer and assigns its
AllowedIPs to the already-handshaken replacement. The resulting handshake and
response therefore prove the new key owns the same direct encrypted path.
Rendezvous registrations are notified only after the new signed descriptor
exists, and an UPDATE is considered published only after the owner returns a
receipt for that exact descriptor version.

M12 intentionally permits this in-place private-key operation only for a pure
Exit with exactly one active downstream relationship and no upstream peer.
An AWG private key belongs to the generation-wide device, so a multi-peer or
dual-role rotation without a fleet-wide transition would break unrelated
sessions. Those cases return `BUSY` and retain the old working identity; M12
claims one representative supported live rotation, not a general multi-peer
rekey protocol.

## TUNNEL_CONTROL routing intent

M12 centrally reserves:

```text
HYDRA_TUNNEL_CONTROL_MARK = 0x48595443
hydra_tunnel_control table = 51822
rule priority = 9800
```

This is distinct from the WAN-only `HYDRA_BYPASS_MARK=0x48594452`. Client
routing table 51822 contains only source-explicit exact current lease `/32` and
`/128` routes through the actual `hydra0`, followed by family-specific
`unreachable` defaults. A client route uses its assigned lease address as the
source; an Exit route uses its actual inner listener address. This prevents a
dual-role host from selecting an unrelated downstream lease address for its
own upstream control connection. Deleting an exact route therefore fails
closed instead of consulting `main`.

Both dial and listener sockets receive the tunnel-control mark before route
selection and are also bound to `hydra0` with `SO_BINDTODEVICE`. Device binding
protects the listener's first SYN-ACK and prevents an already-open socket from
falling through to a physical device when an inner route disappears. The M11
cgroup rule only assigns WAN bypass to unmarked Hydra sockets and cannot
overwrite the nonzero tunnel-control intent.

ProtectedMode preserves both transport intents after the bootstrap `hydra_out`
rules become active. Its PREROUTING policy restores an exact mark only from the
already-selected Hydra-owned socket. Outer AWG replies restore
`HYDRA_BYPASS_MARK`, so crash-adoptable `src_valid_mark` ownership makes Linux
reverse-path validation use `hydra_wan`. Inner session-control replies restore
`HYDRA_TUNNEL_CONTROL_MARK`, so validation uses the exact current remote lease
route in table 51822. The table's unreachable default and `SO_BINDTODEVICE`
keep inner TCP confined to `hydra0`; a missing or stale exact route cannot fall
through to a physical WAN.

The Exit firewall allows INPUT only for the exact configured Exit inner
address, TCP session-control port, and `iifname hydra0`, before the ordinary
downstream-to-Exit isolation drop. Noise plus lease ownership remains required;
the firewall opening alone authorizes nothing. Other management/LAN endpoints
are not exposed. The M8 OUTPUT policy accepts this mark only on `hydra0` and
continues to route public accepted control replies with the separate M11 WAN
ownership mechanism.

Transport replacement and teardown reconcile the complete generation-owned
source/destination route set before closing bound sockets. Family guard rules
remain fail-closed until the routing owner itself closes. A stale generation
cannot reinstall routes after runtime fencing.

The privileged process acceptance independently opens a TCP socket carrying
the tunnel-control mark to the real Exit inner listener. It succeeds through
the exact `/32` route, fails immediately after that route is removed, and
succeeds again only after the exact route is restored. An nftables oracle on
the Client physical interface must remain at zero marked packets during the
fault. This checks the kernel-selected path and socket behavior rather than
only comparing the intended route/rule configuration.

## Healthy-session independence

After promotion, the existing M10 runtime owns ICE, AWG, lease, renewer,
session-control, TUN, and routing. The broker pending map owns none of them.
Renewal, state, rebind, release, bounded health, and supported planned AWG-key
rotation are direct operations. All Rendezvous processes, provider records,
owner registrations, and legacy WAN A-to-B TCP control connections may vanish
without terminating traffic or renewal.

An underlay oracle with no remaining target may report UNKNOWN, but a proven
live direct encrypted session remains positive evidence for its own maintenance.
If the direct ICE/AWG path is truly lost and new credentials are necessary,
fresh establishment may again require an introducer. M12 does not promise
recovery when every introducer and every direct maintenance path is gone.
