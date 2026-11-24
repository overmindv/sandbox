package sqlite

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"path"
	"strconv"
	"unicode/utf8"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/security"
)

const (
	AdapterName     = "sqlite"
	FixtureExitCode = 10
	OutputExitCode  = 11
	harnessPath     = "_sandbox_sqlite.py"
	maxQueryBytes   = 64 * 1024
	maxFixtureBytes = 256 * 1024
)

//go:embed runner.py
var runner string

type Adapter struct{}

func (Adapter) Name() string { return AdapterName }

func (Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode || len(req.Execution.Inputs) == 0 {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: SQLite requires execution input cases", execution.ErrInvalidCommandPayload)
	}
	for _, input := range req.Execution.Inputs {
		if len(input) > maxFixtureBytes || !utf8.ValidString(input) {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid SQLite fixture payload", execution.ErrInvalidCommandPayload)
		}
	}
	if len(req.Code.Files) != 1 {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: SQLite requires one .sql file", execution.ErrInvalidCommandPayload)
	}
	entry := req.Code.Entrypoint
	if entry == "" {
		entry = resolved.Version.FilePolicy.DefaultEntrypoint
	}
	if err := security.ValidateWorkspacePath(entry, security.DefaultPolicy()); err != nil ||
		path.Ext(entry) != ".sql" || req.Code.Files[0].Path != entry {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid SQLite entrypoint", execution.ErrInvalidCommandPayload)
	}
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	query := command.Files[0].Content
	if len(query) == 0 || len(query) > maxQueryBytes || !utf8.Valid(query) || bytes.IndexByte(query, 0) >= 0 {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid SQLite query payload", execution.ErrInvalidCommandPayload)
	}
	if command.RuntimeLimits.StdoutBytes < 2 {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: SQLite requires at least two output bytes", execution.ErrInvalidCommandPayload)
	}
	command.Adapter = AdapterName
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Entrypoint = entry
	command.Argv = []string{"python3", "-I", "./" + harnessPath, "./" + entry, strconv.Itoa(command.RuntimeLimits.StdoutBytes)}
	command.Files = append(command.Files, execution.ExecutionFile{
		Path: harnessPath, Kind: execution.FileKindBuild, Content: []byte(runner),
	})
	return command, nil
}
