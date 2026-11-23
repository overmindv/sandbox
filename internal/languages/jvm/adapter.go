package jvm

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
	JavaName   = "java"
	KotlinName = "kotlin"
	minMemory  = 96 << 20
	minPIDs    = 16
)

type Adapter struct {
	name, label, compiler, archive, stdlib string
	mainClass                              func(string) (string, bool)
}

func Java() Adapter {
	return Adapter{name: JavaName, label: "Java", compiler: javaclass.CompilerPath,
		archive: javaclass.ArchiveName, mainClass: javaclass.MainClass}
}

func Kotlin() Adapter {
	return Adapter{name: KotlinName, label: "Kotlin", compiler: javaclass.KotlinCompilerPath,
		archive: javaclass.KotlinArchiveName, stdlib: javaclass.KotlinStdlibPath, mainClass: javaclass.KotlinMainClass}
}

func Lookup(name string) (Adapter, bool) {
	switch name {
	case JavaName:
		return Java(), true
	case KotlinName:
		return Kotlin(), true
	default:
		return Adapter{}, false
	}
}

func (a Adapter) Name() string         { return a.name }
func (a Adapter) Label() string        { return a.label }
func (a Adapter) CompilerPath() string { return a.compiler }
func (a Adapter) ArchiveName() string  { return a.archive }
func (a Adapter) MainClass(entry string) (string, bool) {
	return a.mainClass(entry)
}

func (a Adapter) BuildCommand(_ context.Context, req contracts.RunRequest, resolved languages.ResolvedLanguage) (execution.ExecutionCommand, error) {
	if req.Execution == nil || req.Execution.Mode != contracts.ExecutionMode {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s requires stdin test cases", execution.ErrInvalidCommandPayload, a.label)
	}
	policy := resolved.Version.FilePolicy
	if len(req.Code.Files) > javaclass.MaxSources || policy.MaxFiles > 0 && len(req.Code.Files) > policy.MaxFiles {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: too many %s source files", execution.ErrInvalidCommandPayload, a.label)
	}
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		return execution.ExecutionCommand{}, err
	}
	entry := command.Entrypoint
	if entry == "" {
		entry = policy.DefaultEntrypoint
	}
	main, ok := a.MainClass(entry)
	if !ok {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid %s entrypoint", execution.ErrInvalidCommandPayload, a.label)
	}
	if command.RuntimeLimits.MemoryBytes < minMemory || command.RuntimeLimits.PIDs < minPIDs {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s requires at least 96 MiB and 16 PIDs", execution.ErrInvalidCommandPayload, a.label)
	}
	compile := []string{a.compiler, entry}
	found := false
	for _, file := range command.Files {
		if _, ok := a.MainClass(file.Path); !ok {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: invalid %s source path", execution.ErrInvalidCommandPayload, a.label)
		}
		if !utf8.Valid(file.Content) {
			return execution.ExecutionCommand{}, fmt.Errorf("%w: %s source must be UTF-8", execution.ErrInvalidCommandPayload, a.label)
		}
		compile = append(compile, file.Path)
		found = found || file.Path == entry
	}
	if !found {
		return execution.ExecutionCommand{}, fmt.Errorf("%w: %s entrypoint is missing", execution.ErrInvalidCommandPayload, a.label)
	}
	command.Adapter = a.name
	command.Entrypoint = entry
	command.RuntimeImage = resolved.Version.RuntimeImage
	command.WorkingDir = resolved.Version.Execution.WorkingDir
	command.Argv = a.runArgv(main, command.RuntimeLimits.MemoryBytes)
	command.CompileArgv = compile
	return command, nil
}

func (a Adapter) runArgv(main string, memory int64) []string {
	// Reserve the rest of the cgroup budget for JVM threads, metadata and native memory.
	heap := strconv.FormatInt(memory/(2<<20), 10) + "m"
	classpath := "./" + a.archive
	if a.stdlib != "" {
		classpath = a.stdlib + ":" + classpath
	}
	return []string{
		"/usr/bin/java", "-Xms8m", "-Xmx" + heap, "-Xss512k",
		"-XX:+UseSerialGC", "-XX:ActiveProcessorCount=1", "-XX:-UseContainerSupport",
		"-XX:MaxMetaspaceSize=32m", "-XX:ReservedCodeCacheSize=16m",
		"-XX:+DisableAttachMechanism", "-XX:-UsePerfData", "-XX:-CreateCoredumpOnCrash",
		"-XX:ErrorFile=/dev/null", "-Djava.io.tmpdir=/tmp",
		"-cp", classpath, main,
	}
}
