"""Tests for package.json and package-lock.json handling."""

from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.manifest import LOCKFILE_VERSION, Lockfile, LockNode, Manifest  # noqa: E402


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.path = os.path.join(self.tmp, "package.json")

    def test_missing_file(self):
        manifest = Manifest.load(self.path)
        self.assertEqual(manifest.name, "")

    def test_round_trip(self):
        manifest = Manifest.create("demo", "2.1.0", description="a demo")
        manifest.dependencies["left-pad"] = "^1.3.0"
        manifest.dev_dependencies["jest"] = "^29.0.0"
        manifest.scripts["test"] = "jest"
        manifest.save(self.path)

        loaded = Manifest.load(self.path)
        self.assertEqual(loaded.name, "demo")
        self.assertEqual(loaded.version, "2.1.0")
        self.assertEqual(loaded.dependencies, {"left-pad": "^1.3.0"})
        self.assertEqual(loaded.dev_dependencies, {"jest": "^29.0.0"})
        self.assertEqual(loaded.scripts, {"test": "jest"})

    def test_dependencies_helper(self):
        manifest = Manifest.create("demo")
        manifest.dependencies["a"] = "1"
        manifest.optional_dependencies["b"] = "2"
        manifest.dev_dependencies["c"] = "3"
        self.assertEqual(
            manifest.runtime_dependencies(dev=True, optional=True),
            {"a": "1", "b": "2", "c": "3"},
        )
        self.assertEqual(manifest.runtime_dependencies(dev=False, optional=False), {"a": "1"})

    def test_add_and_remove(self):
        manifest = Manifest.create("demo")
        manifest.add_dependency("x", "^1.0.0")
        self.assertEqual(manifest.dependencies["x"], "^1.0.0")
        manifest.add_dependency("y", "^2.0.0", dev=True)
        self.assertIn("y", manifest.dev_dependencies)
        self.assertTrue(manifest.remove_dependency("y"))
        self.assertNotIn("y", manifest.dev_dependencies)


class LockfileTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.path = os.path.join(self.tmp, "package-lock.json")

    def test_round_trip(self):
        lock = Lockfile.empty("demo", "1.0.0")
        lock.set_root(Manifest.create("demo", "1.0.0"))
        lock.add(
            "node_modules/left-pad",
            LockNode(
                path="node_modules/left-pad",
                version="1.3.0",
                resolved="https://registry.test/left-pad/-/left-pad-1.3.0.tgz",
                integrity="sha512-abc",
                dependencies={"dep": "^1.0.0"},
            ),
        )
        lock.save(self.path)

        with open(self.path, "r", encoding="utf-8") as handle:
            raw = json.load(handle)
        self.assertEqual(raw["lockfileVersion"], LOCKFILE_VERSION)
        self.assertEqual(raw["name"], "demo")
        self.assertIn("node_modules/left-pad", raw["packages"])

        reloaded = Lockfile.load(self.path)
        self.assertEqual(reloaded.name, "demo")
        self.assertEqual(reloaded.packages["node_modules/left-pad"].version, "1.3.0")
        self.assertEqual(
            reloaded.packages["node_modules/left-pad"].integrity, "sha512-abc"
        )

        # saving again must not change the document
        with open(self.path, "r", encoding="utf-8") as handle:
            before = handle.read()
        reloaded.save(self.path)
        with open(self.path, "r", encoding="utf-8") as handle:
            after = handle.read()
        self.assertEqual(before, after)

    def test_remove(self):
        lock = Lockfile.empty("demo", "1.0.0")
        node = LockNode(path="node_modules/left-pad", version="1.3.0")
        lock.add("node_modules/left-pad", node)
        self.assertEqual(lock.remove("left-pad"), 1)
        self.assertEqual(len(lock), 0)


if __name__ == "__main__":
    unittest.main()
