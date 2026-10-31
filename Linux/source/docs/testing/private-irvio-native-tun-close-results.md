# Private IRVIO — NativeTun cancellation/close race

## Finding and causal order

The post-replay-fix public regression selection passed 19 cases, but separate real NativeTun stress failed once with `write |1: file already closed`. This was not a demonstrated retained-resource leak. Inspection of pinned AWG `v3.1.20260828` shows:

1. `NativeTun.Close` closes `statusListenersShutdown`.
2. The netlink listener may observe shutdown and run its deferred cleanup, closing its netlink socket, events and `RWCancel` pipe.
3. `NativeTun.Close` calls `netlinkCancel.Cancel`, which writes to that pipe.
4. The main TUN file is closed regardless of the cancellation error, but the cancellation error is returned first.

If the pipe writer is already closed, cancellation reports `os.ErrClosed`. If its reader has closed but the writer has not yet closed, cancellation reports `EPIPE`. The first fix covered only the former; a 10-repeat real stress run then produced one `broken pipe` failure and nine successful 500-cycle repetitions. That intermediate run is not PASS.

## Narrow correction

IRVIO owner and borrowed-device close now use one helper. Only a single `*os.PathError` for a write returning exactly `os.ErrClosed` or `EPIPE` is eligible for normalization, and only when `device.File().Stat()` proves the main descriptor is already closed. Missing/open descriptors, bare errors, read errors, unrelated errors and joined errors remain failures. Owner event-drain timeout handling is unchanged. No wire protocol, AWG cryptography, route, MTU or recovery-policy change is involved.

`TestLinuxCloseHandlesCompletedCancelPipeRace` deterministically models the cleanup ordering using the actual AWG RWCancel and real OS pipes. Before correction it failed for both owner and borrower. The completed-close cases must succeed; negative cases must retain their error; repeated Close stays idempotent. This is a controlled ordering regression, not instrumentation of a production daemon stack.

## Validation

All tests run on the mini-PC. `go test -race -shuffle=on -count=20 ./internal/tunnel` passed after both error variants were handled (1.869s), as did package vet. Privileged runner SHA-256: `bacae919fdf1ab770b69274d3b1d6f71ac1a6e125a1fc7c12cc6c3d1a5335aea`.

Affected `go test -race -shuffle=on -count=3 -timeout=180s` suites passed: runtime 28.409s, transportgen 1.573s, sessioncontrol 6.191s, privateaccess 3.518s and productruntime 1.071s. Whole-tree `go vet ./...` and `go test ./...` also passed; unchanged packages used the cache and opt-in privileged cases were not enabled by that command.

Final real stress passed all 20 repetitions: 10,000 create/close cycles plus each repetition's warmup. Every repetition reported baseline and final `{total:6 netlink:0 pipes:0 tun:0}`; times ranged from 5.26s to 6.89s. The runner executed inside a separate unnamed network namespace with a 180s deadline. Final inspection found only the pre-existing management namespace, no test processes and no hydrad cgroups.

The existing installed application and earlier archives do not contain this correction yet. Refresh the clean preview and exercise its combined dataplane/lifecycle before final handoff; this NativeTun gate alone does not establish all private-mode acceptance requirements.
