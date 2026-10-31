# Private IRVIO — active profile deletion

Added `TestPrivateRuntimeActiveProfileDeletion` using the existing real combined-mode lab. No production changes. Tests run only on the mini-PC, with extracted `bbeb819` daemon SHA-256 `a6ebc8837e286ec101ca9f74e631940cfe36884649bb17c11d4464b1054b5fd0`.

The fixture requires a private upstream plus an approved downstream client and real forwarding before sending Local API DeleteProfile. It waits for a different nonempty local identity after daemon reexec, empty targets/members/invitations/enrollments, empty bootstrap configuration, DISCONNECTED without hosting/public contribution, and no same-boot recovery intent. It checks release of the upstream lease, blocked downstream traffic with zero scoped physical-WAN leaks, all three desktops' saved network baselines after explicit leave, host ordinary-WAN egress, and successful Shutdown of the reset daemon.

This is actual API/identity/trust/network teardown, not merely deletion of a test JSON file. The test resets only its temporary host profile, not the installed user's profile. It uses manual-daemon reexec semantics; installed socket-reactivation semantics remain covered by separate product tests. DNS in this topology is isolated/synthetic, not a new native-resolver proof.

## Regression scope

Before adding this fixture, mini-PC `go vet ./...` and `go test ./...` completed successfully after the replay-cache fix and storage-boundary tests. Many unchanged package results were cached; opt-in privileged suites were not enabled. After adding the fixture, netns runner compilation and `go vet ./test/netns/m4b` passed. The initial compile typo used `HostPeerId` instead of the actual `LocalPeerId` API field and was corrected before any network run.

Both real-network repetitions passed (27.07s / 26.87s) under the three-minute suite timeout. `/tmp/hydra-m4b-ad179aef/results.json` records start `2026-10-06T22:26:52.758474191Z`, completion `22:27:46.747804268Z`, exit 0 and host integrity true. Runner SHA-256: `5def51de2f15dcc13720200b76a95ce1c4b50215cfcbaccbb602d42158b047f7`. Final inspection found only the pre-existing management namespace, no test/extracted-daemon process and no hydrad cgroups.
