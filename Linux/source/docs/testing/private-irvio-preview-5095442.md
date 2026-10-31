# Private IRVIO — NativeTun-fix local preview

Clean source `5095442713cd2c3dbc73ef99d06b28f8bb95b6d5`, branch `private-irvio`, built on the mini-PC at `2026-10-06T22:45:01Z`. Includes both session-control fixes and the completed NativeTun cancellation handling. No public release, GitHub, website or production-distributor changes.

Checkout: `/home/ubuntu/irvio-private-preview-5095442`. Artifacts: `/home/ubuntu/irvio-private-artifacts/5095442`. Archive: `irvio-private-preview-5095442713cd-ubuntu-amd64.tar.gz`. Go 1.27.1, Flutter 3.47.4, offline caches and the verified September 2026 DB-IP Country Lite database.

| Artifact | SHA-256 |
| --- | --- |
| Archive | `935703c71f5ba4f4964c07d5a1c70dd44f9a78bb311d3fc91402797f645cebac` |
| Normal daemon (`CGO_ENABLED=0`, no trimpath) | `3bc7b8d52b1fee475da995991969ffa28dc758095990c980fe7e04f6eb839d53` |
| Packaged trimpath daemon | `0cd289e5bb042a648fbf182fb315e26ad35dd8b12c622c99ed18bc1d9ae9639a` |
| UI executable | `b3e37d768d862a44c71f22f20b3cfc7e2fbc2651d53c261a792ae1b5daadedf3` |
| UI libapp.so | `a1b3ec5d209b41c38d21805e7811ba009ccb2fa03d500cc2fe24d68025ec878c` |
| Netns runner | `66046952d3de6b13b0a980679cf848e69079796c22379f9e4c652f09a6f79e07` |

Archive auditor PASS: 567 files, safe paths/types/modes, exact checksum inventory and required assets/services, no profile/key/log files. External archive checksum and extracted `SHA256SUMS` both passed. Clean checkout remained clean. The existing tray plugin deprecation warning remains nonfatal. Invoking the nonexecutable repository build script directly initially returned 126; invoking it through Bash succeeded without a source or permission change.

On the build's isolated UI source copy, `flutter analyze --no-pub` reported no issues and `flutter test --no-pub --reporter compact` passed all 130 tests in 28 seconds. This is analysis/widget/controller/socket-fixture evidence, not a replacement for native GUI interaction.

## Packaged runtime validation

Selected four existing private scenarios twice: combined hosting/upstream, multiple clients/full leave, active profile deletion and three-cycle disconnect/RELEASE. Daemon is the extracted archive binary, not the separately built normal binary. Existing isolated runtime/DNS launcher and synthetic GeoIP fixtures remain; this selection does not retest native host DNS.

All eight cases passed in artifact `/tmp/hydra-m4b-033e44b3`: `2026-10-06T22:45:41.69199616Z` to `22:51:15.317655053Z`, exit 0 and host integrity true. Multiple-client leave took 47.22s/47.60s, RELEASE cycles 65.50s/65.17s, combined mode 27.09s/27.13s and active profile deletion 26.76s/27.10s. Combined-mode probes required own traffic via the pinned upstream and downstream HTTP/UDP/DNS/ICMP via physical WAN, not that upstream. Final inspection found only the pre-existing management namespace, no test/extracted-daemon process and no hydrad cgroups.

## Installed preview refresh

After successful lab cleanup, the mini-PC installation was updated through its existing `/opt/irvio/uninstall.sh` and the extracted `install.sh --user ubuntu --no-deps`, in PID1's network/mount namespaces. Dependencies were already installed. A root-only backup at `/var/tmp/irvio-private-update-5095442.CDrh5i` contains the previous installation and profile; never publish it. The old installed daemon/UI/units were removed by their installer, not an ad-hoc profile purge.

Tar comparison of `/var/lib/irvio` reported only GID changes on 13 files, no content differences. Its nonzero comparison status stopped the lab update script after installation, so subsequent checks were performed explicitly. This is not exact metadata preservation; the systemd StateDirectory group transition was also observed in the prior installation test. The trust store had zero targets/members before and after, version 1; no pre-existing trust was claimed or fabricated.

Installed daemon, UI executable and libapp.so match the table above. Before API activation, daemon was inactive/MainPID=0. Ordinary-user `private-summary` through `/run/irvio/control.sock` activated the daemon and returned DISCONNECTED, no private target/hosting/public contribution, and zero saved targets. Daemon-owned Shutdown then succeeded: inactive/MainPID=0/NRestarts=0/ExecMainStatus=0, socket active. No runtime intent, hydra0 or nft tables remained. Actual-host ordinary HTTPS with proxy disabled returned HTTP 200. Extracted installer `--check` passed.

No installed CONNECT or native UI launch was requested in this refresh; connected behavior was tested in the isolated packaged lab above. This package is a local development preview, not a declaration that every P00–P11 acceptance requirement is complete.
