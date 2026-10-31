# Private IRVIO — shutdown-dialog-fix local preview

Final update: the corrected 100-cycle run completed successfully as `d753e940`; see `private-irvio-final-package-resources.md`. The failed first attempt and initial launch states below remain historical evidence.

Clean source `f5f816b5341f24ea61e9993ec62f26a43a1c5b7f`, branch `private-irvio`. Built on the mini-PC at `2026-10-06T23:03:36Z` with Go 1.27.1 and Flutter 3.47.4. No workstation runtime tests, public release or production distributor changes.

Artifacts: `/home/ubuntu/irvio-private-artifacts/f5f816b`. Clean checkout: `/home/ubuntu/irvio-private-preview-f5f816b`. Archive: `irvio-private-preview-f5f816b5341f-ubuntu-amd64.tar.gz`.

| Artifact | SHA-256 |
| --- | --- |
| Archive | `49b5871ced7b62df6728be09c95fa5ffb3df42a32f5bd6f003a031796a3231a8` |
| Normal daemon | `d2ec523561727b7afeeff0ba35c653b674f6fc2d7ee87b9887da5f0a57fb863b` |
| Packaged trimpath daemon | `f268089bfb7d5a3194a4425b6fb3d5cfe9fe65f5be1e7c7b3962625482d2f528` |
| UI executable | `e1e8a3dbf6dddec9c33363865c8172d9e277bd3edc8f9e8e44fad9e488ee7b59` |
| UI libapp.so | `6411e8ece1a8ee93ee00350a796d745c535012aec251617bd5c9d183f65fed43` |
| Netns runner | `9d82fa8b7ebc36d5302ae01b7fdb470e4d96f09b307d4d61924b3acc3f0f0017` |

Archive audit passed for 567 files; external checksum and extracted exact `SHA256SUMS` passed. Source checkout remained clean. The nonfatal AppIndicator deprecation warning remains. The UI fix's 131-test suite and native ten-cycle Cancel/reclose proof are recorded separately in `private-irvio-native-dialog-fix.md`; those native results must not be attributed to this newly packaged executable.

## Installation

The previous installed preview was quiescent. Its owned installation was removed through its uninstaller and replaced with the extracted `install.sh --user ubuntu --no-deps` in PID1's network/mount namespaces. Dependencies were already present. Root-only backup: `/var/tmp/irvio-private-update-f5f816b.lElhf9`; contains private profile state and must not be published. All regular profile files had identical path/content checksum inventories before and after installation; this does not assert identical ownership metadata.

Installed daemon, UI and libapp.so compare byte-for-byte with the archive. Installer `--check` passed. Ordinary-user API access activated the daemon and returned DISCONNECTED, zero saved targets, private hosting/target inactive and public contribution false. Daemon-owned Shutdown returned it to inactive/MainPID=0/NRestarts=0/ExecMainStatus=0; socket activation remained available. Actual-host HTTPS without a proxy returned 200. Before launching the next lab only the existing `hydra-mgmt` namespace remained. No installed CONNECT was requested.

## Final-binary resource gate — first attempt failed

Autonomous mini-PC unit `irvio-private-lifecycle-f5f816b.service`, invocation `cf7a4168dc804c44b8f6f3cb522a75c1`, initial PID 30049. Selection: `TestPrivateRuntimeLifecycle100`, count 1, test timeout 24 minutes, unit maximum 25 minutes. It uses this archive's daemon and runner with the existing isolated DNS launcher and synthetic country fixtures; it is not a new native-host DNS test. Initial state was active/running and routed enrollment reached approval. No final result is claimed yet.

Terminal artifact `855a8439` reports exit 1 and host integrity true, 119.42 seconds. The resource file contains the initial sample and two completed cycles, not 100. Subsequent promotion failed at `tunnel_dns_activate`: the real resolvectl reported link indexes 5/6/7 unknown. The intended lab DNS substitution was not reliable. Initial link indexes overlap actual host interfaces, so the generic host-integrity result is insufficient to establish DNS preservation.

Actual-host inspection found its configured DNS missing while NetworkManager still reported `192.168.88.1` for enp4s0. Reapply alone did not restore resolved; explicitly restoring that known DNS and its default-route flag restored ordinary no-proxy HTTPS (200). This is a lab isolation failure, not a passing resource test or a proven daemon DNS defect.

The Go lab launcher performed thread-local namespace changes without `runtime.LockOSThread`; this was corrected in the test helper only. A read-only privileged regression checks DNS executable identity across Go workers after launcher exec. Both old and corrected helpers passed 20 launches and another 400 launches under GOGC=1, so that small test does not reproduce the original failure or by itself prove its precise scheduling cause. A short real lifecycle repetition with the corrected helper precedes any new 100-cycle run. Frozen production binaries remain unchanged. Earlier 100-cycle evidence retains its recorded older binary and must not substitute for a successful final-binary run.

## Corrected helper repetition

Helper SHA-256 `1c5e517723d3236e169c079e97950384912991d2efd7678d6993692a2b147c31`. `TestPrivateRuntimeDisconnectRelease` passed in 65.41s: three cycles, real traffic, revocation and cleanup. Artifact `ae8c3a20` has exit 0 and host integrity true. Manual DNS inspection retained `192.168.88.1` on enp4s0; only `hydra-mgmt` remained. Helper vet passed on the mini-PC.

The repeat 100-cycle unit is `irvio-private-lifecycle-f5f816b-r2.service`, invocation `c0f26e3aa1634c21b7a7d4dc87094800`. Its wrapper captures actual-host DNS/domain/default-route before and after and fails on any difference, in addition to the existing runner checks. This uses the unchanged f5f816b production archive and updated lab launcher. Result is pending, not PASS; no concurrent privileged lab or reboot is safe until completion/cleanup are checked.
