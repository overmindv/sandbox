package gojudge

import (
	"fmt"
	"sort"
	"strings"

	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/security"
)

const (
	stdoutName = "stdout"
	stderrName = "stderr"
)

type Request struct {
	RequestID string    `json:"requestId,omitempty"`
	Cmd       []Command `json:"cmd"`
}

type Command struct {
	Args            []string              `json:"args"`
	Env             []string              `json:"env,omitempty"`
	Files           []FileDescriptor      `json:"files,omitempty"`
	CPULimit        int64                 `json:"cpuLimit,omitempty"`
	ClockLimit      int64                 `json:"clockLimit,omitempty"`
	MemoryLimit     int64                 `json:"memoryLimit,omitempty"`
	ProcLimit       int                   `json:"procLimit,omitempty"`
	CopyIn          map[string]MemoryFile `json:"copyIn,omitempty"`
	CopyOut         []string              `json:"copyOut,omitempty"`
	CopyOutCached   []string              `json:"copyOutCached,omitempty"`
	CopyOutMax      int64                 `json:"copyOutMax,omitempty"`
	CopyOutTruncate bool                  `json:"copyOutTruncate,omitempty"`
}

type FileDescriptor struct {
	Content *string `json:"content,omitempty"`
	Name    string  `json:"name,omitempty"`
	Max     int     `json:"max,omitempty"`
}

type MemoryFile struct {
	Content *string `json:"content,omitempty"`
	FileID  string  `json:"fileId,omitempty"`
}

func RequestFromExecutionCommand(command execution.ExecutionCommand) (Request, error) {
	if len(command.Argv) == 0 {
		return Request{}, fmt.Errorf("%w: argv is required", execution.ErrInvalidCommandPayload)
	}
	if err := security.ValidateRuntimeEnvironment(command.Environment); err != nil {
		return Request{}, err
	}
	if command.RuntimeLimits.PIDs <= 0 {
		return Request{}, fmt.Errorf("%w: runtime pids limit is required", execution.ErrInvalidCommandPayload)
	}
	if command.RuntimeLimits.MemoryBytes <= 0 {
		return Request{}, fmt.Errorf("%w: runtime memory limit is required", execution.ErrInvalidCommandPayload)
	}

	if execution.IsExecutionMode(command) {
		if len(command.CopyOut) > 0 || command.TestMode != "" || command.TestEntrypoint != "" {
			return Request{}, fmt.Errorf("%w: execution contains report controls", execution.ErrInvalidCommandPayload)
		}
		for _, file := range command.Files {
			if file.Kind != execution.FileKindSource && file.Kind != execution.FileKindBuild {
				return Request{}, fmt.Errorf("%w: execution contains test material", execution.ErrInvalidCommandPayload)
			}
		}
	}
	copyIn := make(map[string]MemoryFile, len(command.Files))
	for _, file := range command.Files {
		normalized, err := security.NormalizeRelativePath(file.Path, security.DefaultPolicy())
		if err != nil {
			return Request{}, fmt.Errorf("copyIn path %q: %w", file.Path, err)
		}
		content := string(file.Content)
		copyIn[normalized] = MemoryFile{Content: &content}
	}

	copyOut := make([]string, 0, len(command.CopyOut))
	for _, path := range command.CopyOut {
		normalized, err := security.ValidateCopyOutPath(path, security.DefaultPolicy())
		if err != nil {
			return Request{}, fmt.Errorf("copyOut path %q: %w", path, err)
		}
		copyOut = append(copyOut, optionalCopyOut(normalized))
	}

	copyOutMax := command.RuntimeLimits.WorkspaceBytes
	if copyOutMax <= 0 {
		copyOutMax = 1024 * 1024
	}

	if execution.IsExecutionMode(command) {
		if len(command.Inputs) == 0 {
			return Request{}, fmt.Errorf("%w: execution mode requires at least one input", execution.ErrInvalidCommandPayload)
		}
		cmds := make([]Command, 0, len(command.Inputs))
		for _, input := range command.Inputs {
			cmds = append(cmds, baseCommand(command, copyIn, copyOut, copyOutMax, input))
		}

		return Request{
			RequestID: command.AttemptID,
			Cmd:       cmds,
		}, nil
	}

	return Request{
		RequestID: command.AttemptID,
		Cmd:       []Command{baseCommand(command, copyIn, copyOut, copyOutMax, "")},
	}, nil
}

// baseCommand строит одну go-judge команду с заданным stdin.
func baseCommand(command execution.ExecutionCommand, copyIn map[string]MemoryFile, copyOut []string, copyOutMax int64, stdin string) Command {
	return Command{
		Args: append([]string(nil), command.Argv...),
		Env:  environment(command.Environment),
		Files: []FileDescriptor{
			{Content: &stdin},
			{Name: stdoutName, Max: command.RuntimeLimits.StdoutBytes},
			{Name: stderrName, Max: command.RuntimeLimits.StderrBytes},
		},
		CPULimit:        millisToNanos(command.RuntimeLimits.CPUms),
		ClockLimit:      millisToNanos(command.RuntimeLimits.WallMS),
		MemoryLimit:     command.RuntimeLimits.MemoryBytes,
		ProcLimit:       command.RuntimeLimits.PIDs,
		CopyIn:          copyIn,
		CopyOut:         copyOut,
		CopyOutMax:      copyOutMax,
		CopyOutTruncate: false,
	}
}

func environment(values map[string]string) []string {
	merged := make(map[string]string, len(values)+1)
	hasPath := false
	for key, value := range values {
		if strings.EqualFold(key, "PATH") {
			hasPath = true
		}
		merged[key] = value
	}
	if !hasPath {
		merged["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+merged[key])
	}
	return env
}

func millisToNanos(value int) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value) * 1000 * 1000
}

func optionalCopyOut(path string) string {
	if strings.HasSuffix(path, "?") {
		return path
	}
	return path + "?"
}
