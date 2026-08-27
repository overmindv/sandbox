# Test Source Provider

`TestSourceProvider` resolves test material before the language adapter builds an
execution command. The worker keeps Kafka transport and executor details outside
this layer.

## Interface

`TestSourceProvider.Resolve(ctx, req)` returns a new `RunRequest` with executable
test files or a typed `SourceError`.

Provider errors use stable `Reason` values:

- `unsupported_test_source`
- `object_storage_tests_disabled`
- `invalid_test_suite_ref`
- `test_suite_fetch_failed`
- `test_suite_too_large`
- `test_suite_checksum_mismatch`
- `test_suite_decode_failed`
- `invalid_test_suite`
- `test_suite_id_mismatch`
- `test_suite_version_mismatch`
- `test_suite_mode_mismatch`

Retryable errors are infrastructure failures, such as object storage fetch
errors. Invalid references, malformed suites, size violations, and checksum
mismatches are terminal `invalid_request` results.

## Providers

`InlineProvider` passes through requests with `tests.source=inline`. It is kept
for local development, public tests, and backward-compatible MVP flows.

`ObjectStorageProvider` loads hidden tests by reference:

`<key_prefix>/test-suites/<suite_id>/<version-or-latest>.json`

Kafka requests for `tests.source=object_storage` must contain:

- `suite_id`
- `checksum_sha256`
- optional `version`

The resolved request is converted to `tests.source=inline` internally after the
suite is fetched, decoded, and validated. Object storage reference fields are
cleared before the request reaches the language adapter.

## Suite Manifest

The suite manifest uses `schema_version=sandbox.testsuite.v1`:

- `suite_id`
- optional `version`
- `mode`
- optional `entrypoint`
- `files`
- optional `metadata`

Each file is a normal `SourceFile` with a relative path, base64 content, and an
optional per-file `checksum_sha256`. After resolution, the common security guard
validates the resulting files again.

## Integrity

The Kafka request checksum covers the raw suite manifest bytes in object storage.
This prevents stale, swapped, or corrupted suite manifests from being trusted.

Per-file checksums cover decoded file bytes inside the manifest. A mismatch makes
the suite invalid and does not retry.

## Size Limits

`tests.object_storage.max_bytes` is enforced twice:

- against the raw JSON manifest payload fetched from object storage;
- against the total decoded size of all test files in the manifest.

This keeps both Kafka and worker memory pressure predictable.

## Cache Policy

`tests.object_storage.cache_ttl_ms` controls the in-process suite cache.

- `<= 0`: cache disabled.
- `> 0`: cache enabled for the configured TTL.

Cached entries are keyed by object key and still verify the requested manifest
checksum before use. Expired entries are evicted lazily on read.
