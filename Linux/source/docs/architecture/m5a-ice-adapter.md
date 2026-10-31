# M5a Pion ICE adapter ownership

M5a adapts Pion ICE host-candidate and ICE-control machinery to the physical
UDP ownership domain established by `HydraSocketMux`. It does not perform
peer-to-peer ICE negotiation or NAT traversal.

## Ownership

```text
one UDP4 socket       one IPv6-only UDP6 socket
        \                    /
             HydraSocketMux
              /           \
        Control            Data
           |
    one ICE dispatcher
           |
    Hydra ice.UDPMux
           |
       Pion Agent
```

`HydraSocketMux` alone opens and closes operating-system sockets. The adapter
receives only `Mux.Control()`, and Pion receives only logical bounded
`net.PacketConn` references. Neither a logical connection, the adapter, nor a
Pion Agent closes the mux. Non-ICE writes through those logical connections
return `ErrNonICEPayloadUnsupported`; future application traffic belongs to
`Mux.Data()` and M6 HydraBind.

The adapter accepts an explicit bounded list containing at most one concrete
address per active family. Every advertised listen address uses `Mux.Port()`.
Multiple addresses in one family are deferred because the current mux receive
metadata does not expose the local destination address needed to demultiplex
them safely.

## Control phases

Exactly one high-level component reads `Mux.Control()`:

```text
M3b/M4 STUN and profiling phase
        |
        | close stunprobe.Client
        v
M5 ICE phase
        |
        | close iceadapter.Adapter
        v
M6 application Data phase
```

An immutable classifier chain is installed when both STUN and ICE classifiers
are needed across the handoff. `Control` and `Drop` are terminal decisions;
`Data` means that a classifier did not claim the datagram. Phase ownership,
not the chain, prevents concurrent Control readers.

## Routing and bounds

Incoming Binding Requests are strictly decoded with Pion STUN and require one
well-formed USERNAME whose first component is an active local ufrag for the
packet family. Their source endpoint need not already be known. Pion later
validates ICE credentials and MESSAGE-INTEGRITY.

The logical `WriteTo` path strictly decodes outbound Binding Requests, records
their Transaction ID, local ufrag, exact remote endpoint, and family before
calling `Mux.Control().WriteTo`. Matching responses reach Control and are
routed to that ufrag. A wrong source, wrong family, or unknown transaction is
unclaimed Data. Transactions retired by expiry, ufrag removal, send failure,
or shutdown remain in a bounded time-limited quarantine whose exact matching
late responses are dropped.

Registry ufrags, active transactions, retired transactions, and per-connection
receive queues all have configured finite limits. The single dispatcher never
spawns a goroutine per datagram and never blocks on a full logical queue.

## Pion policy and validation

M5a constructs a real Pion ICE v4.4.1 Agent using `WithUDPMux`. Candidate
gathering is explicitly restricted to host candidates over UDP4 and UDP6.
mDNS, TCP, Pion STUN/srflx gathering, and relay/TURN candidates are disabled.

Loopback tests and the privileged Linux namespace laboratory prove both:

- gathered host candidate ports equal `Mux.Port()`;
- `/proc/net/udp{,6}` or namespace `ss` shows no additional Pion listener;
- a Pion STUN-encoded outbound ICE Binding Request is observed at the peer with
  source port `Mux.Port()` for both families.

The test does not exchange candidates, call Dial or Accept, select a pair,
perform hole punching, or send application data through an ICE connection.
Those concerns remain in M5b/M5c, while AmneziaWG integration remains in M6.
