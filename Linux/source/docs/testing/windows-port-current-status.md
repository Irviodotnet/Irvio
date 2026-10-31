# Windows port — current acceptance map

Snapshot: 2026-10-13. This is a navigation aid, not a replacement for the original
[port plan](../product/windows-port-implementation-plan.md) or an overall PASS.
Historical failures and their evidence remain in
[the validation journal](windows-port-validation.md). Later journal entries may
supersede this snapshot; a green build never implies all runtime gates passed.

## Baselines that must not be conflated

| Item | Current identity / meaning |
| --- | --- |
| Fresh handoff source | `dfda72923e68741dd600cfdae4ce166e2b5ae186` |
| Fresh handoff daemon | SHA-256 `3a153faa75c30b4c3e8d0c51cddb9b7ad6ee95007373bb2e88e9894ff69451c9`; built, not yet installed/runtime-tested |
| Runtime-tested peer daemon | `bc09655`, SHA-256 `b83182f4d581830872a7c8d75b32ad4a86b124a5c5415b75024e962210c5dda8` |
| Build VM | Windows 11 Pro, IRVIO-Win11-Peer; two guest NICs, one host/public uplink |
| Package | Development-unsigned EXE, not MSI or signed public release |
| Handoff | `/home/ubuntu/Work/IRVIO_WINDOWS_BUILD/dfda729`; instructions in `install.md` |

Do not overwrite the older c1a71e0 handoff or relabel it current. Read
`IRVIO_WINDOWS_BUILD/CURRENT.md`. No bootstrap, invitation, private profile,
identity or raw capture belongs in the distributable handoff.

## Evidence and remaining coverage

| Area | Concrete evidence available | Remaining / limitations |
| --- | --- | --- |
| Global CLIENT+EXIT | Global100 on the recorded earlier diagnostic binary; current installed five-downstream Disconnect and exact cleanup | Older 100-cycle results are not measurements of new package bytes; retain modest handle-growth observation and unexplained historical bind failure |
| Private VPN | Linux↔Windows, Windows↔Windows traffic and 100 measured CLIENT+HOSTING cycles on their recorded baselines | Complete original W07 failure/discovery matrix and current-package parity still need item-by-item closure; clock sensitivity remains documented |
| Physical network changes | Source replacement and both guest-NIC directions; same daemon, no new CONNECT, HTTPS and uncached DNS | One host uplink is not broadband↔mobile/public-ISP migration; intermittent earlier DNS failure is not explained by later successes |
| Same-boot crash | Installed forced process kill, automatic SCM restart, same country restored, HTTPS/DNS, exact teardown | Capture is limited to tagged IPv4 UDP; not all protocols or IPv6 |
| Leak oracle | Independent captures on both guest NICs with positive controls before/after; migration and crash PASS | Sampled 100 ms probes; initial CONNECT and intentional teardown excluded; broader IPv6/DNS/power coverage remains distinct |
| Power lifecycle | Real cold reboot and BootType 2 hibernate evidence; installed session resume and cleanup | BootType 1 Fast Startup not proved; two hybrid attempts were actually cold boots; VM does not provide physical S3/Modern Standby |
| Desktop GUI | Genuine standard-user manual fixture with real connect, QR save/import, SQLite Delete Profile and Shutdown; installed filtered-token activation | These are different fixtures; finish original DPI/multi-monitor/language/Unicode-account and actual clean edition coverage without conflating them |
| Headless server | Actual preset/ports/GeoIP/start-stop plus packaging lifecycle checks | Public M11 reachability failed in VM NAT; no successful public quorum/Rendezvous/selectable Exit plus second-client traffic claim |
| Installation | Recorded real install/update/rollback/uninstall and interruption tests; new package integrity checked | Do not rerun all installer scenarios for unchanged packaging; final artifact needs proportionate runtime/release verification, signing and target-edition coverage |
| Native regression | dfda729 vet, test, shuffle ×10, race, race-shuffle ×5, normal/trimpath builds all PASS; exact loopback cleanup | Env-gated privileged suites not enabled by this matrix; original bare-test fixture failure retained |
| Release | Source, dependency provenance, hashes, notices and development handoff | Signing input, remaining runtime/hardware gates and separately authorized packaged release soak are not complete |

The ordinary standard-user UI proof is in “W09 ordinary-user GUI and real SQLite
profile deletion”; the later installed test explicitly used an administrator's
filtered token. Neither should erase the other, and neither alone proves every
installer/account scenario. The 100-cycle Global and Private campaigns likewise
have different binary hashes and precise scope in their journal sections.

## Known observations that must not disappear

- Post-migration curl exit 6 occurred before DHCP restoration in earlier runs.
  Subsequent uncached-DNS passes do not identify its cause. Preserve bounded
  pre-restoration DNS diagnostics if it recurs; no speculative cache flush.
- Earlier TCP/51822 WSAEACCES was not reproduced in the instrumented Global100
  campaign. Non-reproduction is not a causal fix.
  The [P2 follow-up](windows-p2-lifecycle-2026-10-13.md) adds handle-type sampling;
  new installed NL/DE attempts stopped before CONNECTED, so neither the full
  handle-growth observation nor the historical bind refusal is closed.
- Private broker deadlines and the signed PREPARE/READY chain both confused
  remote wall time with local lifetime. The P1 follow-up fixes bounded ±2-second
  skew in source, with RED/GREEN and native race tests. A five-second remote
  timestamp allowance does not extend caller timers or local READY ownership.
  Installed/release packages and public distributors have not been updated by
  that follow-up; see the final P1 section of the validation journal.
- Explicit source ownership on Windows needs real local addresses. Bare tests
  using unassigned 127.0.0.2/.3 fail as designed. Use the committed disposable-VM
  fixture; do not replace addresses, bypass ownership or skip migration tests.

## Current user-directed scope

The owner requested finishing only the running tests and producing the port
report, without a separate laptop or additional test scenarios. The matrix and
cleanup are complete; its 17 evidence hashes are verified. See the
[final report](windows-port-report-2026-10-13.md). The owner explicitly requested
pausing the Windows-port goal after delivery, until a new instruction. The items
below preserve remaining acceptance requirements, not authority to start work.

The subsequent explicit P1 request authorized clock-skew fixes and DNS-migration
investigation only. Four completed migration probes passed DNS/HTTPS; one earlier
attempt failed recovery before DNS could be tested. Historical curl-6 remains
OPEN, not fixed by non-reproduction. Other full-port work remains deferred.

## Remaining work order (deferred beyond current report)

1. Reuse an installed fixture for the next uncovered runtime gate. Keep native
   regression and network experiments separate within the same VM. New package
   bytes need their own runtime evidence; do not borrow old binary results.
2. Prioritize the original remaining IPv6/DNS/power/GUI and Private discovery
   requirements, not another repeated installer interruption campaign.
3. Obtain actual physical Windows edition/power/mobile-network coverage and a
   reachable Windows-server topology for M11. Never bypass quorum to make a
   NAT-only VM count as a public server.
4. Perform the original requirement-by-requirement completion audit before any
   full-port claim. Release signing and a new packaged soak need their actual
   inputs/authorization; they are not inferred from unit tests.

Workstation proxy/network must remain untouched. Windows tests run in disposable
VMs; Linux tests use the mini-PC. Raw logs/captures stay private, with only
allowlisted summaries and hashes exported. No new multi-hour soak is authorized.
