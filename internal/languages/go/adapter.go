package golang

import (
	"context"
	"fmt"
	"strings"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

const (
	AdapterName           = "go"
	DefaultEntrypoint     = "solution.go"
	DefaultTestEntrypoint = "solution_test.go"
	DefaultModulePath     = "sandbox"
)

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return AdapterName
}

func (a *Adapter) BuildCommand(ctx context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	_ = ctx
	if err := validateGoFiles(req, resolved); err != nil {
		return execution.ExecutionCommand{}, err
	}

	if req.Execution != nil && req.Execution.Mode == contracts.ExecutionMode {
		return buildExecutionCommand(req, resolved)
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
	command.Argv = GoTestArgv(resolved.Version.Execution.Command.Argv)
	command.Files = buildWorkspaceFiles(command.Files)
	command.RuntimeLimits = execution.RuntimeLimitsFromRequest(req.Limits)
	return command, nil
}

// buildExecutionCommand строит команду исполнения go-программы по входным кейсам.
// mode=execution запускает пакет через "go run ." вместо go test.
func buildExecutionCommand(req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}

	command.Adapter = AdapterName
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Environment = cloneStringMap(resolved.Version.Execution.Environment)
	command.Argv = []string{"go", "run", "."}
	command.Files = buildWorkspaceFiles(command.Files)
	command.RuntimeLimits = execution.RuntimeLimitsFromRequest(req.Limits)
	return command, nil
}

func validateGoFiles(req contracts.RunRequest, resolved languages.ResolvedLanguage) error {
	maxFiles := resolved.Version.FilePolicy.MaxFiles
	if maxFiles > 0 && len(req.Code.Files)+len(req.Tests.Files) > maxFiles {
		return fmt.Errorf("go workspace contains %d files, max is %d", len(req.Code.Files)+len(req.Tests.Files), maxFiles)
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Code.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
		return err
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Tests.Files, resolved.Version.FilePolicy.AllowedExtensions); err != nil {
		return err
	}
	return nil
}

func buildWorkspaceFiles(files []execution.ExecutionFile) []execution.ExecutionFile {
	files = append([]execution.ExecutionFile(nil), files...)
	if hasGoMod(files) {
		return files
	}
	files = append(files, execution.ExecutionFile{
		Path:    "go.mod",
		Kind:    execution.FileKindBuild,
		Content: []byte("module " + DefaultModulePath + "\n\ngo 1.26\n"),
	})
	return files
}

func hasGoMod(files []execution.ExecutionFile) bool {
	for _, file := range files {
		if strings.EqualFold(file.Path, "go.mod") {
			return true
		}
	}
	return false
}

func cloneStringMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
