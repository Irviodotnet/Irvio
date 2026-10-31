# M12 scaling validation

M12 separates exact/property tests, a compact logical simulation, and an
authenticated implementation load test. None of these is presented as a
production SLA or a measurement of the other layers.

## Reproduction

```bash
make test-m12-scale
```

The target runs:

1. deterministic model tests;
2. a real `/hydra/rendezvous/1.0.0` server with 100 authenticated libp2p Exit
   registrations, 1,000 GET requests, and 100 brokered SIGNAL operations;
3. 144 deterministic logical cases with up to one million Exits, one million
   Clients, and 1,000 Rendezvous servers.

The raw model output is JSON Lines on stdout. Inputs, seed, per-case counters,
and assumptions are in each record; CI should retain stdout as its raw artifact.

## Compact model

`internal/rendezvous/scalemodel` stores only Exit-to-owner assignments, failed
server bits, and counters. It opens no sockets and performs no signature or
protobuf work. Each Exit chooses one to four distinct owner servers. Candidate
selection is deterministic pseudo-random sampling with replacement from the
configured country pool. A server failure is either a random set or a
contiguous server-ID failure domain. Success means at least one sampled Exit
has one surviving registration owner.

The cases compare:

- registration replication `R=1/2/3/4`;
- fixed `K=5`, fixed `K=10`, and progressive `5 -> 10`;
- independent and contiguous/correlated server loss at 10, 30, and 50 percent;
- a common pool of 1,000,000 Exit records and a rare fixture pool of 137;
- the same seed, `2037407826`, across each comparison.

Progressive acquisition emits a second logical GET/SAMPLE fan-out only for a
Client whose first five candidates have no surviving route. The model assumes
two SAMPLE operations per GET. It does not model DHT partial views, geographic
misclassification, malicious sampling, libp2p, cryptography, queueing, network
latency, or real process capacity. In particular, its random local sample is
not evidence of globally uniform distributed sampling.

## Observed logical results

On 2026-09-06 the full 144-case run completed in 2.18 seconds. Representative
common-pool, independent-failure observations were:

| R | Acquisition | Failed R | Clients without a surviving owner |
|---:|---|---:|---:|
| 1 | fixed 5 | 10% | 15 / 1,000,000 |
| 1 | fixed 5 | 30% | 2,450 / 1,000,000 |
| 1 | fixed 5 | 50% | 31,742 / 1,000,000 |
| 1 | fixed 10 | 50% | 994 / 1,000,000 |
| 2 | fixed 5 | 50% | 983 / 1,000,000 |
| 2 | fixed 10 | 50% | 1 / 1,000,000 |
| 3 | fixed 5 | 50% | 24 / 1,000,000 |
| 3 | fixed 10 | 50% | 0 / 1,000,000 |
| 4 | fixed 5 | 50% | 1 / 1,000,000 |
| 4 | fixed 10 | 50% | 0 / 1,000,000 |

The v1 production maximum remains three registrations; `R=4` exists only as a
comparison requested by the scaling plan. For `R=1`, 50 percent independent
loss, progressive 5-to-10 produced the same 994 no-route results as fixed ten,
but only 1,031,742 logical GETs instead of assuming a top-up for every Client.
Its derived per-server refresh load was 17.196 GET/s and 34.391 SAMPLE/s.

At the reference `E=C=1,000,000`, `S=1,000`, `R=3`, heartbeat 30s, and refresh
60s, the configured averages are 3,000 registrations/server, 100
heartbeats/s/server, 16.67 GET/s/server, and approximately 33.33 SAMPLE/s/server
before skew, partial results, retries, ACKs, or top-up. These are arithmetic
workload inputs, not measured server capacity.

Finite zero-failure observations do not establish a zero failure probability.
Results depend on the stated assignment/failure model and seed; comparisons
should use multiple retained seeds before changing production policy.

## Authenticated implementation load

The gated `TestM12AuthenticatedRendezvousLoad` uses real pinned libp2p TCP and
Noise hosts, real signed NodeAnnouncements/receipts, length-prefixed protobuf
handlers, the country index, and actual registration and signaling streams.
The admission intervals are accelerated only to generate the load; caps,
verification, state machines, and carriers are production implementations.

Hardware for the recorded run:

```text
AMD Ryzen 9 7945HX, 16 cores / 32 threads
approximately 63.5 GB RAM
Linux x86_64
```

Observed mixed GET+SIGNAL latency and process state:

```text
registrations=100, fresh=100
GET=1000, SIGNAL=100
P50=1.211735ms, P95=1.644187ms, P99=2.448329ms
HeapAlloc=46,453,824 bytes
Heap delta during requests=16,437,096 bytes
GC cycles during requests=17
open FDs=310, FD delta during requests=0
elapsed test time=1.74s
```

This validates the real protocol path and bounded FD behavior at a lightweight
load point. It is not a 10,000-registration capacity benchmark, does not model
Internet RTT, and does not establish an SLA. Production sizing still requires
longer multi-process tests on deployment hardware with RSS, connection limits,
kernel socket memory, and adversarial overload included.
