package typescript

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/tsjob"
)

const AdapterName = "typescript"

type Adapter struct{}

func (Adapter) Name() string { return AdapterName }

func (Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: TypeScript requires stdin test cases", execution.ErrInvalidCommandPayload)
	}
	policy := resolved.Version.FilePolicy
	if len(req.Code.Files) > tsjob.MaxFiles || policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many TypeScript source files", execution.ErrInvalidCommandPayload)
	}
	cmd, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	entry := cmd.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	entryJS, ok := tsjob.JSPath(entry)
	if !ok {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid TypeScript entrypoint", execution.ErrInvalidCommandPayload)
	}
	limits := cmd.RuntimeLimits
	if limits.MemoryBytes < tsjob.MinMemoryBytes || limits.PIDs < tsjob.MinPIDs {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: TypeScript requires at least 64 MiB and 16 PIDs", execution.ErrInvalidCommandPayload)
	}
	found := false
	compile := []string{tsjob.CompilerPath}
	for _, file := range cmd.Files {
		if _, ok := tsjob.JSPath(file.Path); !ok || !utf8.Valid(file.Content) {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: expected a UTF-8 TypeScript source", execution.ErrInvalidCommandPayload)
		}
		found = found || file.Path == entry
		compile = append(compile, file.Path)
	}
	if !found {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: TypeScript entrypoint is missing", execution.ErrInvalidCommandPayload)
	}
	heap := strconv.FormatInt(min(tsjob.MaxHeapMiB, limits.MemoryBytes/(2<<20)), 10)
	cmd.Adapter = AdapterName
	cmd.Entrypoint = entry
	cmd.RuntimeImage = resolved.Version.RuntimeImage
	cmd.WorkingDir = resolved.Version.Execution.WorkingDir
	cmd.CompileArgv = compile
	cmd.Argv = []string{tsjob.RunnerPath, entryJS, heap}
	return cmd, nil
}
