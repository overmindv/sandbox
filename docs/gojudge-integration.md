# Go-Judge Integration

Sandbox uses the go-judge REST API as the first real executor integration.

The worker calls `POST /run` to execute jobs and `GET /version` for health. In the
pilot, both requests go through the runner's private mTLS proxy.

## Runner TLS

Set `SANDBOX_GOJUDGE_BASE_URL` to the private HTTPS address. Mount three PEM files
read-only in the worker and set their paths:

- `SANDBOX_GOJUDGE_TLS_CA_FILE`: CA bundle for the runner server.
- `SANDBOX_GOJUDGE_TLS_CERT_FILE`: worker certificate and any intermediates.
- `SANDBOX_GOJUDGE_TLS_KEY_FILE`: worker private key, readable only by the worker.

Set all three paths or none. With these files configured, the worker verifies
the runner name using the configured CA bundle and presents its client certificate.
It refuses plain HTTP and does not follow redirects. Files are loaded at startup;
restart the worker after replacing them. Never mount the worker key into go-judge
or pass it to a user program. Local HTTP still works with no TLS files.

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
