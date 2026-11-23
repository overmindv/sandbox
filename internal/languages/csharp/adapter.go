package csharp

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/dotnetjob"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

const AdapterName = "csharp"

type Adapter struct{}

func (Adapter) Name() string { return AdapterName }

func (Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C# requires stdin test cases", execution.ErrInvalidCommandPayload)
	}
	policy := resolved.Version.FilePolicy
	if len(req.Code.Files) > dotnetjob.MaxSources || policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many C# source files", execution.ErrInvalidCommandPayload)
	}
	cmd, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	entry := cmd.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	if !dotnetjob.ValidSourcePath(entry) {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid C# entrypoint", execution.ErrInvalidCommandPayload)
	}
	if cmd.RuntimeLimits.MemoryBytes < dotnetjob.MinMemoryBytes || cmd.RuntimeLimits.PIDs < dotnetjob.MinPIDs {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C# requires at least 96 MiB and 16 PIDs", execution.ErrInvalidCommandPayload)
	}
	found := false
	compile := []string{dotnetjob.CompilerPath}
	for _, file := range cmd.Files {
		if !dotnetjob.ValidSourcePath(file.Path) || !utf8.Valid(file.Content) {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: expected a UTF-8 C# source", execution.ErrInvalidCommandPayload)
		}
		found = found || file.Path == entry
		compile = append(compile, file.Path)
	}
	if !found {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C# entrypoint is missing", execution.ErrInvalidCommandPayload)
	}
	cmd.Adapter = AdapterName
	cmd.Entrypoint = entry
	cmd.RuntimeImage = resolved.Version.RuntimeImage
	cmd.WorkingDir = resolved.Version.Execution.WorkingDir
	cmd.CompileArgv = compile
	cmd.Argv = []string{dotnetjob.DotnetPath, "exec", "--runtimeconfig", dotnetjob.RuntimeConfig, "./" + dotnetjob.AssemblyName}
	cmd.Environment = map[string]string{
		"LC_ALL":                   "C.UTF-8",
		"DOTNET_EnableDiagnostics": "0",
		"DOTNET_PROCESSOR_COUNT":   "1",
		"DOTNET_GCHeapHardLimit":   "0x" + strconv.FormatInt(cmd.RuntimeLimits.MemoryBytes/2, 16),
	}
	return cmd, nil
}
