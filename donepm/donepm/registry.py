"""npm registry client."""

from __future__ import annotations

import hashlib
import json
import os
import time
from dataclasses import dataclass, field

from .errors import RegistryError
from .http import HttpClient
from .semver import Version, max_satisfying

DEFAULT_REGISTRY = os.environ.get("DONEPM_REGISTRY", "https://registry.npmjs.org")
CACHE_TTL = 300.0

#: npm serves a much smaller "abbreviated" packument to installers
ABBREVIATED = "application/vnd.npm.install-v1+json"


def default_cache_dir() -> str:
    """Where DonePM keeps downloaded metadata and tarballs."""
    override = os.environ.get("DONEPM_CACHE_DIR")
    if override:
        return override
    base = os.environ.get("XDG_CACHE_HOME") or os.path.join(os.path.expanduser("~"), ".cache")
    return os.path.join(base, "donepm")


@dataclass
class Resolved:
    """A concrete package version selected for installation."""

    name: str
    version: str
    tarball: str
    integrity: str = ""
    shasum: str = ""
    dependencies: dict[str, str] = field(default_factory=dict)
    dev_dependencies: dict[str, str] = field(default_factory=dict)
    optional_dependencies: dict[str, str] = field(default_factory=dict)
    peer_dependencies: dict[str, str] = field(default_factory=dict)
    scripts: dict[str, str] = field(default_factory=dict)
    bin: dict[str, str] = field(default_factory=dict)
    engines: dict[str, str] = field(default_factory=dict)
    os: list[str] = field(default_factory=list)
    cpu: list[str] = field(default_factory=list)
    deprecated: str = ""
    resolved_url: str = ""

    @property
    def key(self) -> str:
        return f"{self.name}@{self.version}"

    def to_dict(self) -> dict:
        data = {
            "version": self.version,
            "resolved": self.resolved_url or self.tarball,
            "integrity": self.integrity,
        }
        if self.shasum:
            data["shasum"] = self.shasum
        if self.dependencies:
            data["dependencies"] = self.dependencies
        if self.optional_dependencies:
            data["optionalDependencies"] = self.optional_dependencies
        if self.peer_dependencies:
            data["peerDependencies"] = self.peer_dependencies
        if self.engines:
            data["engines"] = self.engines
        if self.os:
            data["os"] = self.os
        if self.cpu:
            data["cpu"] = self.cpu
        if self.bin:
            data["bin"] = self.bin
        if self.deprecated:
            data["deprecated"] = self.deprecated
        return data


def parse_spec(spec: str) -> tuple[str, str]:
    """Split ``name@range`` into its parts."""
    spec = spec.strip()
    if spec.startswith("@"):
        slash = spec.find("/")
        at = spec.find("@", slash) if slash != -1 else -1
        if at == -1:
            return spec, "latest"
        return spec[:at], spec[at + 1:] or "latest"
    at = spec.find("@")
    if at == -1:
        return spec, "latest"
    return spec[:at], spec[at + 1:] or "latest"


class Registry:
    """Talks to an npm-compatible registry."""

    def __init__(
        self,
        url: str = DEFAULT_REGISTRY,
        *,
        http: HttpClient | None = None,
        cache_dir: str | None = None,
        offline: bool = False,
    ) -> None:
        self.url = url.rstrip("/")
        self.http = http or HttpClient()
        self.cache_dir = cache_dir
        self.offline = offline
        self._packuments: dict[str, dict] = {}

    # -- metadata ---------------------------------------------------------

    def _cache_path(self, name: str) -> str | None:
        if not self.cache_dir:
            return None
        safe = name.replace("/", "__")
        # the registry is part of the key: the same name on two registries
        # must not share a cached packument
        key = hashlib.sha256(self.url.encode("utf-8")).hexdigest()[:12]
        return os.path.join(self.cache_dir, "packuments", key, f"{safe}.json")

    def _tarball_cache_path(self, resolved: Resolved) -> str | None:
        if not self.cache_dir:
            return None
        key = resolved.integrity or resolved.tarball
        digest = hashlib.sha256(key.encode("utf-8")).hexdigest()
        return os.path.join(self.cache_dir, "tarballs", f"{digest}.tgz")

    def _read_cache(self, name: str) -> dict | None:
        path = self._cache_path(name)
        if not path or not os.path.exists(path):
            return None
        try:
            if time.time() - os.path.getmtime(path) > CACHE_TTL:
                return None
            with open(path, "r", encoding="utf-8") as handle:
                return json.load(handle)
        except (OSError, ValueError):
            return None

    def _write_cache(self, name: str, data: dict) -> None:
        path = self._cache_path(name)
        if not path:
            return
        try:
            os.makedirs(os.path.dirname(path), exist_ok=True)
            tmp = path + ".tmp"
            with open(tmp, "w", encoding="utf-8") as handle:
                json.dump(data, handle)
            os.replace(tmp, path)
        except OSError:
            pass

    def packument(self, name: str) -> dict:
        """Return the metadata document for *name*."""
        if name in self._packuments:
            return self._packuments[name]
        cached = self._read_cache(name)
        if cached is not None:
            self._packuments[name] = cached
            return cached
        if self.offline:
            raise RegistryError(f"offline mode and no cached metadata for {name!r}")
        url = f"{self.url}/{name.replace('/', '%2f')}"
        data = self.http.get_json(url, headers={"Accept": ABBREVIATED})
        if not isinstance(data, dict):
            raise RegistryError(f"unexpected metadata for {name!r}")
        self._packuments[name] = data
        self._write_cache(name, data)
        return data

    # -- resolution -------------------------------------------------------

    def resolve(self, name: str, spec: str = "latest") -> Resolved:
        """Pick the concrete version of *name* that satisfies *spec*."""
        doc = self.packument(name)
        versions = doc.get("versions") or {}
        if not versions:
            raise RegistryError(f"no versions published for {name!r}")

        if spec in {"", "*", "latest"}:
            latest = doc.get("dist-tags", {}).get("latest")
            if latest and latest in versions:
                chosen = latest
            else:
                best = max_satisfying(versions.keys(), "*")
                if best is None:
                    raise RegistryError(f"no usable version for {name!r}")
                chosen = str(best)
        else:
            if spec in versions:
                chosen = spec
            else:
                tag = doc.get("dist-tags", {}).get(spec)
                if tag and tag in versions:
                    chosen = tag
                else:
                    best = max_satisfying(versions.keys(), spec)
                    if best is None:
                        raise RegistryError(f"no version of {name!r} satisfies {spec!r}")
                    chosen = str(best)

        manifest = versions[chosen]
        dist = manifest.get("dist", {})
        return Resolved(
            name=manifest.get("name", name),
            version=chosen,
            tarball=dist.get("tarball", ""),
            integrity=dist.get("integrity", ""),
            shasum=dist.get("shasum", ""),
            dependencies=manifest.get("dependencies") or {},
            dev_dependencies=manifest.get("devDependencies") or {},
            optional_dependencies=manifest.get("optionalDependencies") or {},
            peer_dependencies=manifest.get("peerDependencies") or {},
            scripts=manifest.get("scripts") or {},
            bin=manifest.get("bin") or {},
            engines=manifest.get("engines") or {},
            os=manifest.get("os") or [],
            cpu=manifest.get("cpu") or [],
            deprecated=manifest.get("deprecated") or "",
            resolved_url=dist.get("tarball", ""),
        )

    def resolve_spec(self, spec: str) -> Resolved:
        name, range_ = parse_spec(spec)
        return self.resolve(name, range_)

    # -- tarballs ---------------------------------------------------------

    def tarball(self, resolved: Resolved) -> bytes:
        """Download (or reuse) the tarball for *resolved*."""
        if not resolved.tarball:
            raise RegistryError(f"no tarball URL for {resolved.key}")
        path = self._tarball_cache_path(resolved)
        if path and os.path.exists(path):
            try:
                with open(path, "rb") as handle:
                    return handle.read()
            except OSError:
                pass
        data = self.http.get_bytes(resolved.tarball)
        if path:
            try:
                os.makedirs(os.path.dirname(path), exist_ok=True)
                tmp = path + ".tmp"
                with open(tmp, "wb") as handle:
                    handle.write(data)
                os.replace(tmp, path)
            except OSError:
                pass
        return data

    # -- publishing -------------------------------------------------------

    def publish(self, tarball: bytes, metadata: dict, token: str, *, tag: str = "latest") -> dict:
        url = f"{self.url}/{metadata['name'].replace('/', '%2f')}"
        body = {
            "_id": metadata["name"],
            "name": metadata["name"],
            "description": metadata.get("description", ""),
            "dist-tags": {tag: metadata["version"]},
            "versions": {metadata["version"]: metadata},
            "readme": metadata.get("readme", ""),
            "_attachments": {
                f"{metadata['name']}-{metadata['version']}.tgz": {
                    "content_type": "application/octet-stream",
                    "data": _b64(tarball),
                    "length": len(tarball),
                }
            },
        }
        headers = {"Authorization": f"Bearer {token}"}
        resp = self.http.post_json(url, body, headers=headers)
        if not resp.ok:
            raise RegistryError(f"publish failed: {resp.status} {resp.text[:400]}")
        try:
            return resp.json()
        except ValueError:
            return {"ok": True}


def _b64(data: bytes) -> str:
    import base64

    return base64.b64encode(data).decode("ascii")


def version_object(text: str) -> Version:
    return Version.parse(text)
