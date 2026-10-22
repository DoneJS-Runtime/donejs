"""Tests for the hardened tarball reader/writer."""

from __future__ import annotations

import io
import os
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.tarball import (  # noqa: E402
    UnsafeArchive,
    create_tarball,
    extract_tarball,
    read_manifest,
)


def _tar_with(entries: list[tuple[str, bytes, str]]) -> bytes:
    """Build a tar.gz from (name, content, kind) triples."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as archive:
        for name, content, kind in entries:
            info = tarfile.TarInfo(name)
            if kind == "file":
                info.size = len(content)
                archive.addfile(info, io.BytesIO(content))
            elif kind == "link":
                info.type = tarfile.SYMTYPE
                info.linkname = content.decode()
                archive.addfile(info)
            elif kind == "dir":
                info.type = tarfile.DIRTYPE
                archive.addfile(info)
    return buf.getvalue()


class ExtractionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.dest = os.path.join(self.tmp, "out")

    def test_extracts_regular_files(self):
        data = _tar_with([("package/index.js", b"module.exports = 1;\n", "file")])
        written = extract_tarball(data, self.dest)
        self.assertEqual(written, ["index.js"])
        with open(os.path.join(self.dest, "index.js"), "rb") as handle:
            self.assertEqual(handle.read(), b"module.exports = 1;\n")

    def test_strips_package_prefix(self):
        data = _tar_with([("package/lib/a.js", b"a", "file")])
        written = extract_tarball(data, self.dest)
        self.assertEqual(written, ["lib/a.js"])
        self.assertTrue(os.path.isfile(os.path.join(self.dest, "lib", "a.js")))

    def test_rejects_path_traversal(self):
        data = _tar_with([("package/../../evil.txt", b"pwn", "file")])
        with self.assertRaises(UnsafeArchive):
            extract_tarball(data, self.dest)
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "evil.txt")))

    def test_rejects_absolute_paths(self):
        data = _tar_with([("/etc/passwd", b"pwn", "file")])
        with self.assertRaises(UnsafeArchive):
            extract_tarball(data, self.dest)

    def test_rejects_symlinks(self):
        data = _tar_with([("package/link", b"/etc/passwd", "link")])
        with self.assertRaises(UnsafeArchive):
            extract_tarball(data, self.dest)

    def test_reads_manifest(self):
        data = _tar_with([("package/package.json", b'{"name":"x","version":"1.0.0"}', "file")])
        manifest = read_manifest(data)
        self.assertEqual(manifest["name"], "x")

    def test_manifest_missing(self):
        data = _tar_with([("package/index.js", b"x", "file")])
        with self.assertRaises(Exception):
            read_manifest(data)


class PackTests(unittest.TestCase):
    def test_round_trip(self):
        with tempfile.TemporaryDirectory() as tmp:
            os.makedirs(os.path.join(tmp, "lib"))
            with open(os.path.join(tmp, "package.json"), "w", encoding="utf-8") as handle:
                handle.write('{"name":"demo","version":"1.0.0"}')
            with open(os.path.join(tmp, "lib", "a.js"), "w", encoding="utf-8") as handle:
                handle.write("a")
            os.makedirs(os.path.join(tmp, "node_modules", "junk"))
            with open(os.path.join(tmp, "node_modules", "junk", "x"), "w") as handle:
                handle.write("junk")

            data = create_tarball(tmp, {"name": "demo", "version": "1.0.0"})
            with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
                names = archive.getnames()
            self.assertIn("package/package.json", names)
            self.assertIn("package/lib/a.js", names)
            self.assertFalse(any(name.startswith("package/node_modules") for name in names))


if __name__ == "__main__":
    unittest.main()
