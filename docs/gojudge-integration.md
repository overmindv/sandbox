# Go-Judge Integration

Sandbox uses the go-judge REST API as the first real executor integration.

The production path is HTTP `POST /run`. go-judge also has a gRPC endpoint, but it is disabled by default on the go-judge side, so this repository keeps transport-neutral request/response models and implements the default REST transport first.

## Request Mapping

`execution.ExecutionCommand` maps to one go-judge `cmd` item:

- `Argv -> cmd.args`
- `Environment -> cmd.env` as sorted `KEY=value` strings
- workspace files -> `cmd.copyIn[path].content`
- stdin -> empty memory file descriptor
- stdout/stderr -> go-judge collectors named `stdout` and `stderr`
- `RuntimeLimits.CPUms -> cpuLimit` in nanoseconds
- `RuntimeLimits.WallMS -> clockLimit` in nanoseconds
- `RuntimeLimits.MemoryBytes -> memoryLimit`
- `RuntimeLimits.PIDs -> procLimit`
- `CopyOut -> copyOut` as optional paths with `?`
- `RuntimeLimits.WorkspaceBytes -> copyOutMax`

No shell command is built. `cmd.args` is sent as an array.

## Go Build and Run

Set `SANDBOX_GOJUDGE_COMPILER_URL` to the internal Go compiler endpoint.
Compose and the Kubernetes manifests configure a separate go-judge for it.
The worker compiles once per execution request, then runs the binary with the
original input, CPU, wall, memory, process and output limits for each case.
Python and the legacy `go test` path do not use the compiler endpoint.

The compiler is offline and uses a per-sandbox cache in tmpfs. Its file limit
is 32 MiB; the worker accepts a compiled binary of up to 8 MiB. The runner
receives the binary through go-judge's file API. The worker deletes both copies
after use. The server expires files after two minutes if cleanup is interrupted.
Each file ID is used only on the connection that produced it. Connect the
worker directly to the go-judge service, without a per-request HTTP proxy.
Use the same architecture for the compiler and runner.

The file API has no client authentication. Compose does not publish it by
default. To run host-side integration tests, opt in to loopback ports:

```sh
docker compose -f docker-compose.yml -f docker-compose.gojudge-host.yml up -d gojudge gojudge-compiler
```

The runner listens on port 5050 and the compiler on 5051 on the local host.
Do not expose either port on a shared network.

## Response Mapping

go-judge returns one result item for the single command. The executor collects:

- `files.stdout -> ExecutionResult.Stdout`
- `files.stderr -> ExecutionResult.Stderr`
- configured copy-out files -> `RawExecutionResult.ReportFiles`
- `time/runTime` nanoseconds -> milliseconds
- `memory` bytes -> `MemoryPeakBytes`
- `exitStatus -> ExitCode`

For Python pytest commands, the raw result is passed into the Python pytest
parser. For Go commands with `tests.mode=go-test`, the raw `go test -json`
stream is passed into the Go parser. Normal test failures become `failed`, not
`internal_error`.

## Status Mapping

| go-judge status | Sandbox status |
| --- | --- |
| `Accepted` | `passed`, or language parser result |
| `Time Limit Exceeded` | `tle` |
| `Memory Limit Exceeded` | `mle` |
| `Output Limit Exceeded` | `ole` |
| `File Error` | `internal_error` with code `gojudge_file_error` |
| `Nonzero Exit Status` | language parser result, otherwise `runtime_error` |
| `Signalled` | `runtime_error` with code `signalled` |
| `Internal Error` | `internal_error` with code `gojudge_internal_error` |

HTTP/network failures are executor infrastructure errors. The client retries only retryable infrastructure failures: network/timeout errors, HTTP `429`, and HTTP `5xx`. HTTP `4xx` request errors are not retried.

## Live Integration Test

Run a local go-judge first, for example on `http://localhost:5050`, then execute:

```powershell
$env:SANDBOX_GOJUDGE_INTEGRATION = "1"
$env:SANDBOX_GOJUDGE_BASE_URL = "http://localhost:5050"
$env:GOCACHE = Join-Path (Get-Location) ".gocache"
go test ./internal/execution/gojudge -run TestLiveGoJudgeRunsPythonPytest
```

The live Python test expects the go-judge runtime to have `python3` and `pytest`
available. Go E2E requests require `go1.26`; the local Docker Compose runtime
image `overmindv/sandbox-gojudge:local` installs both Python and Go.
