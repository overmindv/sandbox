# Security Model

Security Guards v1 run before a `RunRequest` reaches language adapters, scheduler, or executor.

## Request Guards

The Kafka processor rejects unsafe requests before dispatch:

- raw Kafka payload size must fit `configs/limits.yaml:global.request_max_bytes`;
- each decoded file must fit `global.file_content_max_bytes`;
- source/test file counts must fit `global.source_files_max_count` and `global.test_files_max_count`;
- total file count must fit `security.filesystem.max_files`;
- paths must be relative POSIX-style paths;
- absolute paths, Windows drive paths, backslashes, empty path segments, `.`, and `..` are rejected;
- filename segments are allowlisted to ASCII letters, digits, `.`, `_`, and `-`;
- archives are rejected by extension and magic bytes, even though archive upload is not enabled yet.

Requests rejected after JSON decode are published as `invalid_request` plus DLQ. Oversized raw payloads are rejected before decode and published to DLQ only, because IDs cannot be trusted without parsing.

## Output Guards

Executor stdout/stderr are truncated before result publication using `security.logs.max_stdout_bytes` and `security.logs.max_stderr_bytes`.

When truncation happens, `RunResult.logs.truncated` is set to `true`.

## Copy-Out Guards

Copy-out paths are treated as untrusted executor output. The security package validates copy-out path syntax and exposes a symlink rejection guard for executor adapters.

Executors must use metadata-equivalent checks such as `lstat` and reject `os.ModeSymlink` before reading copy-out files.

## Current Boundary

Security Guards v1 protect the request and result boundaries. They do not replace executor isolation. The real executor must still enforce CPU, wall time, memory, PID, filesystem, network, stdout/stderr, and workspace limits inside the sandbox runtime.

## Доверенная оценка программ (OVM-233)

По умолчанию worker разрешает только stdin/stdout execution. Эталоны и checker
остаются в tasks; exit zero означает только завершение процесса (status ok),
не accepted. Fake report и изменение созданных решением файлов не являются
каналом результата: runtime отдаёт stdout/stderr, а tasks сравнивает stdout.

Legacy pytest/go-test opt-in через SANDBOX_FEATURE_LEGACY_HARNESS=true разрешён
только в local/dev/test. Production запрещает флаг. Legacy harness не изолирован
от solution и не подходит для grading недоверенного кода.

В execution запрещены test material, harness и report copyOut. Go go.mod
маркируется build metadata, а не checker/harness. Некорректный runtime protocol
(число ответов, неизвестный статус, отсутствующий exitStatus, противоречивый
Accepted, trailing JSON) возвращает invalid_request/code=checker_error.
Tasks уже отображает этот совместимый wire status как checker_error.
POST /run не повторяется автоматически после неоднозначного сбоя.

Live smoke в отдельном gojudge: overwrite harness, fake report, exit zero,
read expected, контрольный echo для Python и Go. Команда:
SANDBOX_GOJUDGE_INTEGRATION=1 SANDBOX_GOJUDGE_BASE_URL=http://127.0.0.1:55050
go test ./internal/execution/gojudge -run TestLiveMalicious -v

Проверка не доказывает отсутствие всех sandbox escape; она подтверждает границу
доверенного оценивания для указанного execution пути.
