#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

"""Read-only tar allowlist, permissions, inventory and checksum audit."""
import hashlib
import pathlib
import sys
import tarfile

archive = pathlib.Path(sys.argv[1])
root = archive.name.removesuffix(".tar.gz")
allowed = {
    "install.sh", "uninstall.sh", "install.md", "BUILD_INFO.json",
    "GO_BUILD_INFO.txt", "SHA256SUMS", "payload", "integration", "scripts", "licenses",
}
forbidden = {".git", "profile", "identities", "pre_alpha_final_test", "key.txt", "client_logs.md"}
files = {}
with tarfile.open(archive, "r:gz") as tar:
    seen = set()
    for member in tar:
        path = pathlib.PurePosixPath(member.name)
        assert not path.is_absolute() and ".." not in path.parts, member.name
        assert path.parts[0] == root and member.name not in seen, member.name
        seen.add(member.name)
        assert member.isdir() or member.isfile(), f"Link/special file: {member.name}"
        assert member.uid == member.gid == 0, member.name
        assert member.mode & 0o022 == 0, f"Writable by group/other: {member.name}"
        assert not forbidden.intersection(path.parts), member.name
        if len(path.parts) == 1:
            assert member.isdir()
            continue
        assert path.parts[1] in allowed, member.name
        if member.isfile():
            assert path.suffix not in {".key", ".pem", ".sqlite", ".db"}, member.name
            files[str(path.relative_to(root))] = tar.extractfile(member).read()
    manifest = files["SHA256SUMS"].decode("utf-8")
    declared = {}
    for line in manifest.splitlines():
        digest, name = line.split("  ", 1)
        name = name.removeprefix("./")
        assert name not in declared
        declared[name] = digest
    assert set(declared) == set(files) - {"SHA256SUMS"}, "Unhashed or missing files"
    for name, digest in declared.items():
        assert hashlib.sha256(files[name]).hexdigest() == digest, name
    for required in (
        "payload/bin/irviod", "payload/ui/irvio", "payload/ui/lib/libapp.so",
        "payload/ui/data/flutter_assets/assets/irvio-globe.png", "payload/geo/country.mmdb",
        "integration/systemd/irvio.socket", "integration/systemd/irviod.service",
        "integration/systemd/irvio-boot-recover.service", "install.sh", "uninstall.sh",
        "integration/systemd/irvio-server.service", "scripts/server.sh",
        "scripts/check-server.sh", "scripts/server-health.py",
        "payload/ui/data/flutter_assets/assets/flags/cz.png",
        "payload/ui/data/flutter_assets/assets/flags/LICENSE.txt",
    ):
        assert required in files, required
    flags = [name for name in files if name.startswith("payload/ui/data/flutter_assets/assets/flags/") and name.endswith(".png")]
    assert len(flags) >= 249, "Incomplete bundled country flags"
    for script in ("install.sh", "uninstall.sh", "integration/irvio"):
        assert tar.getmember(f"{root}/{script}").mode & 0o111, script
print(f"PASS: {len(files)} files; safe paths/types/modes, exact checksum inventory, required payload, no profile/key/log files.")
