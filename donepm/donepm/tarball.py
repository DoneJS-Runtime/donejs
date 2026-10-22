"""Hardened extraction of npm tarballs.

npm packages arrive as ``tar.gz`` blobs whose members start with
``package/``.  Before anything is written to disk the archive is checked
for the classic package-manager attacks: path traversal, absolute paths,
symlink/hardlink escapes and absurd archive sizes.
"""

from __future__ import annotations

import os
import tarfile
from io import BytesIO

from .errors import PackageError

#: tar members are capped so a corrupted archive cannot fill the disk.
MAX_MEMBER_BYTES = 512 * 1024 * 1024
MAX_TOTAL_BYTES = 2 * 1024 * 1024 * 1024
MAX_FILES = 100_000

_PREFIX = "package/"


class UnsafeArchive(PackageError):
    """The tarball tried to escape the extraction directory."""


def _safe_target(dest: str, member: tarfile.TarInfo) -> str:
    name = member.name
    if name.startswith("/") or (len(name) > 1 and name[1] == ":"):
        raise UnsafeArchive(f"absolute path in archive: {member.name!r}")
    # Strip the npm "package/" prefix; everything else stays relative.
    parts = name.split("/")
    if parts and parts[0] == "package":
        parts = parts[1:]
    if not parts:
        return dest
    rel = "/".join(parts)
    target = os.path.join(dest, rel)
    # Resolve without touching the filesystem so symlinks in *dest* cannot
    # be followed.
    root = os.path.realpath(dest)
    candidate = os.path.realpath(os.path.join(root, rel))
    if candidate != root and not candidate.startswith(root + os.sep):
        raise UnsafeArchive(f"path escapes the install directory: {member.name!r}")
    return target


def extract_tarball(
    data: bytes,
    dest: str,
    *,
    strip_prefix: bool = True,
    allow_symlinks: bool = False,
) -> list[str]:
    """Extract an npm tarball into *dest* and return the files written."""
    if not os.path.isdir(dest):
        os.makedirs(dest, exist_ok=True)

    written: list[str] = []
    total = 0
    with tarfile.open(fileobj=BytesIO(data), mode="r:*") as archive:
        members = archive.getmembers()
        if len(members) > MAX_FILES:
            raise UnsafeArchive(f"archive has too many files: {len(members)}")

        for member in members:
            if member.issym() or member.islnk():
                if not allow_symlinks:
                    raise UnsafeArchive(f"link entry in archive: {member.name!r}")
                continue

            if not member.isfile() and not member.isdir():
                continue

            if member.size > MAX_MEMBER_BYTES:
                raise UnsafeArchive(f"archive member too large: {member.name!r}")

            total += member.size
            if total > MAX_TOTAL_BYTES:
                raise UnsafeArchive("archive expands beyond the size limit")

            target = _safe_target(dest, member)
            if member.isdir():
                os.makedirs(target, exist_ok=True)
                continue

            parent = os.path.dirname(target)
            if parent:
                os.makedirs(parent, exist_ok=True)
            f = archive.extractfile(member)
            if f is None:
                continue
            with f, open(target, "wb") as out:
                out.write(f.read())
            # Clear setuid/setgid/sticky bits that npm packages never need.
            mode = member.mode & 0o777
            os.chmod(target, mode)
            written.append(os.path.relpath(target, dest))

    if not strip_prefix:
        return written
    return [w for w in written if w]


def read_manifest(data: bytes) -> dict:
    """Pull the ``package/package.json`` out of an npm tarball."""
    with tarfile.open(fileobj=BytesIO(data), mode="r:*") as archive:
        for member in archive.getmembers():
            parts = member.name.split("/")
            if len(parts) >= 2 and parts[0] == "package" and parts[-1] == "package.json":
                f = archive.extractfile(member)
                if f is None:
                    continue
                import json

                return json.loads(f.read().decode("utf-8"))
    raise PackageError("tarball does not contain package/package.json")


def strip_package_prefix(name: str) -> str:
    if name.startswith(_PREFIX):
        return name[len(_PREFIX):]
    return name


#: directories that never belong in a published tarball
_EXCLUDED_DIRS = frozenset({"node_modules", ".git", ".hg", ".svn", ".cache", ".npm"})


def create_tarball(root: str, manifest: dict) -> bytes:
    """Pack *root* into an npm style tarball (members under ``package/``)."""
    import io
    import json
    import tarfile

    allowed = manifest.get("files")
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as archive:
        package_json = json.dumps(manifest, indent=2).encode("utf-8")
        info = tarfile.TarInfo("package/package.json")
        info.size = len(package_json)
        info.mode = 0o644
        archive.addfile(info, io.BytesIO(package_json))

        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if d not in _EXCLUDED_DIRS)
            for filename in sorted(filenames):
                absolute = os.path.join(dirpath, filename)
                relative = os.path.relpath(absolute, root)
                if relative == "package.json":
                    continue
                parts = relative.split(os.sep)
                if any(part in _EXCLUDED_DIRS for part in parts):
                    continue
                if allowed is not None:
                    top = parts[0]
                    if top not in allowed and relative not in allowed:
                        continue
                if os.path.islink(absolute) or not os.path.isfile(absolute):
                    continue
                info = archive.gettarinfo(absolute, arcname=f"package/{relative}")
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                info.mode = 0o755 if info.mode & 0o100 else 0o644
                with open(absolute, "rb") as handle:
                    archive.addfile(info, handle)
    return buf.getvalue()
