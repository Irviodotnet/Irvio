# Product Runtime Profiles v1 — Alpha acceptance checkpoint

Status: **NOT YET PASS**. The local product and lifecycle gates below pass, but the literal public-server deployment gate is blocked by the available verifier topology. The product changes may be committed as a non-PASS checkpoint; that commit does not certify Alpha acceptance.

## Baseline and binaries

- Branch: `feat/egress-safety-policy-v0`.
- Baseline HEAD: `e2046c8a3e29273c2719e30710e3a70a315688ca`.
- Normal `hydrad` SHA-256: `0c4570e2560f9e14885abdcffbb7a010d767cd6ea793f63652f6e24126ad3b34`.
- Trimpath `hydrad` SHA-256: `10d865f2197a02e5084702248feb6bd5bac6a63ba5b2f3471514e13ef9d74b6e`.
- These hashes identify the binaries used for the acceptance checkpoint before committing; a later build may embed different VCS metadata.
- The workstation was used to build binaries and run Go tests. After the operator's instruction, no HYDRA daemon or networking test was launched on the workstation; live product tests used the mini-PC or its disposable network namespaces.
- After the mini-PC tests, the temporary acceptance systemd units and their private installed binaries were removed; the original `hydra-client.service` is active again. Root-owned reboot/test evidence was retained under `/var/lib/hydra-alpha-acceptance/` and `/tmp/hydra-m4b-*`.

## Acceptance gates

| Gate | Result | Evidence |
| --- | --- | --- |
| ExitSelector event/snapshot ordering | PASS | New 128-iteration focused regression reproduced the old ordering defect, then passed 100 repeated runs after committing the snapshot before publishing `EventProviderUnavailable`. |
| Real host-namespace DNS | PASS | Mini-PC `hydrad` and `systemd-resolved` ran in the same host network namespace, with the real `/usr/bin/resolvectl`. CONNECT installed `hydra0` DNS `1.1.1.1` and route-only domain `~.`; `resolvectl query example.org` reported `-- link: hydra0`. DISCONNECT removed the link configuration; DNS, nftables and policy rules matched the saved baseline. |
| Same-boot SIGKILL/adoption | PASS after a narrow cleanup fix | With an active connection, SIGKILL left `hydra_protect` and blocked ordinary WAN probes (zero matching physical-WAN packets). Restart retained fail-closed protection and reported `NO_EXIT`, without falsely claiming CONNECTED. Explicit DISCONNECT removed the recovered kill switch, owned Exit firewall and HYDRA-owned route/rule residue. The first run exposed the missing crash-residue cleanup; the patched second run left no HYDRA nft table or policy route. Automatic reconnect to the former country is not an existing M8 guarantee. |
| Real reboot | PASS | Mini-PC boot ID changed from `e62a282c-4dea-4900-b619-a7771fe534b8` to `8ac9a06e-9808-4305-b79f-77b35ff84c3e`. A zero-argument product desktop unit was CONNECTED before reboot. At +30 seconds on the new boot it was running but DISCONNECTED, with no contribution, no `hydra0`, no ProtectedMode nft table and no auto-CONNECT; an ordinary WAN ICMP probe succeeded. The temporary acceptance unit was then stopped/disabled and the original `hydra-client.service` restored. |
| Desktop DISCONNECT with multiple downstream leases | PASS | Two independent downstream clients connected through the Desktop Exit and passed user-plane HTTP probes. The Desktop log records two distinct `TUNNEL_LEASE_DATA_PLANE_ACTIVATED` events, then two `TUNNEL_LEASE_DATA_PLANE_REVOKED` events before `PRODUCT_DESKTOP_IDLE`. Post-Disconnect downstream user probes could not reach WAN. Own TUN disappeared, contribution became false, direct WAN returned, FD/pipes returned to baseline, and the privileged test host snapshot was byte-identical. Mini-PC artifact: `/tmp/hydra-m4b-f4a5f192`, `host_integrity=true`. |
| Literal public server command and second Internet client | BLOCKED / NOT RUN | The only available public HYDRA host is `89.124.71.34`. Its three hydrad identities share that public IP. M11 requires two verifier attestations from distinct PeerIDs **and distinct public IPs**. A second independent public verifier failure domain is unavailable; using same-IP identities or lowering quorum would violate the acceptance contract. The real DB-IP MMDB is installed at `/var/lib/hydra/geo/country.mmdb`, and the server preset passed the separate three-independent-verifier netns integration test, but neither substitutes for the literal public-host gate. The existing Amsterdam services were not disrupted for a knowingly non-accepting deployment. |
| Whole-tree Go gates | PASS | `go vet ./...`, `go test ./...`, shuffle ×10, race, and race+shuffle ×5 completed with exit code 0 after the production fixes. The later test-only downstream assertion also passed in the privileged run. |
| Relevant privileged regression | PASS for executed suites | Earlier M6–M12/P1/product suites passed, including M8 crash adoption, M9 recovery, M11 quorum, M12 runtime and product server/desktop netns paths. The new multi-downstream product test ran on the mini-PC, with host integrity true. No new multihour soak was run. |

## Narrow fixes made during acceptance

1. ExitSelector now updates the committed provider-availability snapshot before publishing `EventProviderUnavailable`.
2. Same-boot product recovery cleanup now removes only HYDRA-owned route and Exit-firewall residue before disabling the recovered kill switch. Unknown/foreign nft or route objects are refused, leaving protection fail-closed. Focused route/firewall tests, including a privileged foreign-table case, pass.

The host DNS test used real `resolvectl`; the private netns regression continues to use its lab-only DNS launcher because host `systemd-resolved` cannot see namespace-local TUN links.

## Remaining release gate

Provide at least two reachable public M11 verifier peers on distinct public IPs, then run the literal `hydrad --role server --bootstrap hydra-bootstrap:v1:<bundle>` command with the real MMDB and prove its Rendezvous/Exit advertisement, Safety Policy and a second client's Internet path. Until that production-host proof passes, the correct verdict is **ALPHA ACCEPTANCE: NOT YET PASS**, regardless of the checkpoint commit.
