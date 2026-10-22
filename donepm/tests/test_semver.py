"""Tests for SemVer parsing and npm range matching."""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.semver import Version, VersionError, matches, max_satisfying  # noqa: E402


class VersionTests(unittest.TestCase):
    def test_parse(self):
        version = Version.parse("1.2.3")
        self.assertEqual((version.major, version.minor, version.patch), (1, 2, 3))

    def test_parse_with_prefix(self):
        self.assertEqual(str(Version.parse("v1.2.3")), "1.2.3")

    def test_parse_prerelease(self):
        version = Version.parse("1.0.0-beta.2")
        self.assertEqual(version.prerelease, ("beta", "2"))
        self.assertEqual(str(version), "1.0.0-beta.2")

    def test_invalid(self):
        with self.assertRaises(VersionError):
            Version.parse("not-a-version")

    def test_ordering(self):
        self.assertLess(Version.parse("1.0.0"), Version.parse("1.0.1"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-beta"))
        self.assertGreater(Version.parse("2.0.0"), Version.parse("1.99.99"))


class RangeTests(unittest.TestCase):
    def test_exact(self):
        self.assertTrue(matches(Version.parse("1.2.3"), "1.2.3"))
        self.assertFalse(matches(Version.parse("1.2.4"), "1.2.3"))

    def test_caret(self):
        self.assertTrue(matches(Version.parse("1.4.0"), "^1.2.3"))
        self.assertFalse(matches(Version.parse("2.0.0"), "^1.2.3"))
        self.assertFalse(matches(Version.parse("1.2.2"), "^1.2.3"))

    def test_caret_zero_major(self):
        self.assertTrue(matches(Version.parse("0.2.9"), "^0.2.3"))
        self.assertFalse(matches(Version.parse("0.3.0"), "^0.2.3"))

    def test_tilde(self):
        self.assertTrue(matches(Version.parse("1.2.9"), "~1.2.3"))
        self.assertFalse(matches(Version.parse("1.3.0"), "~1.2.3"))

    def test_comparators(self):
        self.assertTrue(matches(Version.parse("1.5.0"), ">=1.2.0 <2.0.0"))
        self.assertFalse(matches(Version.parse("2.0.1"), ">=1.2.0 <2.0.0"))

    def test_or(self):
        self.assertTrue(matches(Version.parse("3.0.0"), "^1.0.0 || ^3.0.0"))

    def test_star(self):
        self.assertTrue(matches(Version.parse("9.9.9"), "*"))

    def test_max_satisfying(self):
        versions = ["1.0.0", "1.2.0", "1.9.9", "2.0.0"]
        best = max_satisfying(versions, "^1.0.0")
        self.assertIsNotNone(best)
        self.assertEqual(str(best), "1.9.9")

    def test_max_satisfying_none(self):
        self.assertIsNone(max_satisfying(["1.0.0"], "^2.0.0"))


if __name__ == "__main__":
    unittest.main()
