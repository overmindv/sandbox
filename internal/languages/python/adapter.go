package python

import (
	"context"

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
	command.Argv = append([]string(nil), resolved.Version.Execution.Command.Argv...)
	if resolved.Version.Execution.Compile != nil {
		command.CompileArgv = append([]string(nil), resolved.Version.Execution.Compile.Argv...)
	}
	command.Environment = cloneStringMap(resolved.Version.Execution.Environment)
	if command.Entrypoint == "" {
		command.Entrypoint = resolved.Version.FilePolicy.DefaultEntrypoint
	}
	if command.TestEntrypoint == "" {
		command.TestEntrypoint = resolved.Version.FilePolicy.DefaultTestEntrypoint
	}
	return command, nil
}

func validatePythonFiles(req contracts.RunRequest, resolved languages.ResolvedLanguage) error {
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Code.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
		return err
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Tests.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
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
