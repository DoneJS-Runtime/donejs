"""package.json and package-lock.json handling.

The lockfile is written in npm's ``lockfileVersion: 3`` shape so that
existing npm/node tooling keeps working on the same tree.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from typing import Any

from .errors import LockfileError, PackageError

LOCKFILE_VERSION = 3


def read_json(path: str) -> Any:
    if not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as handle:
        try:
            return json.load(handle)
        except ValueError as exc:
            raise PackageError(f"{path} is not valid JSON: {exc}") from exc


def write_json(path: str, data: Any, *, indent: int = 2) -> None:
    os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as handle:
        json.dump(data, handle, indent=indent, sort_keys=True)
        handle.write("\n")
    os.replace(tmp, path)


@dataclass
class Manifest:
    name: str = ""
    version: str = "0.0.0"
    description: str = ""
    main: str = ""
    private: bool = False
    dependencies: dict[str, str] = field(default_factory=dict)
    dev_dependencies: dict[str, str] = field(default_factory=dict)
    optional_dependencies: dict[str, str] = field(default_factory=dict)
    peer_dependencies: dict[str, str] = field(default_factory=dict)
    scripts: dict[str, str] = field(default_factory=dict)
    bin: dict[str, str] | str = ""
    engines: dict[str, str] = field(default_factory=dict)
    os: list[str] = field(default_factory=list)
    cpu: list[str] = field(default_factory=list)
    license: str = ""
    raw: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def load(cls, path: str) -> "Manifest":
        data = read_json(path)
        if data is None:
            return cls()
        if not isinstance(data, dict):
            raise PackageError(f"{path} must contain a JSON object")
        return cls._from_dict(data)

    @classmethod
    def _from_dict(cls, data: dict) -> "Manifest":
        return cls(
            name=data.get("name", ""),
            version=str(data.get("version", "0.0.0")),
            description=data.get("description", ""),
            main=data.get("main", ""),
            private=bool(data.get("private", False)),
            dependencies=dict(data.get("dependencies") or {}),
            dev_dependencies=dict(data.get("devDependencies") or {}),
            optional_dependencies=dict(data.get("optionalDependencies") or {}),
            peer_dependencies=dict(data.get("peerDependencies") or {}),
            scripts=dict(data.get("scripts") or {}),
            bin=data.get("bin", ""),
            engines=dict(data.get("engines") or {}),
            os=list(data.get("os") or []),
            cpu=list(data.get("cpu") or []),
            license=data.get("license", ""),
            raw=dict(data),
        )

    @classmethod
    def create(cls, name: str, version: str = "1.0.0", *, description: str = "") -> "Manifest":
        return cls(name=name, version=version, description=description)

    # -- persistence ------------------------------------------------------

    def to_dict(self, *, include_raw: bool = True) -> dict[str, Any]:
        data: dict[str, Any] = dict(self.raw) if include_raw else {}
        data["name"] = self.name
        data["version"] = self.version
        data["description"] = self.description
        data["private"] = self.private
        for key, value in (
            ("main", self.main),
            ("dependencies", self.dependencies),
            ("devDependencies", self.dev_dependencies),
            ("optionalDependencies", self.optional_dependencies),
            ("peerDependencies", self.peer_dependencies),
            ("scripts", self.scripts),
            ("bin", self.bin),
            ("engines", self.engines),
            ("os", self.os),
            ("cpu", self.cpu),
            ("license", self.license),
        ):
            if value:
                data[key] = value
            else:
                data.pop(key, None)
        return data

    def save(self, path: str) -> None:
        write_json(path, self.to_dict())

    # -- helpers ----------------------------------------------------------

    def runtime_dependencies(self, *, dev: bool = True, optional: bool = True) -> dict[str, str]:
        merged: dict[str, str] = {}
        merged.update(self.dependencies)
        if optional:
            merged.update(self.optional_dependencies)
        if dev:
            merged.update(self.dev_dependencies)
        return merged

    def all_dependencies(self) -> dict[str, str]:
        return self.runtime_dependencies(dev=True, optional=True)

    def add_dependency(self, name: str, version: str, *, dev: bool = False) -> None:
        target = self.dev_dependencies if dev else self.dependencies
        target[name] = version

    def remove_dependency(self, name: str) -> bool:
        removed = False
        for group in (self.dependencies, self.dev_dependencies, self.optional_dependencies):
            if name in group:
                del group[name]
                removed = True
        return removed


@dataclass
class LockNode:
    path: str
    version: str = ""
    resolved: str = ""
    integrity: str = ""
    dependencies: dict[str, str] = field(default_factory=dict)
    optional_dependencies: dict[str, str] = field(default_factory=dict)
    dev: bool = False
    link: bool = False

    def to_dict(self) -> dict[str, Any]:
        data: dict[str, Any] = {}
        if self.version:
            data["version"] = self.version
        if self.resolved:
            data["resolved"] = self.resolved
        if self.integrity:
            data["integrity"] = self.integrity
        if self.dependencies:
            data["dependencies"] = self.dependencies
        if self.optional_dependencies:
            data["optionalDependencies"] = self.optional_dependencies
        if self.dev:
            data["dev"] = True
        if self.link:
            data["link"] = True
        return data

    @classmethod
    def from_dict(cls, path: str, data: dict) -> "LockNode":
        return cls(
            path=path,
            version=data.get("version", ""),
            resolved=data.get("resolved", ""),
            integrity=data.get("integrity", ""),
            dependencies=dict(data.get("dependencies") or {}),
            optional_dependencies=dict(data.get("optionalDependencies") or {}),
            dev=bool(data.get("dev", False)),
            link=bool(data.get("link", False)),
        )


@dataclass
class Lockfile:
    name: str = ""
    version: str = "0.0.0"
    packages: dict[str, LockNode] = field(default_factory=dict)
    requires: bool = True

    @classmethod
    def load(cls, path: str) -> "Lockfile":
        data = read_json(path)
        if data is None:
            return cls()
        if not isinstance(data, dict):
            raise LockfileError(f"{path} must contain a JSON object")
        packages: dict[str, LockNode] = {}
        for key, value in (data.get("packages") or {}).items():
            if not isinstance(value, dict):
                continue
            if value.get("link"):
                continue
            packages[key] = LockNode.from_dict(key, value)
        return cls(
            name=data.get("name", ""),
            version=str(data.get("version", "0.0.0")),
            packages=packages,
            requires=bool(data.get("requires", True)),
        )

    @classmethod
    def empty(cls, name: str = "", version: str = "0.0.0") -> "Lockfile":
        return cls(name=name, version=version)

    def to_dict(self) -> dict[str, Any]:
        packages: dict[str, Any] = {}
        if self.name or self.version:
            root: dict[str, Any] = {"name": self.name, "version": self.version}
            packages[""] = root
        for path, node in self.packages.items():
            if node.version or node.resolved or node.dependencies:
                packages[path] = node.to_dict()
        return {
            "name": self.name,
            "version": self.version,
            "lockfileVersion": LOCKFILE_VERSION,
            "requires": self.requires,
            "packages": packages,
        }

    def save(self, path: str) -> None:
        write_json(path, self.to_dict())

    # -- helpers ----------------------------------------------------------

    def set_root(self, manifest: Manifest) -> None:
        self.name = manifest.name
        self.version = manifest.version
        self.packages[""] = LockNode(
            path="",
            version=manifest.version,
            dependencies=dict(manifest.dependencies),
            optional_dependencies=dict(manifest.optional_dependencies),
        )

    def add(self, path: str, node: LockNode) -> None:
        self.packages[path] = node

    def get(self, path: str) -> LockNode | None:
        return self.packages.get(path)

    def find(self, name: str) -> list[LockNode]:
        suffix = f"node_modules/{name}"
        return [node for path, node in self.packages.items() if path.endswith(suffix)]

    def remove(self, name: str) -> int:
        removed = 0
        for path in list(self.packages):
            if path.endswith(f"node_modules/{name}"):
                del self.packages[path]
                removed += 1
        return removed

    def __len__(self) -> int:
        return len(self.packages)
