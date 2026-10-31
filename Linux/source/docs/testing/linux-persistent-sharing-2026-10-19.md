# Opt-in persistent private sharing — 2026-10-19

Usage: `irvio --mode client --bootstrap <code|text-file|QR-image> --persistent`.

The CLI enables persistence through authenticated Local API v1.8 only after hosting is ready and the invitation is saved. A root-owned mode-0600 `persistent-sharing-v1.json` in the daemon profile contains only hosting intent. Existing bootstrap, identity and friend authorization remain in their existing stores. Writes and deletion sync the profile directory.

The new installed/enabled `irvio-persistent.service` is a conditional boot oneshot, not a resident process. Only when that marker exists does it request the normal daemon service. The daemon validates the intent and uses the existing private recovery path, including retries if infrastructure is initially unavailable. No network protocol, M11 quorum, ICE/AWG or lease semantics changed. The Local API extension is additive; Windows does not implement persistent hosting.

Explicit Disconnect/Shutdown, `irvio stop`, or profile deletion disable persistence. OS/service termination retains the marker but tears down runtime resources normally. Without the flag, the existing fresh-boot IDLE behavior remains. No new invitations are minted on reboot: a one-hour invitation still expires normally, while accepted friend permissions remain until revoked.

## Validation on mini-PC

- Race-instrumented sharecli tests: PASS, including successful persistent handoff and failure cleanup.
- Full race-instrumented Local API suite: PASS, including strict command-field validation.
- Privileged lifecycle tests: PASS for persistent intent ownership/mode, rejection of other VPN modes, OS stop versus explicit stop, profile deletion, invalid durable intent, and existing nonpersistent fresh-runtime behavior.
- Linux builds and Windows daemon cross-compilation: PASS.
- Actual SSH command with `--persistent`: exit 0, HOSTING, persistence true; marker root-owned 0600.
- Actual reboot while HOSTING: new boot ID; boot hook exits successfully; daemon already running before status call; HOSTING restored, persistence true, three active Rendezvous registrations.
- `irvio stop`: DISCONNECTED, persistence false.
- Second actual reboot: new boot ID; hook skipped because its marker was absent; no daemon before API access; DISCONNECTED, hosting false, persistence false after status.
- Shell syntax and git diff checks: PASS.

Tests were not executed on the workstation. No automatic VPN connection was initiated there. The mini-PC is left disconnected with persistence disabled; the flag is opt-in for the user's next launch.
