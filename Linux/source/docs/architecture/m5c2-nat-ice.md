# M5c2 controlled IPv4 NAT ICE connectivity

M5c2 validates direct Pion ICE connectivity through controlled IPv4 NAT while
HydraSocketMux remains the exclusive physical UDP owner. It is a system test of
the existing STUN, signaling, and ICE chain, not a general NAT-traversal claim
or an application data plane.

## Separate control and data networks

Each run creates fresh disposable namespaces and two isolated bridges:

```text
authenticated libp2p TCP control bridge
        A control ---------------- B control

Hydra UDP data path
 A private -> NAT A -- external bridge -- NAT B <- B private
                         |
                    RFC 5780/STUN
```

Libp2p binds only to `192.0.2.1`/`192.0.2.2` on the control bridge.
HydraSocketMux binds only to the data address. Candidate assertions reject both
control addresses, private subnets have no direct route to each other, and NAT
tests enable UDP4 only. The control bridge therefore cannot rescue an ICE
check or bypass NAT44.

M5c2 uses this separate test-only routable control network for authenticated
libp2p signaling. Production signaling reachability between two NATed peers
remains unsolved by M5c2 and must not be confused with successful UDP
data-path ICE traversal.

## Same-socket phase chain

Each peer creates one HydraSocketMux, then runs:

```text
STUN lease -> Basic Probe -> close stunprobe -> release STUN lease
           -> ICE lease -> host gathering -> authenticated Offer/Answer
           -> AddRemoteCandidate -> Dial/Accept -> selected pair
```

The test helper records the process-owned `/proc/net/udp` inode after STUN and
again after pair selection. Equality plus an exact one-socket namespace count
proves that the physical socket was neither reopened nor supplemented by
Pion. Router and public-peer nftables counters independently require the
internal source port to equal `Mux.Port()` and require the wrong-port counter
to remain zero. The external port is allowed to differ.

On close, the helper requires zero active adapter ufrags/transactions,
reacquires the STUN phase, and writes through `Mux.Data()` before the mux is
finally closed.

## Controlled mappings and filtering

The external network uses `198.18.90.0/24`; the STUN server owns
`198.18.90.100` and `198.18.90.101`. Fixed mappings make the oracle independent
of the production ICE code:

| Peer | STUN mapping | APDM peer mapping |
|---|---|---|
| A | `198.18.90.10:51011` | `198.18.90.10:51012` |
| B | `198.18.90.20:52011` | `198.18.90.20:52012` |

EIM uses the STUN port for both STUN and peer destinations. APDM uses the
different peer port. EIF explicitly forwards new inbound packets to the
Hydra port. APDF permits conntrack-established reverse traffic and drops other
inbound traffic after DNAT. Every rule is limited to one peer address,
`Mux.Port()`, the STUN/peer addresses, UDP, and the listed test ports.

The required outcomes are:

| Scenario | Mapping/filtering | Expected outcome |
|---|---|---|
| A | EIM+APDF NAT initiator to public peer | connected |
| B | public initiator to EIM+APDF NAT responder | connected |
| C | EIM+APDF NAT to EIM+APDF NAT | simultaneous hole punch connected |
| D | EIM+EIF NAT to APDM+APDF NAT | connected using learned B `:52012` PRFLX |
| E | APDM+APDF NAT to APDM+APDF NAT | bounded expected-unreachable |

For D, B signals its real STUN mapping at `:52011`, sends peer traffic from
`:52012`, and A initially has no `:52012` candidate. Pion validates the
unknown-source ICE request and inserts a peer-reflexive remote candidate. The
result requires Pion's candidate type to be PRFLX and its endpoint to equal the
nftables mapping oracle. Hydra never constructs or signals PRFLX.

For E, each peer sends from its peer-specific mapping to the other's stale
STUN mapping. APDF drops those non-reverse tuples. The test accepts failure
only when both peer-SNAT and inbound-drop counters are nonzero, both Pion calls
terminate within context, and neither side reports a selected pair.

A separate wrong-SRFLX fault case first records the real M3b observation and
then replaces only the candidate port in the test helper. A narrow outbound
drop prevents peer-reflexive recovery in that case, proving bounded failure is
caused by the incorrect signaled endpoint rather than an accidental direct
path.

## Evidence and boundaries

Every mandatory success uses a real M3b Basic observation, M5b detached SRFLX
construction and authenticated protobuf exchange, M5c candidate loading, and
blocking Pion Dial/Accept. Pion remains the checklist, integrity, nomination,
and PRFLX authority. M4 behavior labels are never used to skip or alter ICE.

Selected candidate snapshots support HOST, SRFLX, and dynamically learned
PRFLX for bounded diagnostics. The M5b protocol continues to accept only HOST
and SRFLX. No ICE password, private key, or secret-bearing Offer/Answer is
written to artifacts. Successful results require nominated/succeeded pair
statistics, nonzero STUN check traffic, an increase in Hydra adapter outbound
transactions, and zero application packets/bytes.

Run:

```bash
make test-m5c2
make test-m5c2-netns
make test-m5c2-soak
make clean-m4b
```

Privileged evidence is retained beneath `/tmp/hydra-m4b-<runid>/`. Go tooling
runs as the normal user; only the prebuilt lab runner runs through `sudo`.
Host addresses, routes, nftables, and forwarding sysctls are compared before
and after every runner invocation.

M5c2 does not use TURN, relay, TCP ICE, trickle ICE, double NAT, reconnect, ICE
restart, or application `ice.Conn` reads/writes. M5c3 owns later network-change
and recovery policy. M6 owns HydraBind, AmneziaWG, routing, and the encrypted
application data plane.
