# WAN autodetection: own bootstrap address regression

Validation date: 2026-09-30. Result: **PASS for the focused WAN fix and Prague deployment**.

## Baseline and scope

- Branch: `main`.
- Baseline HEAD: `124a8a1db72ccf4d2ff7ff48c24d15799419cc47`.
- Go: `go1.27.1 linux/amd64`.
- Workstation kernel: `7.0.0-34-generic`.
- Previous Prague daemon SHA-256: `188100dd18e36351a19a311366921137a002e54d24ac64eed345da053cf68bf8`.
- Source changes: `internal/productruntime/underlay_linux.go` and its tests.
- No wire, bootstrap format, M11 quorum, ICE/AWG, TunnelLease or Safety Policy changes.
- No IRVIO networking started on the workstation. Production deployment changed only Prague; other servers and the mini-PC daemon were not upgraded.
- No commit or tarball rebuild performed in this task. The new daemon records a modified source tree on the baseline HEAD.

## Cause and fix

Kernel lookup of the server's own public bootstrap IPv4 returns an `RTN_LOCAL` route through the loopback interface, with a public preferred source. Previously this passed validation and could configure Exit forwarding/NAT on `lo`.

The resolver now accepts only `RTN_UNICAST` routes and rejects `net.FlagLoopback` interfaces and the name `lo`. Existing checks for unavailable links, the HYDRA tunnel, source address and address family remain intact. `ResolveBootstrapUnderlay` already retries subsequent endpoints after an error; no new selection algorithm was added.

A pool containing only the server's own address fails explicitly rather than selecting loopback. Normal unicast routes remain valid, including on-link IPv4/IPv6 routes without a gateway.

## Regression evidence

Before the fix, `TestUnderlayPathRejectsLocalRoutesAndLoopbackLinks` failed for all five cases: own public IP, loopback flag, loopback name, a local route on a physical link, and a broadcast route.

After the fix all cases pass. Existing kernel-selected route tests and new positive on-link IPv4/IPv6 cases also pass.

An opt-in read-only kernel test was compiled on the workstation and executed as root on Prague:

```sh
IRVIO_TEST_UNDERLAY_SELF_IPV4=194.87.52.164 \
IRVIO_TEST_UNDERLAY_REMOTE_IPV4=78.17.209.41 \
  /var/tmp/irvio-productruntime-wan-test \
  -test.run 'TestResolveBootstrapUnderlaySkipsOwnAddressRealKernel|TestUnderlayPath' -test.v
```

Observed: own address rejected; next endpoint selected via `eth0`, source `194.87.52.164`. Self-only pool rejected. The test performs native netlink reads without modifying host networking.

## Build artifacts

```sh
CGO_ENABLED=0 go build -o bin/wan-route-fix/irviod-normal ./cmd/irviod
CGO_ENABLED=0 go build -trimpath -o bin/wan-route-fix/irviod ./cmd/irviod
```

- Normal SHA-256: `21a782c148fca415b692a941766ae3b21ae6044eea809590873fb38985a13a6c`.
- Trimpath/deployed SHA-256: `a2501c9ff8080660d8918723525025fdfbd83b89fea6c6c88ed117516ebb5a16`.
- Canonical workstation binary `bin/irviod` was updated to the tested trimpath build, without launching it.

## Repeated deployment on Prague

Server: `194.87.52.164`.

The permanent unit retains only product flags:

```text
/usr/local/bin/irviod --role server --bootstrap <bundle>
```

For reproduction, the bundle is the original three-node bundle containing Prague, Helsinki and Amsterdam. Its first canonical entry is Prague itself; the prior self-exclusion workaround was deliberately removed. The client retains its four-node production bundle. No `--exit-wan` or other engineering override was used.

New release: `/opt/irvio/releases/irvio-1.1.0-alpha.20260930-wan-route-fix-a2501c9f`.

The old release was preserved. Existing UI/GeoIP assets were copied unchanged, the daemon replaced, and the new release checksum manifest verified. `WAN_FIX_BUILD_INFO.json` supersedes the inherited tarball's daemon provenance. The pre-change systemd unit is backed up under `/var/backups/irvio/wan-route-fix-20260930`.

Observed after startup:

- One `irviod` process, service active, `NRestarts=0`.
- Public reachability verified; Rendezvous active; Exit ready with country `CZ`.
- Signed announcement validated with the existing M11 proof policy: capabilities `163`, new sessions accepted, independent verifier attestations from Helsinki and Amsterdam.
- Three remote Rendezvous registrations active.
- Exit forwarding and NAT use `eth0`, not `lo`.
- Pool retained: `10.113.211.0/24`; identity retained.

## Independent mini-PC user-plane proof

The existing mini-PC client connected to country `CZ` using real discovery, ICE/AWG and TunnelLease activation.

| Check | Result |
| --- | --- |
| Local API state | CONNECTED / CZ |
| HTTPS egress address | `194.87.52.164` |
| ICMP to `1.1.1.1` | 2/2 replies |
| DNS lookup | Successful, link `hydra0` |
| Exit forward counter | 32 packets accepted through `eth0` |
| WAN replies | 30 packets accepted |
| NAT counter | 16 packets |
| `drop-hydra-other` | 0 packets |
| Anti-multihop counter | 0 packets |
| Exit Safety Policy | TCP/UDP rules retained before WAN accept |

These counters are the sampled values after the probes, not a throughput or exhaustive security test. HTTPS probes explicitly disabled proxy use. No user payload capture or new packet logging was added.

After testing, explicit Disconnect completed: no mini-PC `hydra0`, no session-state marker, no nft table residue; original WAN route and DNS restored. UI was relaunched without requesting a connection.

## Go gates

- `gofmt` and `git diff --check`: PASS.
- `go vet ./...`: PASS.
- `go test ./...`: PASS.
- Affected packages (`productruntime`, `cmd/irviod`, `bootstrap`, `runtime`): PASS.
- `go test -race -shuffle=on -count=5 ./internal/productruntime ./cmd/irviod ./internal/bootstrap`: PASS.
- Initial whole-tree `go test -race -shuffle=on ./...`: one timeout in the unchanged `TestSupervisorCoalescesRepeatedRestartTriggers`.
- Repeated supervisor test with the original shuffle seed (`1790762196248771817`), race and count 10: PASS.
- Repeated whole-tree `go test -race -shuffle=on ./...`: PASS.

The initial supervisor timeout is preserved as a test observation, not concealed or fixed by unrelated changes. Its underlying cause was not established.

Privileged netns suites were not enabled by the ordinary whole-tree invocation. This task instead exercised the real kernel regression and the real deployed server/client path described above; it does not claim a full M8/M9/M10/M11/M12 campaign or new release soak.

The cold-network genesis startup issue and the acknowledged New York subnet reachability issue were not changed by this fix.
