package cpp

import (
	"context"
	"fmt"
	"path"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/security"
)

const (
	AdapterName  = "cpp"
	ArtifactName = "_sandbox_cpp"
	CompilerPath = "/usr/bin/g++-14"
)

type Adapter struct{}

func NewAdapter() *Adapter { return &Adapter{} }

func (*Adapter) Name() string { return AdapterName }

func (*Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C++ requires stdin test cases", execution.ErrInvalidCommandPayload)
	}
	policy := resolved.Version.FilePolicy
	if policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many C++ files", execution.ErrInvalidCommandPayload)
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Code.Files, policy.AllowedExtensions); err != nil {
		return execution.ExecutionCommand{}, err
	}
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	entry := command.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	if path.Ext(entry) != ".cpp" {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C++ entrypoint must be a .cpp file", execution.ErrInvalidCommandPayload)
	}
	argv := []string{CompilerPath, "-std=c++17", "-O2", "-pipe", "-static", "-s", "-fdiagnostics-color=never", "-o", ArtifactName}
	foundEntry := false
	for _, file := range command.Files {
		name, err := security.NormalizeRelativePath(file.Path, security.DefaultPolicy())
		if err != nil {
			return execution.ExecutionCommand{}, err
		}
		switch path.Ext(name) {
		case ".cpp":
			// A workspace operand must not be interpreted as a compiler option.
			argv = append(argv, "./"+name)
			foundEntry = foundEntry || name == entry
		case ".h", ".hpp":
		default:
			return execution.ExecutionCommand{}, fmt.Errorf("%w: unsupported C++ file extension", execution.ErrInvalidCommandPayload)
		}
	}
	if !foundEntry {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: C++ entrypoint is missing", execution.ErrInvalidCommandPayload)
	}
	command.Adapter = AdapterName
	command.Entrypoint = entry
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Argv = []string{"./" + ArtifactName}
	command.CompileArgv = argv
	return command, nil
}
