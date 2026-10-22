"""Dependency resolution and installation.

Resolution uses npm's flat ``node_modules`` layout with hoisting: a package
is installed at the root when its version satisfies every dependent,
otherwise a nested copy is placed under the requiring package.
"""

from __future__ import annotations

import base64
import hashlib
import os
import stat
from concurrent.futures import ThreadPoolExecutor, as_completed
from collections import deque
from dataclasses import dataclass, field
from typing import Callable, Sequence

from .errors import BlockedCommand, PackageError, RegistryError, SecurityError
from .manifest import Lockfile, LockNode, Manifest
from .registry import DEFAULT_REGISTRY, Resolved, Registry, parse_spec
from .security import Policy, command_binary
from .semver import matches
from .tarball import extract_tarball

ROOT_MANIFEST = "package.json"
LOCKFILE = "package-lock.json"


@dataclass
class InstallResult:
    added: list[str] = field(default_factory=list)
    changed: list[str] = field(default_factory=list)
    removed: list[str] = field(default_factory=list)
    skipped_scripts: list[str] = field(default_factory=list)
    blocked_scripts: list[str] = field(default_factory=list)
    failed_scripts: list[str] = field(default_factory=list)
    lockfile: Lockfile | None = None
    dry_run: bool = False

    @property
    def touched(self) -> bool:
        return bool(self.added or self.changed or self.removed)

    def summary(self) -> str:
        lines = []
        if self.added:
            lines.append(f"added {len(self.added)} packages")
        if self.changed:
            lines.append(f"changed {len(self.changed)} packages")
        if self.removed:
            lines.append(f"removed {len(self.removed)} packages")
        if not lines:
            lines.append("up to date")
        return ", ".join(lines)


def _bin_dir(root: str) -> str:
    return os.path.join(root, "node_modules", ".bin")


def _package_dir(root: str, rel: str) -> str:
    if not rel:
        return root
    return os.path.join(root, rel)


def verify_integrity(data: bytes, integrity: str) -> None:
    """Check an npm ``integrity`` field (``sha512-<b64>`` or ``sha1-<b64>``)."""
    if not integrity:
        return
    if "-" not in integrity:
        raise PackageError(f"malformed integrity value: {integrity!r}")
    algorithm, _, expected = integrity.rpartition("-")
    algorithm = algorithm.lower()
    if algorithm not in {"sha1", "sha256", "sha512"}:
        return
    digest = hashlib.new(algorithm, data).digest()
    try:
        want = base64.b64decode(expected, validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise PackageError(f"malformed integrity value: {integrity!r}") from exc
    if digest != want:
        raise PackageError(f"integrity check failed (expected {integrity})")


def _write_bin_shims(root: str, resolved: Resolved) -> None:
    if not resolved.bin:
        return
    bins = resolved.bin if isinstance(resolved.bin, dict) else {resolved.name: resolved.bin}
    bin_dir = _bin_dir(root)
    os.makedirs(bin_dir, exist_ok=True)
    for alias, script in bins.items():
        posix = os.path.join(bin_dir, alias)
        with open(posix, "w", encoding="utf-8") as handle:
            handle.write(
                "#!/bin/sh\n"
                f'basedir=$(dirname "$(echo "$0" | sed -e \'s,\\\\,/,g\')")\n'
                "if command -v donejs >/dev/null 2>&1; then\n"
                f'  exec donejs "$basedir/../{resolved.name}/{script}" "$@"\n'
                "fi\n"
                f'exec node "$basedir/../{resolved.name}/{script}" "$@"\n'
            )
        os.chmod(posix, os.stat(posix).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        cmd = os.path.join(bin_dir, f"{alias}.cmd")
        with open(cmd, "w", encoding="utf-8") as handle:
            handle.write(f"@echo off\r\nnode \"%~dp0\\..\\{resolved.name}\\{script}\" %*\r\n")


class Installer:
    def __init__(
        self,
        root: str,
        *,
        registry: Registry | None = None,
        policy: Policy | None = None,
        workers: int = 8,
        dry_run: bool = False,
        runner: Callable[[str, str], int] | None = None,
        reporter: Callable[[str], None] | None = None,
    ) -> None:
        self.root = os.path.abspath(root)
        self.registry = registry or Registry(DEFAULT_REGISTRY)
        self.policy = policy or Policy()
        self.workers = max(1, workers)
        self.dry_run = dry_run
        self._runner = runner
        self._report = reporter or (lambda _msg: None)

    # -- paths ------------------------------------------------------------

    @property
    def manifest_path(self) -> str:
        return os.path.join(self.root, ROOT_MANIFEST)

    @property
    def lockfile_path(self) -> str:
        return os.path.join(self.root, LOCKFILE)

    # -- public API -------------------------------------------------------

    def install(
        self,
        specs: Sequence[str] = (),
        *,
        save: bool = True,
        include_dev: bool = True,
        save_dev: bool = False,
        run_scripts: bool = False,
    ) -> InstallResult:
        manifest = Manifest.load(self.manifest_path)

        # Existing dependencies stay in the plan so `install <pkg>` never
        # prunes a tree that is already there; explicit specs win over the
        # range currently recorded in package.json.
        wanted: dict[str, str] = dict(manifest.runtime_dependencies(dev=include_dev))
        for spec in specs:
            name, rng = parse_spec(spec)
            wanted[name] = rng
        queue: deque[tuple[str, str, str]] = deque(
            (name, rng, "") for name, rng in wanted.items()
        )

        plan = self._resolve(queue)
        result = InstallResult(dry_run=self.dry_run)

        if save and specs:
            for spec in specs:
                name, rng = parse_spec(spec)
                resolved = plan.get(f"node_modules/{name}")
                if resolved is None:
                    continue
                manifest.add_dependency(name, _save_range(rng, resolved.version), dev=save_dev)

        result.lockfile = self._build_lock(manifest, plan)

        if self.dry_run:
            for rel, resolved in plan.items():
                result.added.append(f"{resolved.name}@{resolved.version}")
            return result

        installed = self._fetch_all(plan)
        for rel, resolved in installed.items():
            result.added.append(f"{resolved.name}@{resolved.version}")

        # bin shims
        for rel, resolved in installed.items():
            _write_bin_shims(self.root, resolved)

        if run_scripts:
            self._run_lifecycle(installed, result)

        if save:
            manifest.save(self.manifest_path)
        result.lockfile.save(self.lockfile_path)
        self._prune(plan)
        return result

    def uninstall(self, names: Sequence[str], *, save: bool = True) -> InstallResult:
        manifest = Manifest.load(self.manifest_path)
        lock = Lockfile.load(self.lockfile_path)
        result = InstallResult(dry_run=self.dry_run)
        for name in names:
            if save:
                manifest.remove_dependency(name)
            removed = lock.remove(name)
            if removed:
                result.removed.append(name)
            for rel in list(lock.packages):
                if rel == f"node_modules/{name}" or rel.endswith(f"/node_modules/{name}"):
                    path = _package_dir(self.root, rel)
                    if not self.dry_run and os.path.isdir(path):
                        _rmtree(path)
            path = os.path.join(self.root, "node_modules", name)
            if not self.dry_run and os.path.isdir(path):
                _rmtree(path)
        if save:
            manifest.save(self.manifest_path)
        lock.save(self.lockfile_path)
        return result

    def list_installed(self, *, dev: bool = True) -> list[str]:
        base = os.path.join(self.root, "node_modules")
        found: list[str] = []
        if not os.path.isdir(base):
            return found
        for name in sorted(os.listdir(base)):
            if name.startswith(".") or name == ".bin":
                continue
            pkg = os.path.join(base, name, ROOT_MANIFEST)
            if os.path.exists(pkg):
                manifest = Manifest.load(pkg)
                if manifest.name or manifest.version:
                    found.append(f"{manifest.name or name}@{manifest.version}")
        return found

    def tree(self) -> dict[str, dict[str, str]]:
        tree: dict[str, dict[str, str]] = {}
        base = os.path.join(self.root, "node_modules")
        if not os.path.isdir(base):
            return tree
        for name in sorted(os.listdir(base)):
            pkg = os.path.join(base, name, ROOT_MANIFEST)
            if not os.path.exists(pkg):
                continue
            manifest = Manifest.load(pkg)
            tree[manifest.name or name] = dict(manifest.dependencies)
        return tree

    # -- resolution -------------------------------------------------------

    def _fetch_metadata(
        self, pairs: set[tuple[str, str]]
    ) -> dict[tuple[str, str], Resolved]:
        """Resolve ``(name, range)`` pairs, fetching packuments in parallel."""
        out: dict[tuple[str, str], Resolved] = {}
        if not pairs:
            return out
        if len(pairs) == 1:
            name, rng = next(iter(pairs))
            out[(name, rng)] = self.registry.resolve(name, rng)
            return out
        with ThreadPoolExecutor(max_workers=self.workers) as pool:
            futures = {
                pool.submit(self.registry.resolve, name, rng): (name, rng)
                for name, rng in pairs
            }
            for future in as_completed(futures):
                key = futures[future]
                out[key] = future.result()
        return out

    def _resolve(self, queue: deque[tuple[str, str, str]]) -> dict[str, Resolved]:
        """Turn a work queue into ``node_modules relative path -> Resolved``.

        Metadata is fetched a level at a time, in parallel. Packages are
        hoisted to the root ``node_modules`` whenever the copy already there
        satisfies the requested range; otherwise a nested copy is placed
        under the requiring package.
        """
        plan: dict[str, Resolved] = {}
        root_versions: dict[str, str] = {}
        seen: set[tuple[str, str]] = set()
        errors: list[str] = []
        pending: list[tuple[str, str, str]] = list(queue)

        while pending:
            wanted: dict[tuple[str, str], str] = {}
            for name, rng, parent in pending:
                key = (parent, name)
                if key in seen:
                    continue
                seen.add(key)
                wanted[key] = rng

            try:
                fetched = self._fetch_metadata(
                    {(name, rng) for (_parent, name), rng in wanted.items()}
                )
            except RegistryError as exc:
                errors.append(str(exc))
                break

            pending = []
            for (parent, name), rng in wanted.items():
                resolved = fetched.get((name, rng))
                if resolved is None:
                    errors.append(f"no version of {name!r} satisfies {rng!r}")
                    continue

                if parent == "":
                    rel = f"node_modules/{name}"
                    root_versions[name] = resolved.version
                else:
                    root_version = root_versions.get(name)
                    if root_version is None:
                        rel = f"node_modules/{name}"  # hoist
                        root_versions[name] = resolved.version
                    elif matches(root_version, rng):
                        continue  # the hoisted copy already satisfies this range
                    else:
                        rel = f"{parent}/node_modules/{name}"

                plan[rel] = resolved
                for dep_name, dep_range in resolved.dependencies.items():
                    pending.append((dep_name, dep_range, rel))
                for dep_name, dep_range in resolved.optional_dependencies.items():
                    pending.append((dep_name, dep_range, rel))

        if errors:
            raise RegistryError("; ".join(errors))
        return plan

    def _build_lock(self, manifest: Manifest, plan: dict[str, Resolved]) -> Lockfile:
        lock = Lockfile.load(self.lockfile_path)
        lock.name = manifest.name
        lock.version = manifest.version
        lock.requires = True
        # Root node
        lock.packages[""] = LockNode(
            path="",
            version=manifest.version,
            dependencies=dict(manifest.dependencies),
            optional_dependencies=dict(manifest.optional_dependencies),
        )
        for rel, resolved in plan.items():
            node = LockNode(
                path=rel,
                version=resolved.version,
                resolved=resolved.resolved_url or resolved.tarball,
                integrity=resolved.integrity,
                dependencies=dict(resolved.dependencies),
                optional_dependencies=dict(resolved.optional_dependencies),
            )
            lock.packages[rel] = node
        return lock

    # -- fetching ---------------------------------------------------------

    def _fetch_all(self, plan: dict[str, Resolved]) -> dict[str, Resolved]:
        if not plan:
            return {}
        installed: dict[str, Resolved] = {}
        with ThreadPoolExecutor(max_workers=self.workers) as pool:
            futures = {pool.submit(self._fetch_one, rel, resolved): rel for rel, resolved in plan.items()}
            for future in as_completed(futures):
                rel, resolved = future.result()
                installed[rel] = resolved
        return installed

    def _fetch_one(self, rel: str, resolved: Resolved) -> tuple[str, Resolved]:
        data = self.registry.tarball(resolved)
        verify_integrity(data, resolved.integrity)
        dest = _package_dir(self.root, rel)
        extract_tarball(data, dest)
        return rel, resolved

    # -- lifecycle scripts ------------------------------------------------

    def _run_lifecycle(self, installed: dict[str, Resolved], result: InstallResult) -> None:
        root_manifest = Manifest.load(self.manifest_path)
        for rel, resolved in installed.items():
            self._run_package_scripts(resolved, rel, result)
        for hook in ("preinstall", "install", "postinstall"):
            command = root_manifest.scripts.get(hook)
            if command:
                self._execute(f"{root_manifest.name or 'root'} {hook}", command, self.root, result)

    def _run_package_scripts(self, resolved: Resolved, rel: str, result: InstallResult) -> None:
        dest = _package_dir(self.root, rel)
        for hook in ("preinstall", "install", "postinstall"):
            command = resolved.scripts.get(hook)
            if command:
                self._execute(f"{resolved.name} {hook}", command, dest, result)

    def _execute(self, label: str, command: str, cwd: str, result: InstallResult) -> None:
        binary = command_binary(command)
        try:
            self.policy.authorize(f"{label}", command, binary=binary)
        except BlockedCommand as exc:
            self._report(f"  blocked {label}: {exc}")
            result.blocked_scripts.append(f"{label}: {command}")
            return
        except SecurityError as exc:
            self._report(f"  skipped {label}: {exc}")
            result.skipped_scripts.append(f"{label}: {command}")
            return
        if self._runner is None:
            self._report(f"  would run {label}: {command}")
            return
        self._report(f"  running {label}: {command}")
        code = self._runner(cwd, command)
        if code:
            self._report(f"  {label} exited with status {code}")
            result.failed_scripts.append(f"{label}: {command}")

    # -- pruning ----------------------------------------------------------

    def _prune(self, plan: dict[str, Resolved]) -> None:
        """Remove node_modules entries that the new plan no longer contains."""
        lock = Lockfile.load(self.lockfile_path)
        base = os.path.join(self.root, "node_modules")
        if not os.path.isdir(base):
            return
        wanted = {rel for rel in plan}
        for rel in list(lock.packages):
            if rel in wanted or rel == "":
                continue
            path = _package_dir(self.root, rel)
            if os.path.isdir(path) and not os.path.islink(path):
                _rmtree(path)
            lock.packages.pop(rel, None)
        lock.save(self.lockfile_path)


def _save_range(rng: str, resolved_version: str) -> str:
    """Range to record in package.json after ``donepm install foo``."""
    if rng in {"", "*", "latest"}:
        return f"^{resolved_version}"
    return rng


def _rmtree(path: str) -> None:
    import shutil

    shutil.rmtree(path, ignore_errors=True)
