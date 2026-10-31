#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class PreviewBuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="irvio-preview-test-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.repo = self.base / "repo"
        script = self.repo / "packaging/ubuntu/build.sh"
        script.parent.mkdir(parents=True)
        shutil.copyfile(Path(__file__).resolve().parents[1] / "build.sh", script)
        self.script = script
        for args in (["init", "-q"], ["add", "."],
                     ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                      "commit", "-qm", "fixture"]):
            subprocess.run(["git", "-C", str(self.repo), *args], check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        self.mmdb = self.base / "fixture.mmdb"
        self.mmdb.touch()
        self.out = self.base / "output"
        tools = self.base / "bin"
        tools.mkdir()
        go = tools / "go"
        go.write_text("#!/bin/sh\nexit 73\n")
        go.chmod(0o755)
        self.env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"])

    def build(self, *flags):
        return subprocess.run(["bash", str(self.script), str(self.out), str(self.mmdb), *flags],
                              env=self.env, capture_output=True, text=True, timeout=10)

    def test_clean_preview_uses_commit_name(self):
        result = self.build("--private-preview")
        self.assertEqual(result.returncode, 73, result.stderr)
        head = subprocess.check_output(["git", "-C", str(self.repo), "rev-parse", "--short=12", "HEAD"], text=True).strip()
        self.assertEqual(len(list(self.out.glob(f".irvio-build.*/irvio-private-preview-{head}-ubuntu-amd64"))), 1)

    def test_dirty_tracked_source_refused(self):
        with self.script.open("a") as target:
            target.write("\n# dirty fixture\n")
        self.assert_refused()

    def test_untracked_source_refused(self):
        (self.repo / "unexpected.go").touch()
        self.assert_refused()

    def assert_refused(self):
        result = self.build("--private-preview")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("clean committed checkout", result.stderr)
        self.assertFalse(self.out.exists())

    def test_unknown_mode_refused(self):
        self.assertEqual(self.build("--unknown").returncode, 2)
        self.assertFalse(self.out.exists())

    def test_git_failure_refused(self):
        git = self.base / "bin/git"
        git.write_text("#!/bin/sh\nexit 74\n")
        git.chmod(0o755)
        result = self.build("--private-preview")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("Cannot verify", result.stderr)
        self.assertFalse(self.out.exists())

    def test_public_default_unchanged(self):
        result = self.build()
        self.assertEqual(result.returncode, 73, result.stderr)
        self.assertEqual(len(list(self.out.glob(".irvio-build.*/irvio-1.1.0-alpha.20261005.4-ubuntu-amd64"))), 1)


if __name__ == "__main__":
    unittest.main()
