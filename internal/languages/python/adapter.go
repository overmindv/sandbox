package python

import (
	"context"
	"fmt"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

const AdapterName = "python"

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return AdapterName
}

func (a *Adapter) BuildCommand(ctx context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	_ = ctx
	if err := validatePythonFiles(req, resolved); err != nil {
		return execution.ExecutionCommand{}, err
	}

	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}

	command.Adapter = AdapterName
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	if resolved.Version.Execution.Compile != nil {
		command.CompileArgv = append([]string(nil), resolved.Version.Execution.Compile.Argv...)
	}
	command.Environment = cloneStringMap(resolved.Version.Execution.Environment)
	if command.Entrypoint == "" {
		command.Entrypoint = resolved.Version.FilePolicy.DefaultEntrypoint
	}
	if command.Entrypoint == "" {
		command.Entrypoint = DefaultEntrypoint
	}
	if command.TestEntrypoint == "" {
		command.TestEntrypoint = resolved.Version.FilePolicy.DefaultTestEntrypoint
	}
	if command.TestEntrypoint == "" {
		command.TestEntrypoint = DefaultTestEntrypoint
	}
	command.Argv = PytestArgv(resolved.Version.Execution.Command.Argv, command.TestEntrypoint)
	command.Environment["SANDBOX_PYTEST_REPORT"] = ReportPath
	command.CopyOut = []string{ReportPath}
	command.Files = buildWorkspaceFiles(command.Files)
	command.RuntimeLimits = MapLimits(req.Limits)
	return command, nil
}

func validatePythonFiles(req contracts.RunRequest, resolved languages.ResolvedLanguage) error {
	maxFiles := resolved.Version.FilePolicy.MaxFiles
	if maxFiles > 0 && len(req.Code.Files)+len(req.Tests.Files) > maxFiles {
		return fmt.Errorf("python workspace contains %d files, max is %d", len(req.Code.Files)+len(req.Tests.Files), maxFiles)
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Code.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
		return err
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Tests.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
		return err
	}
	if err := rejectReservedPaths(req.Code.Files); err != nil {
		return err
	}
	if err := rejectReservedPaths(req.Tests.Files); err != nil {
		return err
	}
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
