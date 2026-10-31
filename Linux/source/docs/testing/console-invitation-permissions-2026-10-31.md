# Console invitation file access

The current Linux CLI writes a text invitation, not a QR image. Its former `os.CreateTemp` behavior left the file owned by root when the binary was run through sudo, and the caller's umask could further restrict access.

The new file helper explicitly sets mode 0600 and, for root invoked through sudo, validates the invoking account and transfers ownership to that account. Non-root callers cannot influence ownership using SUDO environment variables. Invalid sudo metadata fails before bootstrap import or sharing starts. A direct root login retains root ownership. Parent directory permissions are deliberately unchanged: use a directory the intended account can traverse.

Validation was performed on the mini-PC, not on the workstation:

- Full race-instrumented sharecli suite as ubuntu: PASS (privileged test skipped).
- Full race-instrumented sharecli suite through sudo: PASS, including the privileged test.
- Files created through sudo were opened with `/bin/cat` and copied with `/bin/cp` in child processes running with the invoking user's UID/GID.
- Restrictive umask 0777 still results in owner-readable/writable 0600 files.
- Malformed/mismatched sudo metadata is rejected; non-root metadata is ignored.
- Existing detached/persistent sharing and failure-cleanup regressions remain PASS.

The console binary was updated on the mini-PC without restarting the networking daemon. This does not retroactively change existing files or grant access to other users. No QR export implementation was changed, because this CLI has no QR output path.
