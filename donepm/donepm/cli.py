"""DonePM command line interface.

Command names and flags follow npm so existing habits and CI scripts keep
working.  The difference is what happens before anything runs: every command
is scanned by :mod:`donepm.security` and nothing executes without approval.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from typing import Any, Sequence

from . import __version__
from .errors import DonePMError, PackageError, PermissionDenied, SecurityError, UsageError
from .installer import Installer
from .manifest import Manifest
from .registry import DEFAULT_REGISTRY, Registry, default_cache_dir
from .security import Policy, scan_command
from .tarball import create_tarball

#: options accepted both before and after the subcommand
_SHARED = (
    (("--allow-scripts",), {"action": "store_true", "help": "approve lifecycle scripts up front"}),
    (("--allow-binary",), {"action": "append", "metavar": "NAME", "help": "allow a binary to be executed (repeatable)"}),
    (("-y", "--yes"), {"action": "store_true", "help": "assume yes for non-destructive prompts"}),
    (("--dry-run",), {"action": "store_true", "help": "resolve and report without touching disk"}),
    (("--offline",), {"action": "store_true", "help": "use only cached metadata"}),
    (("--registry",), {"metavar": "URL", "help": "registry base URL"}),
    (("--prefix",), {"metavar": "DIR", "help": "project directory (default: cwd)"}),
    (("--json",), {"action": "store_true", "help": "machine readable output"}),
    (("-v", "--verbose"), {"action": "store_true", "help": "explain what is happening"}),
)


def _shared_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(add_help=False)
    for flags, kwargs in _SHARED:
        parser.add_argument(*flags, default=argparse.SUPPRESS, **kwargs)
    return parser


#: every global flag, used to keep options out of a script's arguments
_GLOBAL_FLAGS = {flag for flags, _ in _SHARED for flag in flags}
#: global flags that consume the next token
_GLOBAL_VALUE_FLAGS = {
    flag
    for flags, kwargs in _SHARED
    if kwargs.get("action") != "store_true"
    for flag in flags
}
#: command specific flags that are still consumed by DonePM
_COMMAND_FLAGS = {"--list", "-h", "--help"}

#: commands whose trailing tokens belong to the script they run
_SCRIPT_COMMANDS = ("run", "run-script", "exec", "x")


def build_parser() -> argparse.ArgumentParser:
    shared = _shared_parser()
    parser = argparse.ArgumentParser(
        prog="donepm",
        description="DonePM - fast, security-first package manager",
        parents=[shared],
    )
    parser.add_argument("--version", action="version", version=f"donepm {__version__}")
    sub = parser.add_subparsers(dest="command", metavar="<command>")

    def add(name: str, help_: str, *aliases: str) -> argparse.ArgumentParser:
        return sub.add_parser(name, aliases=list(aliases), help=help_, parents=[shared])

    p = add("install", "install dependencies (or add packages)", "i", "add")
    p.add_argument("packages", nargs="*", metavar="package")
    p.add_argument("--save-dev", "-D", action="store_true", help="record in devDependencies")
    p.add_argument("--save-prod", "-P", action="store_true", help="record in dependencies")
    p.add_argument("--no-save", action="store_true", help="do not edit package.json")
    p.add_argument("--ignore-scripts", action="store_true", help="never run lifecycle scripts")
    p.set_defaults(func=cmd_install)

    p = add("uninstall", "remove packages", "remove", "rm", "un")
    p.add_argument("packages", nargs="+", metavar="package")
    p.add_argument("--no-save", action="store_true", help="do not edit package.json")
    p.set_defaults(func=cmd_uninstall)

    p = add("run", "run a script from package.json", "run-script")
    p.add_argument("script", nargs="?", metavar="script")
    p.add_argument("args", nargs="*", metavar="--")
    p.add_argument("--list", action="store_true", help="list available scripts")
    p.set_defaults(func=cmd_run)

    p = add("exec", "run a binary from node_modules/.bin", "x")
    p.add_argument("binary", nargs="?", metavar="binary")
    p.add_argument("args", nargs="*", metavar="--")
    p.set_defaults(func=cmd_exec)

    p = add("init", "create a package.json", "create")
    p.add_argument("--name", metavar="NAME")
    p.add_argument("--description", metavar="TEXT")
    p.set_defaults(func=cmd_init)

    p = add("ls", "list installed packages", "list", "ll")
    p.add_argument("--depth", type=int, default=0)
    p.set_defaults(func=cmd_ls)

    p = add("audit", "check installed packages against the advisory database")
    p.set_defaults(func=cmd_audit)

    p = add("publish", "pack and upload the current package")
    p.add_argument("--tag", default="latest")
    p.add_argument("--token", metavar="TOKEN")
    p.add_argument("--access", choices=["public", "restricted"])
    p.set_defaults(func=cmd_publish)

    p = add("explain", "show why a package is installed", "why")
    p.add_argument("package", metavar="package")
    p.set_defaults(func=cmd_explain)

    return parser


# --- shared helpers -------------------------------------------------------


def _opt(args: argparse.Namespace, name: str, default: Any = None) -> Any:
    return getattr(args, name, default)


def _root(args: argparse.Namespace) -> str:
    prefix = _opt(args, "prefix")
    return os.path.abspath(prefix) if prefix else os.getcwd()


def _policy(args: argparse.Namespace) -> Policy:
    return Policy(
        allow_scripts=bool(_opt(args, "allow_scripts", False)),
        allow_binaries=set(_opt(args, "allow_binary", []) or []),
        assume_yes=bool(_opt(args, "yes", False)),
    )


def _registry(args: argparse.Namespace) -> Registry:
    return Registry(
        _opt(args, "registry") or DEFAULT_REGISTRY,
        cache_dir=default_cache_dir(),
        offline=bool(_opt(args, "offline", False)),
    )


def _run_script(cwd: str, command: str) -> int:
    """Execute an approved lifecycle script."""
    try:
        return subprocess.call(command, cwd=cwd, shell=True)
    except OSError as exc:
        print(f"donepm: failed to run {command!r}: {exc}", file=sys.stderr)
        return 1


def _installer(args: argparse.Namespace, *, dry_run: bool | None = None) -> Installer:
    return Installer(
        _root(args),
        registry=_registry(args),
        policy=_policy(args),
        dry_run=bool(_opt(args, "dry_run", False)) if dry_run is None else dry_run,
        runner=_run_script,
        reporter=lambda msg: print(msg, file=sys.stderr),
    )


def _emit(args: argparse.Namespace, payload: Any, human: str) -> None:
    if _opt(args, "json", False):
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        print(human)


# --- commands -------------------------------------------------------------


def cmd_install(args: argparse.Namespace) -> int:
    root = _root(args)
    installer = _installer(args)
    if not args.packages and not os.path.exists(installer.manifest_path):
        raise UsageError("no package.json found - run `donepm init` first")

    dev = bool(args.save_dev)
    # Lifecycle scripts are attempted only when asked for; the policy then
    # approves them up front, asks interactively, or refuses.
    run_scripts = not args.ignore_scripts

    result = installer.install(
        args.packages,
        save=not args.no_save,
        include_dev=not args.save_prod,
        save_dev=dev,
        run_scripts=run_scripts,
    )

    payload = {
        "added": result.added,
        "removed": result.removed,
        "skippedScripts": result.skipped_scripts,
        "blockedScripts": result.blocked_scripts,
        "failedScripts": result.failed_scripts,
        "dryRun": result.dry_run,
    }
    _emit(args, payload, result.summary())
    if result.skipped_scripts and not _opt(args, "json", False):
        print(
            f"\n{len(result.skipped_scripts)} lifecycle script(s) were not run "
            "(DonePM blocks them by default).",
            file=sys.stderr,
        )
        print("Re-run with --allow-scripts to approve them.", file=sys.stderr)
    if result.failed_scripts:
        print(
            f"\n{len(result.failed_scripts)} lifecycle script(s) failed.",
            file=sys.stderr,
        )
        return 1
    _ = root
    return 0


def cmd_uninstall(args: argparse.Namespace) -> int:
    installer = _installer(args)
    result = installer.uninstall(args.packages, save=not args.no_save)
    payload = {"removed": result.removed, "dryRun": result.dry_run}
    _emit(args, payload, result.summary())
    return 0


def cmd_run(args: argparse.Namespace) -> int:
    root = _root(args)
    manifest = Manifest.load(os.path.join(root, "package.json"))

    if args.list or not args.script:
        if not manifest.scripts:
            print("no scripts defined", file=sys.stderr)
            return 1
        for name in sorted(manifest.scripts):
            print(f"  {name}: {manifest.scripts[name]}")
        return 0

    command = manifest.scripts.get(args.script)
    if command is None:
        raise UsageError(f"unknown script {args.script!r} (try `donepm run --list`)")

    extra = [a for a in (args.args or []) if a != "--"]
    full = " ".join([command, *extra]) if extra else command

    policy = _policy(args)
    policy.assert_allowed(full, where=f"script {args.script}")
    # The script belongs to this project and the user named it explicitly,
    # so the block rules are enough - the binary allowlist only guards code
    # that arrived with a dependency.
    policy.authorize(f"script {args.script}", full, script=False)

    return _spawn(full, root)


def cmd_exec(args: argparse.Namespace) -> int:
    root = _root(args)
    if not args.binary:
        raise UsageError("donepm exec needs a binary name")
    binary = args.binary
    candidate = os.path.join(root, "node_modules", ".bin", binary)
    if not os.path.exists(candidate):
        raise UsageError(f"{binary!r} is not installed in node_modules/.bin")

    extra = [a for a in (args.args or []) if a != "--"]
    policy = _policy(args)
    policy.assert_allowed(binary, where=f"exec {binary}")
    # A downloaded binary has to be a known one before it may run.
    policy.authorize(f"exec {binary}", " ".join([binary, *extra]), binary=binary, script=False)
    return _spawn([candidate, *extra], root)


def cmd_init(args: argparse.Namespace) -> int:
    root = _root(args)
    path = os.path.join(root, "package.json")
    if os.path.exists(path) and not _opt(args, "yes", False):
        raise UsageError("package.json already exists (pass -y to overwrite)")
    name = args.name or os.path.basename(root.rstrip(os.sep)) or "package"
    manifest = Manifest.create(name, "1.0.0", description=args.description or "")
    manifest.scripts = {"test": "echo \"Error: no test specified\" && exit 1"}
    manifest.raw = {
        "name": name,
        "version": "1.0.0",
        "description": manifest.description,
        "main": "index.js",
        "scripts": manifest.scripts,
        "keywords": [],
        "author": "",
        "license": "ISC",
    }
    if _opt(args, "dry_run", False):
        print(json.dumps(manifest.to_dict(), indent=2, sort_keys=True))
        return 0
    manifest.save(path)
    print(f"wrote {path}")
    return 0


def cmd_ls(args: argparse.Namespace) -> int:
    installer = _installer(args)
    installed = installer.list_installed()
    if _opt(args, "json", False):
        print(json.dumps({"dependencies": installed}, indent=2))
    elif installed:
        for entry in installed:
            print(f"  {entry}")
    else:
        print("(empty)")
    return 0


def cmd_explain(args: argparse.Namespace) -> int:
    installer = _installer(args)
    tree = installer.tree()
    if args.package not in tree:
        raise UsageError(f"{args.package!r} is not installed")
    dependents = [name for name, deps in tree.items() if args.package in deps]
    print(f"{args.package} is installed")
    for name in sorted(dependents):
        print(f"  required by {name}")
    if not dependents:
        print("  required by the root package")
    return 0


def cmd_audit(args: argparse.Namespace) -> int:
    installer = _installer(args)
    installed = installer.list_installed()
    if not installed:
        print("no packages to audit")
        return 0
    payload: dict[str, list[str]] = {}
    for entry in installed:
        name, _, version = entry.rpartition("@")
        if name:
            payload.setdefault(name, []).append(version)

    registry = _registry(args)
    if registry.offline:
        raise UsageError("audit needs network access (offline mode is on)")
    try:
        resp = registry.http.post_json(
            f"{registry.url}/-/npm/v1/security/advisories/bulk", payload
        )
    except DonePMError as exc:
        raise UsageError(f"could not reach the advisory database: {exc}") from exc
    if not resp.ok:
        raise UsageError(f"advisory lookup failed: {resp.status}")
    advisories = resp.json() or {}

    count = 0
    for name, findings in advisories.items():
        for finding in findings:
            count += 1
            severity = finding.get("severity", "unknown")
            title = finding.get("title", "")
            url = finding.get("url", "")
            vulnerable = finding.get("vulnerable_versions", "")
            print(f"{severity}: {name} {vulnerable} - {title}")
            if url:
                print(f"  {url}")
    if _opt(args, "json", False):
        print(json.dumps(advisories, indent=2, sort_keys=True))
    elif count == 0:
        print("found 0 vulnerabilities")
    else:
        print(f"found {count} vulnerabilities")
    return 1 if count else 0


def cmd_publish(args: argparse.Namespace) -> int:
    root = _root(args)
    path = os.path.join(root, "package.json")
    if not os.path.exists(path):
        raise UsageError("no package.json to publish")
    manifest = Manifest.load(path)
    if not manifest.name or not manifest.version:
        raise PackageError("package.json needs a name and a version")
    if manifest.private:
        raise UsageError(f"{manifest.name} is marked private")

    token = args.token or _npm_token(root)
    if not token:
        raise UsageError("a registry token is required (--token, DONEPM_TOKEN or ~/.npmrc)")

    data = manifest.to_dict()
    if args.access:
        data["publishConfig"] = {"access": args.access}
    tarball = create_tarball(root, data)

    if _opt(args, "dry_run", False):
        print(f"would publish {manifest.name}@{manifest.version} ({len(tarball)} bytes)")
        return 0

    registry = _registry(args)
    result = registry.publish(tarball, data, token, tag=args.tag)
    print(f"published {manifest.name}@{manifest.version}")
    _ = result
    return 0


# --- plumbing -------------------------------------------------------------


def _spawn(command: str | list[str], cwd: str) -> int:
    shell = isinstance(command, str)
    try:
        completed = subprocess.run(command, cwd=cwd, shell=shell, check=False)
    except OSError as exc:
        raise DonePMError(f"failed to start command: {exc}") from exc
    return completed.returncode


def _npm_token(root: str) -> str:
    env = os.environ.get("DONEPM_TOKEN") or os.environ.get("NPM_TOKEN")
    if env:
        return env.strip()
    for candidate in (os.path.join(root, ".npmrc"), os.path.expanduser("~/.npmrc")):
        if not os.path.exists(candidate):
            continue
        try:
            with open(candidate, "r", encoding="utf-8") as handle:
                for line in handle:
                    line = line.strip()
                    if line.startswith("//") and "_authToken=" in line:
                        return line.split("_authToken=", 1)[1].strip()
        except OSError:
            continue
    return ""


def _reorder_script_args(argv: list[str], command: str) -> list[str]:
    """Lift global options out of a script's argument list.

    ``donepm exec tsc --noEmit --prefix build`` must hand ``--noEmit`` to
    ``tsc`` while still honouring ``--prefix``.  Recognised options are moved
    in front of the script name; everything else keeps its order.
    """
    index = argv.index(command)
    head = argv[: index + 1]
    options: list[str] = []
    script: list[str] = []
    index += 1
    while index < len(argv):
        token = argv[index]
        if token == "--":
            script.extend(argv[index:])
            break
        name = token.split("=", 1)[0]
        if name in _GLOBAL_FLAGS or name in _COMMAND_FLAGS:
            options.append(token)
            if name in _GLOBAL_VALUE_FLAGS and "=" not in token and index + 1 < len(argv):
                options.append(argv[index + 1])
                index += 2
                continue
        else:
            script.append(token)
        index += 1

    head.extend(options)
    if script:
        if script[0] == "--":
            head.extend(script)
        else:
            head.append(script[0])
            if len(script) > 1:
                head.append("--")
                head.extend(script[1:])
    return head


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    raw = list(sys.argv[1:] if argv is None else argv)
    for token in raw:
        if token in _SCRIPT_COMMANDS:
            raw = _reorder_script_args(raw, token)
            break
    args, extras = parser.parse_known_args(raw)
    if extras:
        args.args = list(getattr(args, "args", []) or []) + extras
    if not getattr(args, "command", None):
        parser.print_help()
        return 2
    try:
        return args.func(args)
    except PermissionDenied as exc:
        print(f"donepm: {exc}", file=sys.stderr)
        return 3
    except SecurityError as exc:
        print(f"donepm: {exc}", file=sys.stderr)
        return 3
    except DonePMError as exc:
        print(f"donepm: {exc}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("donepm: interrupted", file=sys.stderr)
        return 130


def scan(command: str) -> list[str]:
    """Expose the scanner for tests and `donepm` internals."""
    return [str(f) for f in scan_command(command)]
