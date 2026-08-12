# Language Adapters

Language adapters are responsible for turning a validated `RunRequest` into an `ExecutionCommand`.

The registry loads `configs/languages.yaml`, resolves `language.id` and `language.version`, checks whether the language is enabled, verifies supported test modes, and delegates command building to the configured adapter.

## Current State

Python is the first enabled adapter. JavaScript, Go, and C++ are present in the manifest but disabled until their adapters are implemented.

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
