"""Security policy for DonePM.

DonePM never executes anything without permission from the user:

* Lifecycle scripts (``preinstall``, ``install``, ``postinstall``, ...) are
  skipped by default.  They only run when the user approves them, either
  up front (``--allow-scripts`` / ``DONEPM_ALLOW_SCRIPTS=1``) or
  interactively at the prompt.
* Every command that is about to run is scanned.  Commands that match a
  *block* rule are refused outright, even with approval.
* Commands that match a *warn* rule need approval like everything else, but
  the reason is shown to the user before they decide.
* Binaries are not executed by name unless they are on the allowlist or the
  user approves that specific binary.
"""

from __future__ import annotations

import os
import re
import sys
from dataclasses import dataclass, field
from typing import Iterable, Sequence

from .errors import BlockedCommand, PermissionDenied

# --- rules ----------------------------------------------------------------

BLOCK = "block"
WARN = "warn"

#: (regex, reason, severity)
_RULES: tuple[tuple[str, str, str], ...] = (
    # remote code execution
    (r"\|\s*(?:sudo\s+)?(?:ba|z|k|da|a|fi)?sh\b", "pipes output into a shell", BLOCK),
    (r"\b(?:curl|wget)\b[^|;&]*\|\s*(?:sudo\s+)?\S+", "downloads and pipes into another program", BLOCK),
    (r"\bbase64\s+(?:-d|-D|--decode)\b", "decodes a base64 payload", BLOCK),
    (r"\b(?:xxd|openssl\s+enc)\b[^|;&]*\|\s*\S+", "decodes and pipes into another program", BLOCK),
    (r"/dev/(?:tcp|udp)/", "opens a raw network socket", BLOCK),
    (r"\b(?:nc|ncat|netcat)\b[^|;&]*\s-[a-z]*e\b", "starts a netcat shell", BLOCK),
    (r"\beval\s+[\"'$]", "evaluates a dynamic string", WARN),
    (r"\bexec\s+[\"'$]", "executes a dynamic string", WARN),
    # destructive filesystem access
    (r"\brm\s+-[a-zA-Z]*[rf][a-zA-Z]*\s+(?:/|~|\*|/\*|\.?/\*)(?:\s|$)", "recursively deletes a root, home or wildcard path", BLOCK),
    (r"\brm\s+-[a-zA-Z]*[rf][a-zA-Z]*\s+-\S*\s+(?:/|~|\*|\.?/\*)", "recursively deletes a root, home or wildcard path", BLOCK),
    (r"\bmkfs(?:\.[a-z0-9]+)?\b", "formats a filesystem", BLOCK),
    (r"\bdd\b[^|;&]*\bof=/dev/", "writes raw data to a device", BLOCK),
    (r">\s*/etc/(?:passwd|shadow|sudoers)\b", "overwrites a system account file", BLOCK),
    (r"\b(?:tee|dd|mv)\b[^|;&]*\s*/etc/", "writes into a system directory", BLOCK),
    (r"\bchmod\s+(?:-R\s+)?(?:777|a\+rwx|\+s)\b", "grants wide or setuid permissions", BLOCK),
    (r":\(\)\s*\{[^}]*\|[^}]*&[^}]*\}\s*;\s*:", "fork bomb", BLOCK),
    (r"\bhistory\s+-c\b", "clears the shell history", WARN),
    (r"\bunset\s+HISTFILE\b", "disables the shell history", WARN),
    # privilege and persistence
    (r"\b(?:shutdown|reboot|halt|poweroff)\b", "shuts the machine down", BLOCK),
    (r"\bcrontab\s+-[a-z]*\b", "modifies the cron table", BLOCK),
    (r"\bsystemctl\b[^|;&]*\b(?:enable|start)\b", "enables a system service", BLOCK),
    (r"\bsudo\b", "escalates privileges with sudo", WARN),
    (r"\bsu\s+-\b", "escalates privileges with su", WARN),
    # exfiltration
    (r"\b(?:curl|wget)\b[^|;&]*\s+(?:-T|--upload-file|-F|--form|--data(?:-binary|-raw|-urlencode)?|-d)\b", "uploads data to a remote host", BLOCK),
    (r"\b(?:ssh|scp|rsync)\b[^|;&]*\s+\S+@\S+:", "copies data to a remote host", WARN),
    (r"\bnc\b[^|;&]*\s\S+\s+\d+\b", "sends data over a raw socket", BLOCK),
)


@dataclass(frozen=True)
class Finding:
    """One rule hit inside a command."""

    severity: str
    reason: str
    excerpt: str

    @property
    def blocking(self) -> bool:
        return self.severity == BLOCK

    def __str__(self) -> str:
        return f"{self.severity}: {self.reason} -> {self.excerpt!r}"


_COMPILED = tuple((re.compile(p), reason, sev) for p, reason, sev in _RULES)


def scan_command(command: str) -> list[Finding]:
    """Return every rule hit in *command*, most severe first."""
    findings: list[Finding] = []
    for pattern, reason, severity in _COMPILED:
        match = pattern.search(command)
        if match:
            findings.append(Finding(severity, reason, match.group(0).strip()))
    findings.sort(key=lambda f: 0 if f.blocking else 1)
    return findings


def scan_text(text: str) -> list[Finding]:
    """Scan a script body, one logical line at a time."""
    findings: list[Finding] = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        for finding in scan_command(stripped):
            findings.append(Finding(finding.severity, finding.reason, f"{stripped[:80]}"))
    return findings


# --- policy ---------------------------------------------------------------

#: Binaries DonePM is willing to launch without a per-binary approval.
DEFAULT_ALLOWED_BINARIES = frozenset(
    {
        "node",
        "donejs",
        "npm",
        "pnpm",
        "yarn",
        "python",
        "python3",
        "sh",
        "make",
        "go",
        "git",
        "tsc",
        "jest",
        "eslint",
        "prettier",
        "vitest",
        "webpack",
        "rollup",
        "esbuild",
        "ts-node",
    }
)

#: npm lifecycle hooks that execute code from a package manifest.
LIFECYCLE_SCRIPTS = (
    "preinstall",
    "install",
    "postinstall",
    "preuninstall",
    "uninstall",
    "postuninstall",
    "prepublish",
    "prepublishOnly",
    "prepare",
    "postprepare",
    "prepack",
    "postpack",
    "dependencies",
)


def _env_flag(name: str) -> bool:
    return os.environ.get(name, "").strip().lower() in {"1", "true", "yes", "on"}


@dataclass
class Policy:
    """Decides what DonePM may execute."""

    allow_scripts: bool = False
    allow_binaries: set[str] = field(default_factory=set)
    assume_yes: bool = False
    #: when true no interactive prompt is possible (CI, pipes)
    non_interactive: bool = field(default_factory=lambda: not sys.stdin.isatty())

    def __post_init__(self) -> None:
        if _env_flag("DONEPM_ALLOW_SCRIPTS"):
            self.allow_scripts = True
        extra = os.environ.get("DONEPM_ALLOW_BINARIES", "")
        for name in extra.replace(",", " ").split():
            self.allow_binaries.add(name)

    # -- static checks ----------------------------------------------------

    def blocked(self, command: str) -> list[Finding]:
        """Findings that forbid *command* outright."""
        return [f for f in scan_command(command) if f.blocking]

    def warnings(self, command: str) -> list[Finding]:
        return [f for f in scan_command(command) if not f.blocking]

    def assert_allowed(self, command: str, *, where: str = "command") -> None:
        """Raise :class:`BlockedCommand` when *command* hits a block rule."""
        hits = self.blocked(command)
        if hits:
            reasons = "; ".join(f.reason for f in hits)
            raise BlockedCommand(f"refused to run {where}: {reasons}\n  command: {command}")

    def binary_allowed(self, binary: str) -> bool:
        name = os.path.basename(binary)
        if name in self.allow_binaries:
            return True
        return name in DEFAULT_ALLOWED_BINARIES

    # -- authorisation ----------------------------------------------------

    def authorize(
        self,
        action: str,
        command: str,
        *,
        binary: str | None = None,
        script: bool = True,
        prompt: bool = True,
    ) -> bool:
        """Return True when *command* may run.

        Blocking rules always win.  Otherwise the user has to approve:
        up front through ``--allow-scripts``/``--allow-binaries``, or
        interactively at the prompt.

        *script* is False for commands the user asked for explicitly (their
        own project scripts); *binary* names the executable a third party
        package wants to run.
        """
        self.assert_allowed(command, where=action)

        if binary is not None and not self.binary_allowed(binary):
            if not self._ask(f"{action}: run binary {binary!r}?", command, prompt=prompt):
                raise PermissionDenied(
                    f"permission denied: running {binary!r} needs approval "
                    f"(--allow-binary {binary} or an interactive yes)"
                )
            return True

        if not script:
            return True

        if self.allow_scripts:
            return True

        if not self._ask(f"{action}: run this command?", command, prompt=prompt):
            raise PermissionDenied(
                f"permission denied: {action} was not approved "
                f"(use --allow-scripts or approve interactively)"
            )
        return True

    def _ask(self, question: str, command: str, *, prompt: bool) -> bool:
        if not prompt or self.assume_yes:
            return False
        if self.non_interactive:
            return False
        sys.stderr.write(f"\n{question}\n  {command}\n")
        try:
            answer = input("allow? [y/N] ").strip().lower()
        except (EOFError, KeyboardInterrupt):
            sys.stderr.write("\n")
            return False
        return answer in {"y", "yes"}

    def describe(self, command: str) -> str:
        """Human readable summary of the findings for *command*."""
        findings = scan_command(command)
        if not findings:
            return "no issues detected"
        return "\n".join(f"  - {f.severity}: {f.reason} ({f.excerpt})" for f in findings)


def command_binary(command: str) -> str | None:
    """Best effort extraction of the program a shell command starts with."""
    tokens = command.strip().split()
    if not tokens:
        return None
    for token in tokens:
        if token in {"sudo", "env", "command", "exec", "nohup", "time"}:
            continue
        if "=" in token and not token.startswith("-"):
            continue
        return token
    return None


def summarize(findings: Iterable[Finding]) -> str:
    findings = list(findings)
    if not findings:
        return "no issues detected"
    return "\n".join(f"  - {f.severity}: {f.reason} ({f.excerpt})" for f in findings)


def join_findings(groups: Sequence[Iterable[Finding]]) -> list[Finding]:
    out: list[Finding] = []
    for group in groups:
        out.extend(group)
    return out
