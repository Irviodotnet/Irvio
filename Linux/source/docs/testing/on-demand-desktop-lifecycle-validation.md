# On-demand desktop lifecycle — focused validation

Date: 2026-09-25. Baseline: `feat/egress-safety-policy-v0` at
`8f91d1746de2ad0bf940b540f5c67d2901f5b609`. Testing used the Linux
workstation only for builds and non-networked Go/Flutter tests. All live HYDRA
networking and the real reboot took place on mini-PC `192.168.23.2`.

## Implementation

- `hydra.socket` owns the Local API socket; `hydrad.service` starts only on an
  API connection and uses `Restart=on-failure`. The internal
  `hydra-boot-recover.service` oneshot runs before socket activation.
- A root-owned, mode-0600 `/run/hydra/session-state` stores the same-boot
  country ConnectIntent. ProtectedMode is installed before publishing a new
  ConnectIntent. On crash, the restarted daemon adopts protection and retries
  fresh country discovery. An interrupted explicit teardown is completed, not
  reconnected.
- Local API v1.4 `SHUTDOWN` acknowledges only after safe daemon-owned teardown,
  then exits 0. The GUI close button and tray action use this API; the close
  confirmation has a persistent “do not show again” choice. Idle daemon exits
  after one minute without Local API sessions; the socket stays listening.

## Mini-PC live checks

| Check | Result | Evidence |
| --- | --- | --- |
| On-demand startup | PASS | `hydra.socket` active, `hydrad.service` initially inactive; Local API request started hydrad and returned DISCONNECTED. |
| CONNECT | PASS | Real NL connection reached CONNECTED with CLIENT+EXIT contribution, ICE/AWG/TunnelLease and `/run/hydra/session-state` present. |
| Same-boot SIGKILL | PASS | systemd restarted hydrad (`NRestarts=1`); protection was adopted; fresh recovery returned to CONNECTED. |
| Crash physical-WAN oracle | PASS | 40 user probes across kill/restart; physical-WAN capture count 0. The same oracle captured one direct packet after normal DISCONNECT. |
| GUI process death | PASS | Killing only the GUI left the active daemon CONNECTED after more than one minute. |
| DISCONNECT | PASS | API reported DISCONNECTED, contribution false, intent absent; host network route returned to `enp4s0`, ProtectedMode nft table absent. |
| SHUTDOWN | PASS | API completed; `hydrad.service` inactive, `hydra.socket` active, no intent, ordinary host route restored. |
| Real reboot from active session | PASS | Old boot ID `8ac9a06e-9808-4305-b79f-77b35ff84c3e`, new boot ID `98b2d391-f8e6-4829-a0b3-1807bf3f9af0`; boot oneshot exited, daemon remained inactive, intent absent, ProtectedMode OFF, ordinary WAN TCP probe succeeded. |
| Idle auto-stop | PASS | After a post-reboot API status request, daemon exited successfully after about 65 seconds; socket remained active. |
| Final-binary smoke test | PASS | Updated build reached CONNECTED NL, then DISCONNECT and SHUTDOWN left daemon inactive, socket active, intent absent, and host route restored. |

Mini-PC final hydrad: `c0fd0c72790dfb3d5d02b6a5bc11195f65ea7177655fbe699d6838beca4ffb53`
(SHA-256, installed at `/usr/local/bin/hydrad`). Trimpath build SHA-256:
`4f3a0068786e3049a394b193cee7328201e406c4db5fd06cb43f81e2a40343e4`.
Flutter release library SHA-256:
`b7f28caaf7478862a03ecd378868e573660f4195b479aebf52c7012f12d1cd3d`.
The prior mini-PC installation was backed up at
`/var/tmp/hydra-lifecycle-backup-50sBBm` before deployment.

## Test gates and limits

`systemd-analyze verify`, Go vet, whole-tree Go tests, Go shuffle and race
gates, `flutter analyze`, `flutter test` (70 tests), and Flutter Linux release
build passed. The first repeated Flutter-test invocation failed before tests
started because the workstation proxy intercepted the test runner's loopback
WebSocket; rerunning with proxy variables removed **for that process only**
passed all 70 tests. The real reboot left the mini-PC at its graphical login greeter,
so `:0` was not yet available to the user: an attempted post-boot UI launch
reported `cannot open display`. This is not a daemon or networking failure;
the GUI can be opened after graphical login. No UI process or hydrad process
was left running at the greeter. Visual interaction with the confirmation
dialog and simultaneous teardown of multiple downstream TunnelLeases were not
re-exercised in this focused run; the dialog/controller/API paths have unit
coverage, but those two end-to-end checks remain separate acceptance gaps.

No HYDRA process was launched on the workstation. No workstation network
settings or services were changed.
