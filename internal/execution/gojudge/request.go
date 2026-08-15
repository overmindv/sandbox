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
	CopyOutMax      int64                 `json:"copyOutMax,omitempty"`
	CopyOutTruncate bool                  `json:"copyOutTruncate,omitempty"`
}

type FileDescriptor struct {
	Content *string `json:"content,omitempty"`
	Name    string  `json:"name,omitempty"`
	Max     int     `json:"max,omitempty"`
}

type MemoryFile struct {
	Content string `json:"content"`
}

func RequestFromExecutionCommand(command execution.ExecutionCommand) (Request, error) {
	if len(command.Argv) == 0 {
		return Request{}, fmt.Errorf("%w: argv is required", execution.ErrInvalidCommandPayload)
	}

	copyIn := make(map[string]MemoryFile, len(command.Files))
	for _, file := range command.Files {
		normalized, err := security.NormalizeRelativePath(file.Path, security.DefaultPolicy())
		if err != nil {
			return Request{}, fmt.Errorf("copyIn path %q: %w", file.Path, err)
		}
		copyIn[normalized] = MemoryFile{Content: string(file.Content)}
	}

	copyOut := make([]string, 0, len(command.CopyOut))
	for _, path := range command.CopyOut {
		normalized, err := security.ValidateCopyOutPath(path, security.DefaultPolicy())
		if err != nil {
			return Request{}, fmt.Errorf("copyOut path %q: %w", path, err)
		}
		copyOut = append(copyOut, optionalCopyOut(normalized))
	}

	stdin := ""
	cmd := Command{
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
		CopyOutMax:      command.RuntimeLimits.WorkspaceBytes,
		CopyOutTruncate: false,
	}
	if cmd.CopyOutMax <= 0 {
		cmd.CopyOutMax = 1024 * 1024
	}

	return Request{
		RequestID: command.AttemptID,
		Cmd:       []Command{cmd},
	}, nil
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
