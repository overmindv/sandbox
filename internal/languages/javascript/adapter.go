package javascript

import (
	"context"
	"fmt"
	"path"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/security"
)

const AdapterName = "javascript"

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (*Adapter) Name() string {
	return AdapterName
}

func (*Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, languages.UnsupportedLanguageError{
			LanguageID: req.Language.ID, Version: req.Language.Version, Reason: "JavaScript requires execution mode",
		}
	}
	policy := resolved.Version.FilePolicy
	if policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: JavaScript workspace exceeds the file limit", execution.ErrInvalidCommandPayload)
	}
	if err := languages.ValidateAllowedExtensionsForAdapter(req.Code.Files, policy.AllowedExtensions); err != nil {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %v", execution.ErrInvalidCommandPayload, err)
	}
	entry := req.Code.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	if err := validateEntrypoint(entry, req.Code.Files); err != nil {
		return execution.ExecutionCommand{}, err
	}

	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	command.Adapter = AdapterName
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Entrypoint = entry
	command.Argv = []string{"/usr/local/bin/node", "--", "./" + entry}
	return command, nil
}

func validateEntrypoint(entry string, files []contracts.SourceFile) error {
	if err := security.ValidateWorkspacePath(entry, security.Policy{}); err != nil {
		return fmt.Errorf("%w: invalid JavaScript entrypoint", execution.ErrInvalidCommandPayload)
	}
	switch path.Ext(entry) {
	case ".js", ".mjs", ".cjs":
	default:
		return fmt.Errorf("%w: JavaScript entrypoint must be a .js, .mjs or .cjs file", execution.ErrInvalidCommandPayload)
	}
	for _, file := range files {
		if file.Path == entry {
			return nil
		}
	}
	return fmt.Errorf("%w: JavaScript entrypoint is missing from the workspace", execution.ErrInvalidCommandPayload)
}
