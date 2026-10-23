# donejs

A Node.js-compatible JavaScript runtime written in Go.

`donejs` speaks the Node.js API - `require()`, `fs`, `path`, `Buffer`, `http`,
`stream`, `crypto`, `zlib`, `events`, ... - and runs JavaScript on a from-scratch
bytecode engine (`internal/jit/`) instead of V8 + libuv + JavaScript.

The from-scratch bytecode compiler + interpreter it executes lives in the tree
(`internal/jit/`) - see [Bytecode engine](#bytecode-engine-internaljit).

```
donejs script.js arg1 arg2
donejs -e "console.log(process.argv)"
```

## Build

```sh
go build -o donejs .
go test ./...            # unit tests
./donejs --selftest      # 38 runtime API checks
```

## CLI

| Option | Meaning |
| --- | --- |
| `-e`, `--eval <code>` | evaluate JavaScript instead of running a file |
| `--allow-exec` | allow `child_process` to spawn executables (denied by default) |
| `--selftest` | run the built-in API self test |
| `-v`, `--version` | print the version |
| `-h`, `--help` | usage |

## Module coverage

| Module | State |
| --- | --- |
| `assert` | complete |
| `buffer` | complete |
| `child_process` | implemented, **execution denied unless `--allow-exec`** |
| `console` | complete |
| `crypto` | hashes, HMAC, PBKDF2, random bytes, TLS helpers |
| `events` | `EventEmitter` with `on`/`once`/`off`/`emit`/`prependListener` |
| `fs` | sync + promise APIs, streams, `ENOENT` with `code` |
| `fs/promises` | complete |
| `http` | client + server, keep-alive, chunked bodies |
| `https` | client + server over TLS |
| `net` | TCP sockets |
| `os` | platform, homedir, tmpdir, cpus, memory |
| `path` | `posix` and `win32` flavours, implemented independently of the host OS |
| `process` | argv, env, cwd, exit codes, `nextTick`, signals |
| `querystring` | parse/stringify |
| `stream` | `Readable`, `Writable`, `Transform`, piping, async iteration |
| `string_decoder` | implemented |
| `timers` | `setTimeout`, `setInterval`, `setImmediate` |
| `url` | `URL`, `URLSearchParams` |
| `util` | `inspect`, `format`, `promisify`, `types` |
| `zlib` | gzip/deflate/brotli round trips |

Globals: `console`, `Buffer`, `URL`, `URLSearchParams`, `fetch`, `structuredClone`,
`queueMicrotask`, `setTimeout`/`setInterval`/`setImmediate`, `TextEncoder`,
`TextDecoder`, `Intl`, `require`.

## Architecture

```
main.go                 CLI entry point
internal/vm/
  runtime.go            Runtime, global bootstrap, require()
  loop.go               event loop: timers, jobs, handles, tick queues
  modules.go            CommonJS loader, module cache, native dispatch
  builtin/*.js          JavaScript layer of the built-in modules
  *.go                  native module implementations
internal/jit/           the bytecode engine (self-contained JS engine)
  lexer.go  parser.go  ast.go   front end
  compiler.go          AST -> stack bytecode
  bytecode.go          instruction set + FuncProto
  value.go  object.go  tagged Value, objects, arrays
  vm.go                interpreter loop
```

* **Native table dispatch.** Every native module exposes `__mod.sync(name, args)`
  and `__mod.async(name, args)`; the JavaScript layer in `internal/vm/builtin/`
  turns that into the friendly Node API.
* **One event loop.** `internal/vm/loop.go` owns timers, the job queue used by
  native callbacks, and reference counts for open handles and in-flight
  requests. The process exits when nothing is left to wait for.
* **Node tick ordering.** `process.nextTick` callbacks are flushed at the end of
  the entry module's synchronous body, so they run *before* promise
  microtasks - exactly like Node. `queueMicrotask` uses a separate queue and
  therefore runs after promise jobs.

## Bytecode engine (`internal/jit/`)

`internal/jit/` is a self-contained JavaScript engine: lexer, recursive-descent
parser, AST, stack bytecode compiler, and a bytecode interpreter. It reuses
nothing outside the module so the acceleration techniques can be tried on their own.
The runtime executes scripts through it; it is the only engine left.

Design:

* **Tagged `Value`** - a 32-byte struct (`tag`, `float64`, `ref`). Numbers live
  inline so the arithmetic path allocates nothing.
* **Stack bytecode** - one operand stack per frame; locals are resolved to slots
  at compile time, so the interpreter never does a name lookup.
* **Frame pool** - activations are recycled per function prototype, so the hot
  call path allocates nothing and does no map lookup.
* **Dense switch dispatch** - the instruction set is a dense `uint8`, so the Go
  compiler turns the interpreter loop into a jump table.
* **Recursive self-reference** - a named function's own name resolves to the
  function value itself, which is what makes recursion work without a global
  lookup.

Benchmarks (`go test -run '^$' -bench . ./internal/jit/`, against Node 26.11.0,
on an i5-7400):

| Benchmark | `internal/jit` | V8 (Node) |
| --- | --- | --- |
| `fib(25)` | **25.8 ms** | 1.03 ms |
| `for (i < 1e6) s += i` | **97 ms** | 1.55 ms |

The engine runs these benchmarks 20x slower than V8; that gap is real JIT
(Turbofan), which this engine deliberately does not do.

Known limits: the engine covers the syntax the runtime's builtins and selftest
rely on - classes, private fields, generators and async generators, `yield*`
delegation, `for await`, spread, template literals, `try`/`catch`, BigInt,
optional chaining, and ES modules - and the host bridge is a tagged value
model, not the full ES object model. Output goes through `print`/`console.log`.

## Security

`child_process` is refused by default:

```
Error: child_process is disabled (pass --allow-exec to enable)
  code: 'ERR_EXEC_DENIED'
```

Set `DONEJS_ALLOW_EXEC=1` to allow it for a whole session. `DONEJS_FORCE_EXEC=1`
additionally bypasses the per-command policy.

## Differences from Node.js

* `process.nextTick` is flushed at the end of the entry module rather than at
  every event-loop turn, so a `nextTick` scheduled inside a timer callback
  runs after that callback's promise jobs.
* Native addons (`.node` files) are not supported.
* `Intl` is implemented from the CLDR JSON data (not ICU): full locale
  resolution, plural rules, compact notation, currency/unit patterns,
  day-period rules and `Intl.supportedValuesOf` catalogs, plus non-Gregorian
  calendars (`islamic` and its variants, `persian`, `indian`, `roc`, `coptic`,
  `ethiopic`, `ethioaa`, `buddhist`, `japanese`). Known gaps:
  * the `hebrew`, `chinese`, `dangi` and `iso8601` calendars are accepted but
    fall back to Gregorian field values;
  * `timeZoneName` renders a `GMT±hh:mm` offset instead of the zone's
    exemplar-city or generic name;
  * `Intl.DisplayNames` and `Intl.Segmenter` are not implemented.
* `util.inspect` handles objects, arrays, strings, numbers, errors and dates,
  but not `Promise` state, `Map`/`Set` entries, `RegExp`, `Symbol`, typed
  arrays or functions - those render as bare `{}` / `[]`.
* The event loop implements timers, immediates, socket and HTTP activity -
  not the full libuv feature set (no `SIGWINCH`/`SIGIO` style poll phases).

## Testing

* `internal/vm/selftest.go` - 38 checks over the public API surface, executed
  by `donejs --selftest` and by `go test`.
* `internal/vm/selftest_test.go` - the `go test` wrapper.
