# M11 Public Node Foundation and Dual Role

M11 adds three narrowly scoped properties to the production `hydrad` runtime:
one daemon can be a protected client and an Exit at the same time, accepted
Hydra TCP control traffic remains on the physical WAN under ProtectedMode, and
public-control capability is published only after independent inbound
verification. It does not add Rendezvous, server presence synchronization,
Sybil resistance, reputation, or a public UDP/STUN capability.

## Dual-role ownership and routing

A dual-role process has one `TransportOwner`, one current
`TransportGeneration`, one `HydraSocketMux`, and one Exit-owned long-lived
`hydra0`/AmneziaWG transport:

```text
Node A local application
  -> local-output rule -> hydra_out -> hydra0
  -> upstream AWG peer C -> Exit C WAN

Client B decrypted on A
  -> iif hydra0 rule -> hydra_wan -> A physical WAN
```

The Exit owns `hydra0`; the upstream client session borrows that device and
adds its assigned /32 and/or /128 plus exactly one catch-all AWG peer. Downstream
clients remain individual /32 and /128 cryptokey routes on the same AWG
transport. Closing or reconnecting the upstream removes only the catch-all
peer, its lease addresses, and its client routing transaction. It does not
close downstream peers, the Exit firewall, the mux, or `hydra0`.

The new policy rule is ordered after the marked-outer rule and before ordinary
protected output:

```text
9900+  explicit bypass  -> main
10000  fwmark 0x48594452 -> hydra_wan
10050  iif hydra0       -> hydra_wan
11000  unmarked local   -> hydra_out
```

The existing Exit firewall remains authoritative: `hydra0 -> hydra0` is
dropped, Exit-local/private destinations are isolated, and only
`hydra0 -> configured WAN` is forwarded and NATed. Thus B cannot be forwarded
through A's upstream peer C. An upstream-C outage leaves M8 protection active
for A's local applications while B's forwarded flows can continue through A's
healthy physical WAN.

Network migration updates both the default and directly connected routes in
`hydra_wan` with the new preferred source before new control verification or
transport work begins. Exit NAT44/NAT66 rules are replaced atomically with an
explicit generation-selected SNAT address; this avoids retaining an old
primary-interface address during make-before-break renumbering. A failed route
replacement rolls the NAT address back, while a failed nftables batch leaves
the previous atomic policy intact.

A same-key remote AWG device replacement cannot reuse the client's old Noise
session: endpoint promotion explicitly expires the prior keypairs, starts a
fresh upstream-supported handshake, and waits for a handshake timestamp newer
than the previous one. This closes the case where ICE had migrated correctly
but encrypted packets still used keypairs known only to the destroyed remote
device.

## Accepted libp2p control under ProtectedMode

Pinned go-libp2p exposes an outbound TCP dial hook but no equivalent hook for
accepted sockets. M11 retains explicit `SO_MARK` on all previously marked
sockets and closes the accepted-socket gap with a dedicated cgroup-v2 process
identity:

```text
dedicated hydrad cgroup
  -> nftables inet route/output hook at mangle priority
  -> socket cgroupv2 identity match
  -> meta mark set 0x48594452
  -> policy reroute through hydra_wan
```

The match is ownership-based, not destination-based. A per-data-directory hash
creates stable, collision-bounded cgroup and nftables names. The runtime rejects
a live foreign member or an unowned table. Atomic nftables replacement permits
crash adoption; graceful close moves the daemon back to its parent cgroup and
removes only its own table and empty child cgroup. It never marks all root
traffic or all traffic from an interactive UID. A normal application remains
outside the hydrad cgroup and cannot inherit the bypass.

The route hook is used deliberately: a mark applied only in a later filter hook
would be too late to select the physical routing table. Privileged acceptance
uses packet/source observations, not rule text alone, to prove that a new
inbound authenticated connection receives its reply directly from A while an
ordinary flow from A to the same literal IP follows A's upstream Hydra Exit.

## Public-node state

`PublicNodeRequested` is user consent to verify, never proof. Each address
family has a fenced controller state:

```text
DISABLED -> VERIFYING -> VERIFIED
                      -> UNVERIFIED / EXPIRED
```

IPv4 and IPv6 capabilities are independent. `PUBLIC_CONTROL_V4` and
`PUBLIC_CONTROL_V6` do not imply `CAP_EXIT`; a NAT-traversed peer can still be
an Exit. TCP verification does not imply public UDP or STUN service.

The DHT continues using the configured pinned mode (`server` by the current
deterministic default, or `auto`). Verification controls signed public endpoint
advertisement; it does not recreate the libp2p host or pretend to dynamically
change an upstream DHT implementation mode that has no safe public setter.

## Verification protocol

`/hydra/reachability/1.0.0` is a bounded authenticated request/attestation
protocol. `/hydra/reachability/challenge/1.0.0` is its one-shot reverse
challenge. Frames are length-bounded to 4096 bytes.

1. Subject X opens an authenticated outbound libp2p connection to verifier V.
2. X supplies its PeerID, random 128-bit AttemptID, family, externally
   configured TCP port, NetworkGeneration, and monotonic public epoch. It does
   not supply a trusted IP.
3. V obtains X's source IP from the authenticated connection. Production policy
   rejects unspecified, loopback, link-local, multicast, private, and
   documentation/test endpoints, carrier-grade NAT space, benchmarking space,
   and other reserved non-public ranges. A separate explicit lab policy permits
   isolated private and documentation/test prefixes.
4. V builds a target only from that observed IP and X's advertised port. This
   supports static PAT where external and listener ports differ and prevents
   arbitrary-target scanning/SSRF.
5. A newly constructed no-listen libp2p host with V's same Hydra identity opens
   a fresh TCP connection to the target. The connection must authenticate as X.
   Existing X-to-V connection reuse cannot satisfy the proof.
6. V sends AttemptID, a fresh 256-bit nonce, and its PeerID. X replies only for
   the expected, unexpired, unused verifier/attempt pair. The client accepts an
   attestation only if this reverse challenge was actually consumed.
7. V signs the bounded attestation with its Hydra Ed25519 identity.

The subject also uses a fresh no-listen host for each verifier request, pinned
to the requested address family. This prevents an already-open IPv4 libp2p
connection from being reused as apparent evidence for an IPv6 request (or the
reverse). Explicit socket marks remain in place for both request and challenge
dials.

Global, per-PeerID, and per-observed-IP work is bounded; attempts expire and
are single-use. The challenge echoes only fixed protocol fields, so it is not a
proxy, scanner, or useful reflection service.

## Quorum proof, advertisement, and expiry

The initial policy requests at most three verifier PeerIDs and requires two
distinct valid signatures. The subject cannot verify itself and duplicate
PeerIDs count once. Every proof fragment must agree on subject, family,
externally observed `IP:port`, NetworkGeneration, and public epoch. This is a
practical reachability quorum, not an organizational/ASN/Sybil guarantee.

The combined proof and endpoint are embedded in the signed NodeAnnouncement.
Announcement validation re-verifies each attestation signature and quorum;
capability bits without a proof, proof endpoints without the matching bit,
mixed generations, stale epochs, tampering, and expired attestations are
rejected. Only currently verified endpoints are advertised.

Proofs are leases. Reverification starts before expiry; failure removes the
advertisement conservatively. Renewal is jittered before the configured lead
time so many public nodes do not synchronize their verifier traffic. Every
semantic NetworkGeneration change
immediately invalidates the proof and starts a fresh public epoch. Periodic
reverification also detects public-NAT IP changes that Netlink cannot see.
Disabling public intent fences pending work, clears the proof, and leaves the
node's client and Exit roles intact.

Reachability proofs are transient public metadata, not permanent identity
state. They contain no LAN addresses, TunnelLease, traffic history, or private
key material. A future Rendezvous can validate them without trusting a boolean
claimed by the subject.

## Acceptance boundary

The static-PAT process test launches real `hydrad` processes. Two of three
independent verifier PeerIDs observe the NAT's external documentation address,
dial its configured external port, traverse DNAT to a private listener, perform
libp2p authentication and complete the nonce challenge. nftables counters prove
fresh inbound PAT and cgroup marking. An ordinary unmarked process to the same
verifier IP is blocked. Removing PAT causes re-verification to revoke the
capability.

The dual-role process test launches two downstream clients, A, C, three
verifiers, bootstrap and a deterministic Internet. Internet-observed sources
prove A's local application uses C's WAN while both downstream clients use A's
WAN and retain distinct leases. C's NAT counter does not change during their
flows, independently proving no B-to-A-to-C forwarding. A WAN address change
advances NetworkGeneration, preserves the same `hydra0` and logical leases,
reconnects the upstream and downstream sessions, and replaces the public proof
with the newly observed endpoint. Marked on-link control, default-route outer
traffic, and downstream SNAT all use that generation's selected WAN source.
Closing one downstream client leaves the
other roles intact. When C dies, A's local traffic stays fail-closed,
downstream traffic continues through A, and fresh inbound public verification
remains usable through A's physical WAN. A separate newly started Hydra process
also bootstraps directly through A and completes a real DHT provider and signed
announcement request, rather than treating TCP reachability alone as DHT proof.

M11 intentionally leaves production Rendezvous, heartbeat/presence, global
server synchronization, verifier diversity/Sybil policy, automatic router port
mapping, public UDP/STUN proof, privilege separation, GUI/Windows, and the
shared-engine independent STUN-refresh limitation to later work.
