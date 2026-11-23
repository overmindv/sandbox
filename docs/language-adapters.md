# Language Adapters

Language adapters are responsible for turning a validated `RunRequest` into an `ExecutionCommand`.

The registry loads `configs/languages.yaml`, resolves `language.id` and `language.version`, checks whether the language is enabled, verifies supported test modes, and delegates command building to the configured adapter.

## Current State

Python and Go are enabled by default. JavaScript, C, and C++ need an explicit
manifest change and their corresponding images.

## Python Source Files

Python submissions use `language.id=python` and `tests.mode=pytest`.

Source files are passed in `code.files` as base64-encoded files with relative POSIX-style paths. The default source entrypoint is `solution.py`; callers may set `code.entrypoint`, but the file still has to be included in `code.files`.

Allowed Python workspace extensions are defined in `configs/languages.yaml` and currently include `.py`, `.txt`, and `.json`.

The adapter rejects reserved paths owned by the sandbox runtime:

- `_sandbox_pytest_plugin.py`
- `.sandbox`
- `.sandbox/*`

## Python Pytest Tests

Inline pytest tests are passed in `tests.files`, also as base64-encoded relative paths. The default pytest entrypoint is `test_solution.py`; callers may set `tests.entrypoint` when they need a different test file.

For every Python command the adapter adds a harness file named `_sandbox_pytest_plugin.py`. Pytest loads it with `-p _sandbox_pytest_plugin`, and the plugin writes a machine-readable report to `.sandbox/pytest-report.json`.

The executor must copy out `.sandbox/pytest-report.json` together with captured stdout/stderr. The Python result parser maps that report into `RunResult.summary` and per-test results.

## Python Command Safety

The Python adapter never builds a shell command string. It produces an argv array shaped like:

```text
python3 -m pytest -q --disable-warnings --tb=short -p _sandbox_pytest_plugin test_solution.py
```

The final test entrypoint is appended as a separate argv element, so executors must pass the array directly to the sandbox runtime instead of shell-interpolating it.

## Python Limits

Python limits are copied from `RunRequest.limits` into `ExecutionCommand.RuntimeLimits`.

Memory and workspace limits are converted to bytes:

- `memory_mb -> memory_bytes`
- `workspace_bytes -> workspace_bytes`

CPU, wall-clock, PID, stdout, and stderr limits remain integer millisecond/count/byte values.

## Python Result Mapping

Executor-specific raw output is normalized by the Python parser:

- executor timeout flag -> `tle`
- executor memory flag -> `mle`
- executor output flag -> `ole`
- `SyntaxError`, `IndentationError`, or `TabError` -> `compile_error`
- pytest assertion failures -> `failed`
- pytest collection/setup/runtime errors -> `runtime_error`
- zero exit code with no failures -> `passed`

## Go Source Files

Go submissions use `language.id=go`, `language.version=1.26`, and
`tests.mode=go-test`.

Source files are passed in `code.files` as base64-encoded files with relative
POSIX-style paths. The default source entrypoint is `solution.go`; callers may
set `code.entrypoint`, but `go test ./...` is package-oriented and still uses
all files materialized into the workspace.

Allowed Go workspace extensions are defined in `configs/languages.yaml` and
currently include `.go`, `.mod`, and `.sum`.

If the request does not include `go.mod`, the adapter adds a minimal harness
module:

```text
module sandbox

go 1.26
```

## Go Tests

Inline Go tests are passed in `tests.files`, usually as `solution_test.go`.
Tests should use normal Go `testing` package conventions. The adapter runs:

```text
/usr/local/go/bin/go test -json -p 1 ./...
```

The command is an argv array, not a shell string. The JSON event stream from
`go test -json` is parsed into `RunResult.summary` and per-test results.

## Go Limits

Go limits are copied from `RunRequest.limits` into
`ExecutionCommand.RuntimeLimits`. The manifest sets deterministic runtime
environment defaults:

- `CGO_ENABLED=0`
- `GOCACHE=/tmp/go-build`
- `GOMODCACHE=/tmp/go-mod`
- `GOMAXPROCS=2`
- `GOTOOLCHAIN=local`
- `GOPROXY=off`
- `PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin`

## Go Result Mapping

Executor-specific raw output is normalized by the Go parser:

- executor timeout flag -> `tle`
- executor memory flag -> `mle`
- executor output flag -> `ole`
- failed `go test -json` test event -> `failed`
- build markers such as `[build failed]`, `undefined:`, or `syntax error` -> `compile_error`
- zero exit code with no failures -> `passed`

## C17 and C++17

Use `language.id=c` or `language.id=cpp`, `language.version=17` and
`execution.mode=execution`. Include the entrypoint (`solution.c` or
`solution.cpp` by default) in `code.files`. The compiler builds all source
files for that language in one invocation. C accepts `.h` headers; C++ also
accepts `.hpp`. Build systems, additional flags, and package downloads are
not supported.

The compiler image uses GCC 14.2.0. C uses `gcc-14 -std=c17` and links
libm with `-lm`; C++ uses `g++-14 -std=c++17`. Both use
`-O2 -pipe -static -s`. Each build has
8 seconds of CPU time, 15 seconds of wall time, 512 MiB of memory, and 32 PIDs.
The worker allows up to 20 seconds for the build and upload. The executable
is bounded to 8 MiB and must fit the run's workspace limit. Use the
`compiled-default` profile for local requests.

The worker sends only the executable and the current stdin to each case.
The runtime enforces the request's CPU, wall time, memory, PID and output limits.

## Java 21

Java is disabled in `configs/languages.yaml`. To enable it locally, copy that
manifest, set `enabled: true` for `java`, and point the worker's
`config.languages_file` at the copy. Use `language.id: java`,
`language.version: "21"` and `execution.mode: execution`.
The Kafka schemas are unchanged.

Submit up to 32 UTF-8 `.java` files. The default entrypoint is `Main.java`;
`pkg/Main.java` selects `pkg.Main` and requires `package pkg;` in the source.
Use ASCII identifiers in file paths and class names. The entry class must define
`public static void main(String[] args)`. Maven, Gradle, annotation processing,
module descriptors and third-party libraries are not supported.

The compiler receives only sources and runs the pinned OpenJDK 21.0.13.1
`javac --release 21`. The worker checks a class-only JAR (at most 512 entries
and 1 MiB, with no compressed entries or manifest) before sending it to the
runner. The runner uses the matching headless JRE without `javac`. Every case
starts a fresh JVM and receives only the JAR and its own stdin.

Requests need at least 96 MiB of memory and 16 PIDs. The JVM heap uses at most
half the memory limit; go-judge enforces the total memory, PID, CPU, wall time
and output limits. The JAR must also fit the request's workspace limit.

Build and test locally without starting Kafka or changing the default runtime:

```sh
docker compose -f docker-compose.yml -f docker-compose.java.yml build gojudge gojudge-compiler
sh scripts/test-compiled-live.sh java
```

The Java overlay replaces the compiler image, so do not combine it with the
C/C++ overlay. Both keep the default Python and Go support. The Kubernetes
configuration and the production runtime images are unchanged.

## Kotlin/JVM

The disabled Kotlin profile is available as `language.id: kotlin`,
`language.version: "2.4"` and `execution.mode: execution`. To enable it
locally, copy the manifest, set `enabled: true` for the `kotlin` entry,
and point `config.languages_file` at the copy.

Submit up to 32 UTF-8 `.kt` files. The entrypoint defaults to `Main.kt`
with a top-level `fun main()`. For `pkg/Main.kt`, use `package pkg`;
the JVM launches `pkg.MainKt`. Use ASCII identifiers in paths and class
names. Scripts, build files, compiler plugins and external libraries are not
accepted.

Kotlin 2.4.21 is pinned by SHA-256 and compiles to JVM 21 bytecode. As with
Java, the worker verifies a class-only archive of up to 1 MiB and 512 entries.
The runner adds only the pinned Kotlin stdlib to the JRE image and starts a
fresh JVM for each stdin. The archive must fit the request's workspace limit.
Runtime requests need at least 96 MiB of memory and 16 PIDs.

```sh
docker compose -f docker-compose.yml -f docker-compose.kotlin.yml build gojudge gojudge-compiler
sh scripts/test-compiled-live.sh kotlin
```

The Kotlin images also support Java, Python and Go. Use only one compiler
overlay at a time. Kafka and the Kubernetes configuration are unchanged.

## TypeScript

The TypeScript profile is disabled by default. For local use, copy the manifest,
set `enabled: true` for `typescript`, and point `config.languages_file` at the
copy. Requests use `language.id: typescript`, `language.version: "7.0"` and
`execution.mode: execution`.

Submit up to 32 UTF-8 `.ts` files with relative ASCII paths (up to 100 bytes).
The default entrypoint is `solution.ts`. Use CommonJS-compatible imports, for
example `import { score } from "./helper"`. The compiler supports strict type
checking, enums and Node 24 built-ins. User declarations, `tsconfig.json`,
`package.json`, plugins and npm dependencies are not accepted.

The compiler image pins TypeScript 7.0.2 and the Node declarations by SHA-512.
The worker accepts a bundle of up to 1 MiB containing only JavaScript files
matching the submitted sources. The runner receives the checked bundle and
one case's stdin. It unpacks the files inside that case and runs Node 24
without the TypeScript compiler. Memory and PID limits must be at least
64 MiB and 16; the bundle and unpacked bytes must fit the workspace limit.

```sh
docker compose -f docker-compose.yml -f docker-compose.typescript.yml build gojudge gojudge-compiler
sh scripts/test-compiled-live.sh typescript
```

The runner also supports the existing JavaScript adapter. The TypeScript
overlay is local; it does not change Kafka or the deployment manifests.
Hidden suites use the same input provider as other languages. The worker
does not compare output with the expected answer; Tasks owns that verdict.

Build the optional compiler image and run the local check:

```sh
docker compose -f docker-compose.yml -f docker-compose.cpp.yml build gojudge gojudge-compiler
sh scripts/test-cpp-live.sh
```

The Compose overlay changes only the compiler image. To enable either language
in a local worker, mount a copy of `configs/languages.yaml` at
`/app/configs/languages.yaml` with `enabled: true` in the chosen profile. Keep
`SANDBOX_GOJUDGE_COMPILER_URL` separate from `SANDBOX_GOJUDGE_BASE_URL`.

## Adapter Contract

Each adapter implements:

```go
type LanguageAdapter interface {
    Name() string
    BuildCommand(ctx context.Context, req contracts.RunRequest, resolved ResolvedLanguage) (execution.ExecutionCommand, error)
}
```

The adapter name must match the `adapter` field in `configs/languages.yaml`.

## Unsupported Languages

Unknown, disabled, or unsupported language versions return `ErrUnsupportedLanguage`. The worker maps this to a final Kafka result with status `unsupported_language`.

## Manifest

The manifest defines:

- enabled languages and versions;
- runtime image;
- default entrypoints;
- allowed file extensions;
- supported test modes;
- command argv and environment.

The command is still executed by the executor layer. Language adapters do not run code.
