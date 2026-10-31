# Detached console sharing — 2026-10-19

`irvio --mode client --bootstrap <code|text-file|QR-image>` now saves the invitation and returns successfully without disconnecting. The existing privileged daemon owns the hosting intent. No background CLI process, additional service or networking protocol was added.

`irvio status` reports connection state and private-sharing intent. `irvio stop` performs daemon-owned Disconnect and waits for DISCONNECTED. Repeating stop is safe. Stop refuses to interrupt an active non-hosting VPN mode. Profile and existing friend permissions remain intact.

Startup failures still attempt bounded cleanup with a fresh context. Successful handoff occurs after the invitation file has been synced and closed. Closing SSH after success does not stop sharing. New OS boots remain IDLE without automatic sharing.

## Mini-PC validation

- Race-instrumented sharecli suite, three repetitions: PASS (text/QR/literal parsing, invitation permissions, detached lifecycle, status, idempotent stop, refusal of another VPN mode, failure cleanup).
- Real SSH start from a text file returned exit 0 and a mode-0600 invitation file.
- A new SSH command observed HOSTING with no GUI/console process running.
- Explicit stop returned DISCONNECTED; a second stop was a harmless no-op.
- Real start from QR returned exit 0; HOSTING persisted through the 60-second idle grace period without a Local API client.
- Actual reboot while HOSTING: boot ID changed; before status, no daemon process was running; socket remained available.
- After status activation: DISCONNECTED, private sharing false, no ConnectIntent, no hydra0 or nftables tables; direct host-namespace HTTPS without proxies returned HTTP 200.
- Focused runtime promotion/DNS regressions: PASS.
- Existing bounded-handshake/rekey/crossed-rotation tests: PASS.

## Separate diagnostic observation

An older untracked experimental AWG reconnect probe was also run with race instrumentation. One of 20 simultaneous-handshake attempts exceeded its three-second deadline; queued remote reconnect passed 20/20. This is not a detached-sharing test and was not changed to obtain a PASS. The experimental probe remains outside the commit; this task does not claim an all-suite release PASS or resolve that separate timing observation.

No workstation VPN connections or test execution were performed. Windows MSI changes in the same accumulated commit retain their earlier separate validation report. No new Windows installation campaign was run for this Linux CLI change.
