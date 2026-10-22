"""Minimal SemVer 2.0 parsing, ordering and range matching.

Enough to resolve npm dependency ranges (``^``, ``~``, comparators,
whitespace-separated AND, ``||``) without pulling in a dependency.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import total_ordering
from typing import Iterable

_VERSION_RE = re.compile(
    r"""^
    (?P<major>0|[1-9]\d*)
    (?:\.(?P<minor>0|[1-9]\d*|\*))?
    (?:\.(?P<patch>0|[1-9]\d*|\*))?
    (?:-(?P<pre>[0-9A-Za-z.-]+))?
    (?:\+(?P<build>[0-9A-Za-z.-]+))?
    $""",
    re.VERBOSE,
)

_NUM_RE = re.compile(r"^\d+$")
_PRERE_RE = re.compile(r"^[0-9A-Za-z.-]+$")


class VersionError(ValueError):
    """Not a valid SemVer version."""


@total_ordering
@dataclass(frozen=True)
class Version:
    major: int
    minor: int = 0
    patch: int = 0
    prerelease: tuple[str, ...] = ()
    build: str = ""

    @classmethod
    def parse(cls, text: str) -> "Version":
        text = text.strip()
        if not text:
            raise VersionError("empty version")
        if text[0] in "v=":
            text = text[1:]
        if "+" in text and "-" not in text.split("+")[0]:
            pass
        match = _VERSION_RE.match(text)
        if not match:
            raise VersionError(f"invalid semver version: {text!r}")
        prerelease = match.group("pre")
        return cls(
            major=int(match.group("major")),
            minor=0 if match.group("minor") in (None, "*") else int(match.group("minor")),
            patch=0 if match.group("patch") in (None, "*") else int(match.group("patch")),
            prerelease=tuple(prerelease.split(".")) if prerelease else (),
            build=match.group("build") or "",
        )

    def __str__(self) -> str:
        out = f"{self.major}.{self.minor}.{self.patch}"
        if self.prerelease:
            out += "-" + ".".join(self.prerelease)
        if self.build:
            out += "+" + self.build
        return out

    def _key(self) -> tuple:
        pre: tuple = ()
        if self.prerelease:
            parts = []
            for ident in self.prerelease:
                if _NUM_RE.match(ident):
                    parts.append((0, int(ident), ""))
                else:
                    parts.append((1, 0, ident))
            pre = tuple(parts)
        else:
            # a release sorts after any prerelease of the same core version
            pre = ((2, 0, ""),)
        return (self.major, self.minor, self.patch, pre)

    def __lt__(self, other: "Version") -> bool:
        return self._key() < other._key()

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Version) and self._key() == other._key()


def _prerelease_ident(value: str) -> tuple:
    if _NUM_RE.match(value):
        return (0, int(value), "")
    return (1, 0, value)


def _valid_versions(candidates: Iterable[str]) -> list[Version]:
    out: list[Version] = []
    for candidate in candidates:
        try:
            out.append(Version.parse(candidate))
        except VersionError:
            continue
    return out


# --- ranges ---------------------------------------------------------------

def _split_and(clause: str) -> list[str]:
    """Split a clause into comparators, re-joining ``>=`` and its version."""
    raw = [part for part in re.split(r"\s+", clause.strip()) if part]
    parts: list[str] = []
    index = 0
    while index < len(raw):
        token = raw[index]
        if token in {">=", "<=", ">", "<", "=", "~", "^"} and index + 1 < len(raw):
            parts.append(token + raw[index + 1])
            index += 2
            continue
        parts.append(token)
        index += 1
    return parts


def _comparator(comp: str, v: Version) -> bool:
    match = re.match(r"^(>=|<=|>|<|=|~|\^)?\s*(.+)$", comp)
    if not match:
        return False
    op = match.group(1) or "="
    target = match.group(2).strip()

    if target in {"", "*", "x", "X", "latest"}:
        return True

    if op in {"~", "^"}:
        try:
            tv = Version.parse(target)
        except VersionError:
            return False
        if v < tv:
            return False
        if op == "~":
            # tilde: bump the second component (or first when unspecified)
            upper = Version(tv.major, tv.minor + 1, 0) if tv.minor else Version(tv.major + 1, 0, 0)
            return v < upper
        if tv.major > 0:
            return v.major == tv.major
        if tv.minor > 0:
            return v.major == 0 and v.minor == tv.minor
        return v.major == 0 and v.minor == 0 and v.patch == tv.patch

    # partial targets: "1", "1.2", "1.2.x"
    partial: list[int] | None = None
    if re.search(r"(^|\.)[xX*]($|\.)", target):
        partial = [int(n) for n in target.split(".") if n.isdigit()]
    elif re.fullmatch(r"\d+(\.\d+){0,2}", target):
        numbers = [int(n) for n in target.split(".")]
        if len(numbers) < 3:
            partial = numbers

    if partial is not None:
        actual = [v.major, v.minor, v.patch]
        if op == "=":
            return actual[: len(partial)] == partial
        target = ".".join(str(n) for n in (partial + [0, 0, 0])[:3])

    try:
        tv = Version.parse(target)
    except VersionError:
        return False

    if op == "=":
        return v == tv
    if op == ">":
        return v > tv
    if op == ">=":
        return v >= tv
    if op == "<":
        return v < tv
    if op == "<=":
        return v <= tv
    return False


def matches(version: Version | str, spec: str) -> bool:
    """True when *version* satisfies the npm range *spec*."""
    if isinstance(version, str):
        try:
            version = Version.parse(version)
        except VersionError:
            return False
    spec = (spec or "").strip()
    if not spec:
        return True
    for alternative in spec.split("||"):
        if _matches_and(version, alternative):
            return True
    return False


def _matches_and(version: Version, clause: str) -> bool:
    clause = clause.strip()
    # hyphen ranges: "1.2.3 - 2.3.4"
    if " - " in clause:
        low, _, high = clause.partition(" - ")
        try:
            lv, hv = Version.parse(low.strip()), Version.parse(high.strip())
        except VersionError:
            return False
        return lv <= version <= hv
    parts = _split_and(clause)
    if not parts:
        return True
    return all(_comparator(part, version) for part in parts)


def max_satisfying(versions: Iterable[str], spec: str) -> Version | None:
    """Highest version from *versions* that satisfies *spec*."""
    best: Version | None = None
    for candidate in _valid_versions(versions):
        if not matches(candidate, spec):
            continue
        if best is None or candidate > best:
            best = candidate
    return best


def sort_versions(versions: Iterable[str]) -> list[Version]:
    return sorted(_valid_versions(versions))
