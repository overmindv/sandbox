package native

import (
	"context"
	"fmt"
	"path"
	"unicode/utf8"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/security"
)

const (
	CName     = "c"
	CPPName   = "cpp"
	SwiftName = "swift"
)

type Adapter struct {
	name      string
	label     string
	compiler  string
	standard  string
	sourceExt string
	math      bool
}

func C17() Adapter {
	return Adapter{name: CName, label: "C17", compiler: "/usr/bin/gcc-14", standard: "-std=c17", sourceExt: ".c", math: true}
}

func CPP17() Adapter {
	return Adapter{name: CPPName, label: "C++", compiler: "/usr/bin/g++-14", standard: "-std=c++17", sourceExt: ".cpp"}
}

func Swift() Adapter {
	return Adapter{name: SwiftName, label: "Swift", compiler: "/usr/bin/swiftc", sourceExt: ".swift"}
}

func Lookup(name string) (Adapter, bool) {
	switch name {
	case CName:
		return C17(), true
	case CPPName:
		return CPP17(), true
	case SwiftName:
		return Swift(), true
	default:
		return Adapter{}, false
	}
}

func (a Adapter) Name() string         { return a.name }
func (a Adapter) Label() string        { return a.label }
func (a Adapter) CompilerPath() string { return a.compiler }
func (a Adapter) ArtifactName() string { return "_sandbox_" + a.name }

func (a Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s requires stdin test cases", execution.ErrInvalidCommandPayload, a.label)
	}
	policy := resolved.Version.FilePolicy
	if policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many %s files", execution.ErrInvalidCommandPayload, a.label)
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
	if path.Ext(entry) != a.sourceExt {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s entrypoint must be a %s file", execution.ErrInvalidCommandPayload, a.label, a.sourceExt)
	}
	if a.name == SwiftName && path.Base(entry) != "main.swift" {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: Swift entrypoint must be named main.swift", execution.ErrInvalidCommandPayload)
	}
	artifact := a.ArtifactName()
	argv := a.compileArgs(artifact)
	foundEntry := false
	for _, file := range command.Files {
		name, err := security.NormalizeRelativePath(file.Path, security.DefaultPolicy())
		if err != nil {
			return execution.ExecutionCommand{}, err
		}
		ext := path.Ext(name)
		switch {
		case ext == a.sourceExt:
			if a.name == SwiftName && !utf8.Valid(file.Content) {
				return execution.ExecutionCommand{}, fmt.Errorf("%w: expected UTF-8 Swift source", execution.ErrInvalidCommandPayload)
			}
			// A workspace operand must not be interpreted as a compiler option.
			argv = append(argv, "./"+name)
			foundEntry = foundEntry || name == entry
		case (a.name == CName || a.name == CPPName) && ext == ".h", a.name == CPPName && ext == ".hpp":
		default:
			return execution.ExecutionCommand{}, fmt.Errorf("%w: unsupported %s file extension", execution.ErrInvalidCommandPayload, a.label)
		}
	}
	if !foundEntry {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s entrypoint is missing", execution.ErrInvalidCommandPayload, a.label)
	}
	if a.math {
		argv = append(argv, "-lm")
	}
	command.Adapter = a.name
	command.Entrypoint = entry
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Argv = []string{"./" + artifact}
	command.CompileArgv = argv
	return command, nil
}

func (a Adapter) compileArgs(artifact string) []string {
	if a.name == SwiftName {
		return []string{a.compiler, "-O", "-whole-module-optimization", "-j1", "-module-cache-path", "/tmp/sandbox-swift-cache",
			"-module-name", "SandboxSolution", "-o", artifact}
	}
	return []string{a.compiler, a.standard, "-O2", "-pipe", "-static", "-s", "-fdiagnostics-color=never", "-o", artifact}
}
