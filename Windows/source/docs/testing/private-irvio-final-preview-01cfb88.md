# Private IRVIO — refreshed local preview 01cfb88

Built on the mini-PC from clean commit `01cfb886f8cb9e218743789006089ff852f3b969`, branch `private-irvio`, at `2026-10-06T21:10:02Z`. Includes the confirmed empty-Noise-read RELEASE fix. No GitHub, website or production-distributor deployment occurred.

Clean checkout: `/home/ubuntu/irvio-private-preview-01cfb88`. Artifacts: `/home/ubuntu/irvio-private-artifacts/01cfb88`. Archive: `irvio-private-preview-01cfb886f8cb-ubuntu-amd64.tar.gz`. Built with the existing `packaging/ubuntu/build.sh <output> <September-MMDB> --private-preview`, Go 1.27.1 and Flutter 3.47.4 using offline caches. The tray plugin's existing deprecated `app_indicator_new` warning remains nonfatal.

| Artifact | SHA-256 |
| --- | --- |
| Archive | `f5f2b4c9b0050ae66fed99c36a10adfe2281e9b8af00815671614cfab7f1c7c2` |
| Packaged trimpath daemon | `99e0ebe41e39834810fed72d9dc26e629b421230259b307a46222774a93cd0a3` |
| UI executable | `8eef0e337c0487a130955cf758ed421d21a29d29d89278fb8778b49fac35f30b` |
| UI libapp.so | `0e4d878c71b9282c2be5c9ff75fa03692c7690bf313e1f2ba8504069e748b7e4` |
| Netns runner | `23746842b370821a087e4cd309c5bd481cdeebfd1afba9e76ba3ffc594f63cea` |

The existing archive auditor passed: 567 files, safe paths/types/modes, exact checksum inventory, required binaries/assets/services and no forbidden profile/key/log files. External archive checksum and extracted `SHA256SUMS` both passed. The checkout stayed clean after the build. Runner compilation and netns package vet passed on the mini-PC.

## Autonomous final-binary resource gate — completed

Terminal result is PASS, exit 0, host integrity true. Full 101-sample analysis and RELEASE classification are recorded in `private-irvio-final-lifecycle-results.md`. The launch notes below are historical, not current process state.

Unit `irvio-private-lifecycle-01cfb88-r1.service`, invocation `46427790d40e46889ed27f18c9843e8f`, PID initially 284295. Last launch verification showed active/running and successful enrollment. Artifact root `/tmp/hydra-m4b-2410079c`. This is not a PASS claim; inspect terminal unit state and `results.json` after completion.

The runner uses the extracted archive daemon and checks the clean commit and extracted checksums before starting. Selection: `^TestPrivateRuntimeLifecycle100$`, count=1, test timeout=40m, unit maximum=41m; GC tracing enabled. Harness context reserves 20 seconds per cycle for the existing registration refill and bounded remote RELEASE drain, plus four minutes of setup. This changes no product timeout or acceptance count. It additionally rejects unconfirmed RELEASE during ordinary disconnects, before the intentional membership-revocation phase.

Launcher: mini-PC `01cfb88/lifecycle-run.sh` under the artifact directory. The first unit without `-r1` failed before creating a lab because root Git correctly rejected an ubuntu-owned checkout. The corrected script performs Git inspection through `runuser -u ubuntu`; global Git safety settings were not changed. The failed unit journal is retained.

No concurrent privileged topology, install/restart or reboot may interrupt this resource run. Packaged runtime completion, refreshed installation, fresh-boot acceptance and final requirement audit remain outstanding. Earlier DNS acceptance is recorded separately and does not imply real DNS across all 100 cycles; this resource harness retains its isolated DNS launcher.

## Refreshed installed preview

After resource and multi-client campaigns terminated with host integrity true, installed the audited 01cfb88 archive on the mini-PC through the documented `/opt/irvio/uninstall.sh` followed by extracted `install.sh --user ubuntu --no-deps`, in PID1's actual network/mount namespaces. Both succeeded. The old installed binaries/launcher/units were removed and replaced; persistent state was retained. A root-only backup remains at `/var/tmp/irvio-private-install.oos5XZ/before-01cfb88.tar` and must never be published.

Before first activation: daemon inactive/MainPID=0, socket active. Installed daemon, UI executable and libapp.so matched the hashes above. Tar comparison against the backup reported only GID differences on 13 persistent files, no content differences; this is the previously observed StateDirectory group transition, not exact metadata equality.

Ordinary-user Local API access activated the installed daemon through systemd's socket and returned DISCONNECTED, no connection, empty country, contribution=false and no error. Daemon-owned Shutdown succeeded. Final service state: inactive, MainPID=0, NRestarts=0, ExecMainStatus=0; socket still active. No session-state, hydra0 or nft tables remained. Actual-host ordinary HTTPS with proxy disabled returned HTTP 200. Extracted installer `--check` passed.

No CONNECT was requested and the UI was not opened. This proves refreshed installation and idle inherited-socket behavior, not connected socket-adoption after SIGKILL or a real reboot. Those acceptance gates remain open. Workstation installation, production servers and public releases were unchanged.
