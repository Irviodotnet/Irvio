# Private IRVIO — real reboot gate

## Executed result — 2026-10-06 UTC

Actual kernel reboot completed on the mini-PC. Previous boot ID `73a0c543-b679-4454-8213-1b78c6c16510`; new boot ID `a99c4e11-d2ad-44a3-b614-3e49a395ad7d`. Before reboot, the real-host fixture reached CONNECTED to the authorized private target, verified HTTP egress and native resolver tunnel DNS, and verified actual `/run/irvio/session-state` plus protection rules. Its ready marker was logged at 21:52:25 UTC. `systemctl reboot` was issued only after inspecting that marker and saving evidence.

Before the first post-boot API access: installed daemon inactive/MainPID=0; irvio.socket active; boot-recover completed with ExecMainStatus=0. Actual host had no session-state, hydra0 or nft tables; only hydra-mgmt namespace remained. DNS was 192.168.88.1 through enp4s0; default route via 192.168.88.1/source 192.168.88.18. Proxy-free HTTPS returned 200. The management connected-route metric changed from the earlier 102 to 100 after OS boot; exact route text equality is not claimed.

Ordinary-user socket activation returned DISCONNECTED, contribution=false, empty country, connection=null and no error. Shutdown succeeded. A separate startup of the preserved private identity/trust using the same installed packaged binary returned `saved_targets=1`, DISCONNECTED, private_target_active=false, hosting_wanted=false and contribution=false. It installed no protection/tunnel/intent. Daemon-owned Shutdown left that transient service inactive/MainPID=0/ExecMainStatus=0.

The preserved-profile check uses explicit `--role desktop --data-dir ... --local-api-socket ... --local-api-group root` for isolation. An initial diagnostic command omitted role and was rejected as legacy engineering mode before startup; it is not a product crash. The installed zero-argument service was checked separately. These combined observations prove new-boot inactivity and retained private trust, not same-boot crash recovery (covered separately).

No ordinary Go PASS is expected for the preparation process: reboot intentionally terminates it. Acceptance is the inspected pre-/post-boot evidence, not a missing results.json treated as success. Shutdown was a normal OS reboot, not a forced power cut.

## Preserved local evidence

- `/var/tmp/irvio-private-reboot-0ecd0d6/PRE_REBOOT.json`, protected identity/trust profile, and `pre-reboot-lab.tar.gz` (original lab `/tmp/hydra-m4b-64698173`).
- All preceding `/tmp/hydra-m4b-????????` artifact directories were archived before reboot to `/var/tmp/irvio-private-evidence-before-reboot/netns-artifacts.tar.gz`, mode 0600 under root-only directory. SHA-256 `5bfe1fc2b05f20c4f0517269fab3aa79cbd79bdc86c8181056701d8c34e07fc1` matched after reboot.
- Original `/tmp` paths in earlier reports are now archive-relative paths, because `/tmp` is tmpfs. Private evidence must not be published or committed.

## Reproduction procedure

`TestPrivateRuntimePrepareRealReboot` is a deliberate two-part manual acceptance fixture, not a normal passing Go test. It requires both `IRVIO_PRIVATE_REAL_REBOOT=1` and `IRVIO_PRIVATE_HOST_DNS=1`, plus a new `IRVIO_PRIVATE_REBOOT_EVIDENCE=/var/tmp/irvio-private-reboot-<unique>` directory. The fixture creates that directory itself with mode 0700 and refuses reuse.

It reuses the actual-host DNS topology: private client in PID1 network/mount namespaces, real resolvectl and `/run/irvio/session-state`, isolated distributors/Exit/sink. After authenticated enrollment, CONNECTED, actual tunnel DNS and HTTP egress proof, it verifies the real runtime intent and protection table. It preserves only atomic identity/trust/bootstrap files, not a live SQLite database or API socket. These files are private local evidence and must not enter Git or packages.

`PRE_REBOOT.json` records actual boot ID, time, private target, connected state, runtime intent presence, resolver state and protection. The fixture syncs its evidence filesystem, emits `REAL_REBOOT_READY`, and holds up to its bounded deadline. The operator must inspect evidence and explicitly reboot the mini-PC. No reboot is hidden in the test. If no reboot occurs, it fails and runs the ordinary host cleanup; reaching the hold is not PASS.

After SSH returns, verify a different actual boot ID, no runtime intent, no hydra0/protection/lab namespace residue, successful installed boot-recovery unit, normal DNS and proxy-free WAN. Before any API request, installed daemon must be inactive and its socket listening. Activate it and require IDLE/DISCONNECTED with no private hosting or target. Separately reopen the preserved identity/trust profile with the same packaged binary and require IDLE without auto-connect; no persistent discovery cache is claimed to be preserved. Finish with daemon-owned Shutdown and host integrity inspection.

Do not overlap this gate with any other privileged campaign. Do not reboot the workstation or production servers. Compilation and netns vet were checked on the mini-PC during preparation. The `product-api private-summary` test helper reports only status/aggregate trust count, without exporting invitations, secrets or identities.
