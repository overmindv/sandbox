package java

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/javaclass"
	"overmindv/sandbox/internal/languages"
)

const (
	AdapterName = "java"
	minMemory   = 96 << 20
	minPIDs     = 16
)

type Adapter struct{}

func (Adapter) Name() string { return AdapterName }

func (Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: Java requires stdin test cases", execution.ErrInvalidCommandPayload)
	}
	policy := resolved.Version.FilePolicy
	if len(req.Code.Files) > javaclass.MaxSources || policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many Java source files", execution.ErrInvalidCommandPayload)
	}
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	entry := command.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	main, ok := javaclass.MainClass(entry)
	if !ok {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid Java entrypoint", execution.ErrInvalidCommandPayload)
	}
	if command.RuntimeLimits.MemoryBytes < minMemory || command.RuntimeLimits.PIDs < minPIDs {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: Java requires at least 96 MiB and 16 PIDs", execution.ErrInvalidCommandPayload)
	}
	compile := []string{javaclass.CompilerPath, entry}
	found := false
	for _, file := range command.Files {
		if _, ok := javaclass.MainClass(file.Path); !ok {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid Java source path", execution.ErrInvalidCommandPayload)
		}
		if !utf8.Valid(file.Content) {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: Java source must be UTF-8", execution.ErrInvalidCommandPayload)
		}
		compile = append(compile, file.Path)
		found = found || file.Path == entry
	}
	if !found {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: Java entrypoint is missing", execution.ErrInvalidCommandPayload)
	}
	command.Adapter = AdapterName
	command.Entrypoint = entry
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Argv = runArgv(main, command.RuntimeLimits.MemoryBytes)
	command.CompileArgv = compile
	return command, nil
}

func runArgv(main string, memory int64) []string {
	// Reserve the rest of the cgroup budget for JVM threads, metadata and native memory.
	heap := strconv.FormatInt(memory/(2<<20), 10) + "m"
	return []string{
		"/usr/bin/java", "-Xms8m", "-Xmx" + heap, "-Xss512k",
		"-XX:+UseSerialGC", "-XX:ActiveProcessorCount=1", "-XX:-UseContainerSupport",
		"-XX:MaxMetaspaceSize=32m", "-XX:ReservedCodeCacheSize=16m",
		"-XX:+DisableAttachMechanism", "-XX:-UsePerfData", "-XX:-CreateCoredumpOnCrash",
		"-XX:ErrorFile=/dev/null", "-Djava.io.tmpdir=/tmp",
		"-cp", "./" + javaclass.ArchiveName, main,
	}
}
