package java

import (
	"encoding/base64"
	"errors"
	"slices"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/javaclass"
	"overmindv/sandbox/internal/languages"
)

func TestBuildCommand(t *testing.T) {
	t.Parallel()
	req, resolved := javaRequest()
	command, err := (Adapter{}).BuildCommand(t.Context(), req, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.CompileArgv, []string{javaclass.CompilerPath, "pkg/Main.java", "pkg/Main.java", "pkg/Helper.java"}) {
		t.Error("unexpected compile argv", command.CompileArgv)
	}
	if !slices.Equal(command.Argv[len(command.Argv)-3:], []string{"-cp", "./" + javaclass.ArchiveName, "pkg.Main"}) ||
		!slices.Contains(command.Argv, "-Xmx64m") || command.Argv[0] != "/usr/bin/java" {
		t.Error("unexpected runner argv", command.Argv)
	}
	if command.RuntimeLimits.MemoryBytes != 128<<20 || command.RuntimeLimits.PIDs != 32 ||
		len(command.Environment) != 0 || len(command.CopyOut) != 0 || !slices.Equal(command.Inputs, []string{"first", "second"}) {
		t.Error("runtime controls changed", command.RuntimeLimits)
	}
}

func TestRejectInvalidRequests(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*contracts.RunRequest){
		"wrong mode":       func(r *contracts.RunRequest) { r.Execution = nil },
		"missing main":     func(r *contracts.RunRequest) { r.Code.Entrypoint = "Other.java" },
		"unsafe operand":   func(r *contracts.RunRequest) { r.Code.Files[1].Path = "@Helper.java" },
		"parent segment":   func(r *contracts.RunRequest) { r.Code.Files[1].Path = "pkg/../Helper.java" },
		"bytecode input":   func(r *contracts.RunRequest) { r.Code.Files[1].Path = "pkg/Helper.class" },
		"invalid encoding": func(r *contracts.RunRequest) { r.Code.Files[1].ContentB64 = "/w==" },
		"low memory":       func(r *contracts.RunRequest) { r.Limits.MemoryMB = 64 },
		"low pids":         func(r *contracts.RunRequest) { r.Limits.PIDs = 8 },
		"test material":    func(r *contracts.RunRequest) { r.Tests.Mode = contracts.TestModePytest },
	} {
		t.Run(name, func(t *testing.T) {
			req, resolved := javaRequest()
			mutate(&req)
			if _, err := (Adapter{}).BuildCommand(t.Context(), req, resolved); !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("expected a Java validation error", err)
			}
		})
	}
}

func TestManifestKeepsJavaDisabled(t *testing.T) {
	t.Parallel()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == AdapterName {
			if language.Enabled || language.Adapter != AdapterName || len(language.Versions) != 1 ||
				language.Versions[0].Version != "21" || len(language.Versions[0].TestModes) != 0 {
				t.Error("unexpected Java profile", language)
			}
			return
		}
	}
	t.Error("Java profile is missing")
}

func javaRequest() (contracts.RunRequest, languages.ResolvedLanguage) {
	return contracts.RunRequest{
			Code: contracts.CodeSpec{
				Files: []contracts.SourceFile{
					{Path: "pkg/Main.java", ContentB64: base64.StdEncoding.EncodeToString([]byte("package pkg; class Main {}"))},
					{Path: "pkg/Helper.java", ContentB64: base64.StdEncoding.EncodeToString([]byte("package pkg; class Helper {}"))},
				},
			},
			Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"first", "second"}},
			Limits:    contracts.Limits{MemoryMB: 128, PIDs: 32},
		}, languages.ResolvedLanguage{Version: config.LanguageVersionConfig{
			FilePolicy: config.LanguageFilePolicy{DefaultEntrypoint: "pkg/Main.java"},
		}}
}
