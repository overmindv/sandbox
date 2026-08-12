# Language Adapters

Language adapters are responsible for turning a validated `RunRequest` into an `ExecutionCommand`.

The registry loads `configs/languages.yaml`, resolves `language.id` and `language.version`, checks whether the language is enabled, verifies supported test modes, and delegates command building to the configured adapter.

## Current State

Python is the first enabled adapter. JavaScript, Go, and C++ are present in the manifest but disabled until their adapters are implemented.

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
