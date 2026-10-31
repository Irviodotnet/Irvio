# Private IRVIO — socket-activated crash gate

## Completed isolated socket activation

Mini-PC `/tmp/hydra-m4b-14fdb048`, 2026-10-06 21:47:13–21:47:40 UTC: PASS in 26.94s, exit 0, host integrity true. Runner SHA-256 `440d881e781a2117f64f5d84bcd9ca9e981202b9cbace29b2e86e320d200672d`; unchanged packaged daemon `99e0ebe41e39834810fed72d9dc26e629b421230259b307a46222774a93cd0a3`.

Real socket activation started the daemon only on API access. After private enrollment and traffic, SIGKILL left protection and persisted intent intact. Systemd restarted it once and restored pinned user traffic in 2.286s. Revocation, negative traffic/WAN counters, explicit disconnect, successful Shutdown and a still-listening activation socket passed. All lab socket/service units were removed afterward; only the pre-existing management namespace remained. Installed daemon stayed inactive/MainPID=0/NRestarts=0, installed socket active, no actual-host session intent or nft tables.

This proves inherited-listener crash recovery in an isolated transient unit using the packaged daemon. It does not substitute for actual reboot or connected recovery of the installed zero-argument host unit; DNS remains substituted in this topology.

## Harness corrections and preserved failed evidence

Second attempt `c35b64d8` exited 1 with host integrity true: the socket was created inside an uninitialized profile, and production correctly rejected the unrecognized `control.sock` entry. The restart loop was stopped explicitly using only the two resolved lab units. The corrected harness keeps the activation socket outside the profile and bounds initial API handshake to 15s. Third attempt `d24f00cc` reached private connection but a later API reconnect still used the old path; exit 1, host integrity true. Both initial and recovery/reconnect paths now use the same recorded socket path. No profile security check was relaxed.

Added `TestPrivateRuntimeSocketActivatedCrashRecovery`. It creates a unique transient systemd socket/service in the isolated private topology, checks MainPID=0 before Local API access, then uses actual enrollment, pinned forwarding, SIGKILL, Restart=on-failure and the existing fail-closed/recovery oracle. Final daemon-owned Shutdown must exit successfully without automatic restart while the socket remains listening. Socket/service cleanup is scoped to the lab units. Packaged binary remains unchanged.

The DNS/runtime launcher uses `syscall.Exec`, preserving PID and inherited FD rather than spawning the daemon as a child. Real-host installed idle activation was tested separately; this gate is connected operation with the same packaged binary, not the installed host unit or real DNS.

First attempt `/tmp/hydra-m4b-4c028495`, 2026-10-06 21:41:40–21:41:57 UTC: exit 1, host integrity true. No client daemon was started: systemd-run rejected `--wait` combined with socket operations. Cleanup reported missing transient units because none were created. This is a harness launch error, not an IRVIO failure.

Removed `--wait` for socket-triggered launches, retained it for the established non-socket systemd test, and added direct unit-state assertions for intentional shutdown. Corrected harness compiles and netns package vet passes on the mini-PC. A new privileged run is still required; no socket crash PASS is claimed.
