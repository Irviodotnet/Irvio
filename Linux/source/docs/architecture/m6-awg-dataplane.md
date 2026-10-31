# M6 AmneziaWG Data Plane over HydraSocketMux

M6 is the first encrypted application-packet proof. It embeds the official
AmneziaWG userspace implementation but does not install a production TUN,
route, firewall, DNS policy, or Internet exit.

## Physical ownership

```text
                         TransportGeneration N
                                  |
                           HydraSocketMux
                     one UDP4 / one IPv6-only UDP6
                          /                 \
                 Mux.Control()           Mux.Data()
                       |                      |
                shared ICEEngine          HydraBind
                       |                      |
                Pion ICE Agents       AmneziaWG device
```

Only `HydraSocketMux` owns operating-system UDP sockets. Pion uses the M5a
logical `ice.UDPMux`; AmneziaWG uses `awg.HydraBind`. Neither receives a raw
`net.UDPConn`, and neither closes the mux. `HydraBind.Open` returns one receive
function and the existing `Mux.Port()` without calling any socket constructor.
Its receive side consumes the bounded mux Data queue. Its send side preserves
the upstream batch order while writing every datagram through the mux's
family-correct physical socket.

The injected socket classifier remains protocol-derived rather than AWG-byte
derived. Valid registered ICE STUN is Control, retired ICE responses are Drop,
and encrypted AWG handshake/transport packets are ordinary Data. Real-wire
tests capture every datagram from a successful AWG exchange and prove none was
claimed as Control. A saturated bounded Data queue drops Data without blocking
or starving a valid active-ufrag ICE Binding Request.

## Dependency and startup boundary

M6 directly pins:

```text
github.com/amnezia-vpn/amneziawg-go/v3 v3.1.20260828
upstream commit b5928efb6ca19f0153958460c3d141f04abc5c2e
```

The dependency preflight found upstream issue
[#168](https://github.com/amnezia-vpn/amneziawg-go/issues/168): a first
transport packet can be prepared before the eventual S4 padding configuration
is active. The pinned code still has that ordering. Hydra does not hide it:
M6 rejects nonzero S4 with `ErrTransportPaddingUnsupported`. This is a
conservative compatibility boundary, not an alternate cryptographic
implementation.

All other AWG settings and every peer are written through the upstream UAPI
before `Device.Up`. Tests use a real nontrivial, explicitly non-secret fixture:
Jc=1, Jmin=Jmax=24, S1=16, S2=16, S3=8, S4=0, H1=101, H2=202, H3=303,
H4=404, and one static signature packet. Only after configuration and `Up`
does the test trigger a handshake; application packets are injected only after
both peers report a completed handshake.

AmneziaWG Curve25519 transport keys remain separate from Hydra's persistent
Ed25519/libp2p identity. Tests use deterministic non-secret fixture seeds;
production callers must supply private configuration and the remote public key.
Logs and retained namespace artifacts include no private key or ICE password.

## Endpoint authority and generation fencing

The sole endpoint authority is the selected Pion candidate pair:

```text
iceconnect.Result.SelectedRemoteCandidate + RemoteEndpoint
       -> validate PeerID, family, type and exact IP:port
       -> validate NetworkGeneration and SessionGeneration
       -> immutable awg.SelectedEndpoint
       -> peer UAPI endpoint
```

HOST, SRFLX, and dynamically learned PRFLX remote candidates are accepted. A
stale signaled candidate, DHT record, previous selected pair, or prior transport
generation cannot update a peer. Same-generation reconnect requires a strictly
newer SessionGeneration. Equal-generation idempotence is allowed only for the
same endpoint. A NetworkGeneration mismatch is always rejected.

One `awg.Transport` owns one generation-scoped AmneziaWG device and HydraBind.
It may host a bounded set of peers. Adding, removing, or updating one peer does
not recreate the bind, mux, shared ICE engine, or unrelated peers. The
TransportGeneration installs at most one data plane and closes it before its
ICE engine and physical mux. A stale generation cancels an in-progress data
plane factory and closes any late result.

M6 originally left nonzero `SetMark` unsupported. M7 completes that boundary:
`HydraBind.SetMark` now delegates to `HydraSocketMux`, which applies `SO_MARK`
to its actual UDP4/UDP6 descriptors before protected policy routing is used.
No descriptor is exposed and no replacement socket is created.

## Encrypted packet proof

The test-only bounded channel TUN supplies complete inner packets without
changing host routing:

```text
channel TUN A -> real AWG encryption -> HydraBind A -> Mux A
             -> UDP path selected by ICE
             -> Mux B -> HydraBind B -> real AWG decryption -> channel TUN B
```

Both directions are verified for inner IPv4 and inner IPv6. Cross-family cases
cover outer UDP4 with inner IPv6 and outer UDP6 with inner IPv4. The selected
outer family is independent of the inner packet family. No test calls
`ice.Conn.Read`, `ice.Conn.Write`, or `WriteToPair`; Pion's application packet
and byte counters remain zero while AWG counters increase.

Privileged tests cover:

- direct IPv4 HOST/HOST, direct IPv6 HOST/HOST, and dual-stack selection;
- public initiator to EIM+APDF NAT responder;
- EIM+APDF to EIM+APDF simultaneous hole punching;
- EIM+EIF to APDM+APDF, where AWG must use Pion's learned PRFLX port rather
  than the different signaled SRFLX port.

The helper records the UDP inode immediately after mux creation, after the STUN
phase, after ICE connectivity, and during AWG. Exactly one namespace UDP socket
per enabled family remains visible. Existing independent responder observations
and scoped nftables source-port/mapping counters remain the network oracle.

The multi-peer proof gives one Exit device/bind/mux three peers and distinct
inner prefixes. Bidirectional distinguishable traffic reaches only its intended
peer. A fresh same-network M5b/M5c SessionGeneration updates one endpoint and
encrypted traffic resumes without replacing the bind or mux. The other peers
continue to exchange packets. A full TransportGeneration migration creates a
new mux, fresh STUN/ICE state and new generation-owned AWG device, exchanges
encrypted packets with two peers, promotes the new generation, and closes the
old data plane.

## Commands and boundaries

```bash
make test-m6
make test-m6-netns
make test-m6-soak
make clean-m4b
```

`test-m6-soak` executes 100 race-enabled AWG establish/send/close cycles, 25
three-client Exit cycles, 25 full same-network ICE reconnect/update cycles, 10
transport-generation AWG migrations, and 10 privileged iterations of every
direct and controlled-NAT/PRFLX scenario.

All binaries are built as the normal user. Only the prebuilt network-namespace
runner uses sudo. Host links, addresses, routes, nftables, and forwarding
sysctls are normalized before and after; cleanup is transactional and
idempotent.

M6 does not solve production libp2p reachability between NATed peers, endpoint
or Exit selection, peer disappearance policy, reconnect policy beyond the
generation-fenced transport hook, production TUN, routing, SNAT, DNS, kill
switch, Internet forwarding, TURN, relay, TCP/trickle ICE, or mobile behavior.
Those remain later milestones, primarily M7 for host networking.
