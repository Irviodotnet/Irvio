# Windows P2 — listener refusal and handle lifetime

Baseline: `windows-port`, `c03e93b`. Scope is the two requested P2 observations,
not a new full-port acceptance or installer campaign. Production WFP, ACL,
socket options, listener port and timeout policy are unchanged.

## Historical TCP/51822 refusal

The recorded failing operation is creation/bind of the Exit's inner session
control listener `10.6.17.1:51822` via `sessioncontrol.ListenTunnel`. It is not
an outbound dial to a public server. The previous evidence did not capture an
owner or exclusion at the instant of failure, so it does not establish whether
an exclusive socket, port exclusion or filtering policy caused the refusal.

Existing diagnostics inspect owner tables synchronously on Winsock access or
address-in-use errors. Existing native tests reproduce an exclusive wildcard
conflict, preserve the Winsock error and prove that releasing the conflicting
socket permits the original listener. A separate test exercises an exclusive
physical-source NAT reservation alongside a distinct TUN-source listener.
Neither controlled reproduction proves the cause of the historical event.

## Full-runtime attempts

The peer VM temporarily ran the P1 daemon with SHA-256
`22a36b8c612009fd863c576b2bbb77fad55c5a535075f8642f94146d9daecd20`.
Two separately preserved campaigns targeted NL and DE. Both stopped at their
first connection timeout, before completing a lifecycle cycle. NL evidence
contains successful bootstrap/DHT and candidate discovery followed by broker
signaling deadline/invalid-message failures. These failures are not a recurrence
of the historical bind error and cannot supply a handle-growth verdict.

Both wrappers disconnected and restored the prior installed binary, leaving
the service stopped. Guest clock synchronization was proposed but denied by
the execution safety review; no clock change was made. Clock drift is not
declared the cause of these failures without further evidence.

## Diagnostic improvements

- The cycle harness can explicitly validate the current installed service path
  while retaining its original fixture default.
- An optional out-of-process handle sampler records only per-type counts for
  the selected PID, not object names, addresses or other processes' records.
  Native table reads have bounded allocation/retry budgets. Type numbers are
  local to the running OS and must not be compared across boots as fixed IDs.
- Its native self-test deliberately retains 64 Event handles, observes them,
  closes them and verifies their disappearance. It also tests a nonexistent PID.
- The 100-cycle native fixed-port test now records named handle types and
  goroutines every ten completed cleanup cycles. GC is explicit in this
  component probe, unlike the installed full-runtime campaign.

These are forensic harness changes, not a claimed production bug fix. The full
runtime handle observation remains open until CONNECT/DISCONNECT campaigns can
complete and demonstrate either a plateau or a continuing attributable leak.

## Native results

On Windows 11 Pro, `IRVIO-Win11-Peer`, using the native Go toolchain:

| Check | Result |
| --- | --- |
| Bounded handle sampler self-test | PASS, 64 Event handles retained/released |
| Relevant vet: sessioncontrol/localapi/networkstate | PASS |
| Local API lifecycle, race/shuffle ×10 | PASS, 100 measured cycles per repetition plus warmup |
| Fixed TCP/51822 TUN/listener lifecycle | PASS, 100/100 cycles, 350 seconds |
| Controlled exclusive wildcard conflict, IPv4/IPv6 | PASS, original error preserved and successful bind after blocker closes |
| Tunnel dial, authenticated Noise listener, interface identity fencing | PASS |
| Physical-source NAT reservation coexistence | NOT RUN in this follow-up; source-specific environment gate was not enabled |
| Final route/DNS/adapter baseline comparison | PASS, empty delta; installed service stopped |

Local API handles were 135→135 in the first six series, 135→136 in the seventh,
and 136→136 in the final three. All per-type resource assertions passed;
goroutines remained 3→3. This uses an API controller fixture, not real Internet
CONNECT/DISCONNECT.

Native listener samples kept File=16, Key=14 and goroutines=2 throughout.
Thread/Event/IRTimer/WaitCompletionPacket counts initially increased, then every
type count was identical at cycles 40, 50, 60, 70, 80, 90 and 100. This is a
bounded component plateau, not proof about indefinitely running full clients.

An initial native wrapper used the empty SYSTEM module cache with GOPROXY=off;
vet could not load dependencies. The corrected invocation used the existing
populated offline module/cache directories. Both attempts are preserved; no
product change or dependency substitution was made for that orchestration error.

Evidence on the VM: `C:\IRVIO-Lab\P2-evidence-01.tar`, with per-file SHA256SUMS.
Archive SHA-256:
`2ff669fb1fa0d594352cc13d332ac8b29d9b022696f23d0fd2e92a6a360ea4bd`.
The export includes allowlisted diagnostic text only, no executable backups,
profiles or keys. The short port observer completed; the installed services and
native tests are no longer running. No workstation networking was changed.

## Verdict

Neither P2 is falsely marked fixed. There is no confirmed product defect from
this follow-up to justify changing WFP/ACL or resource ownership. The historical
bind cause and full-runtime handle trend remain OPEN. Restore successful lab
connections, preserve clocks/binary identities in the next baseline, then run
the instrumented installed lifecycle series before choosing a production fix.
