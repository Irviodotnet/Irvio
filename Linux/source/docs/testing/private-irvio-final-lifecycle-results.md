# Private IRVIO — final preview lifecycle results

Mini-PC artifact `/tmp/hydra-m4b-2410079c`, unit `irvio-private-lifecycle-01cfb88-r1.service`. The immutable terminal `results.json` reports start `2026-10-06T21:11:49.916907394Z`, completion `2026-10-06T21:30:11.672315477Z`, exit 0 and `host_integrity=true`. `TestPrivateRuntimeLifecycle100` passed in 1101.70 seconds.

Packaged daemon SHA-256 was rechecked after completion: `99e0ebe41e39834810fed72d9dc26e629b421230259b307a46222774a93cd0a3`. Runner SHA recorded at launch: `23746842b370821a087e4cd309c5bd481cdeebfd1afba9e76ba3ffc594f63cea`. Package provenance is in `private-irvio-final-preview-01cfb88.md`.

## Resources

All 101 idle samples, including cycle zero, have FD=9, pipes=2, sockets=2 and AF_NETLINK=0 for both processes. Host threads range 11–14; client threads remain 12.

| RSS (KiB) | Host | Client |
| --- | ---: | ---: |
| Initial | 39828 | 41172 |
| Median | 118164 | 91832 |
| Maximum | 121844 | 120808 |
| Last | 107440 | 85940 |
| Median cycles 20–40 | 117984 | 92516 |
| Median cycles 80–100 | 118136 | 114700 |

Host steady-window median changes about 0.13%; client about 23.98%. Client RSS is not monotonically increasing: the final sample falls to 85940 KiB, and late GC traces show retained heap dropping from 45 to 23 MB before returning to 44 MB. This supports bounded behavior in this run and excludes the earlier FD/netlink/pipe accumulation signature; it is not a heap-profile proof of absence of every memory leak.

## RELEASE and cleanup

The strengthened oracle rejects unconfirmed RELEASE before intentional revocation. All 100 cycles passed it. Exactly one `TUNNEL_RELEASE_UNCONFIRMED` remains in the client log, at `00:30:11 +03:00`, after the test's intentional membership revocation logged at `00:30:01 +03:00`. It is not a normal-disconnect acknowledgment failure.

The test checks per-cycle return to the saved network baseline and remote lease removal, plus real user traffic, revocation, fail-closed probes and final cleanup. Final host integrity passed. DNS in this particular resource campaign remains isolated/synthetic; actual host DNS evidence is separately documented.

## Separate multi-client gate

The queued multi-client unit started after this successful cleanup. Its first selective-revocation case failed before revocation: the host reported one client where two were expected. The full campaign was still running when this report was written. Do not merge its result with the passing 100-cycle gate or classify the failure without examining both client states and leases. No product fix is inferred from that count alone.
