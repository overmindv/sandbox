package csharp

import (
	"errors"
	"slices"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/dotnetjob"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

func TestAdapterBuildsFixedCommands(t *testing.T) {
	t.Parallel()
	req, lang := request()
	cmd, err := (Adapter{}).BuildCommand(t.Context(), req, lang)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmd.CompileArgv, []string{dotnetjob.CompilerPath, "src/Program.cs", "src/Helper.cs"}) ||
		!slices.Equal(cmd.Argv, []string{dotnetjob.DotnetPath, "exec", "--runtimeconfig", dotnetjob.RuntimeConfig, "./" + dotnetjob.AssemblyName}) ||
		cmd.Environment["DOTNET_GCHeapHardLimit"] != "0x4000000" || cmd.Environment["DOTNET_EnableDiagnostics"] != "0" ||
		cmd.Environment["DOTNET_PROCESSOR_COUNT"] != "1" || cmd.RuntimeLimits.PIDs != 16 || cmd.Entrypoint != "src/Program.cs" {
		t.Error("unexpected C# commands or runtime limits", cmd.CompileArgv, cmd.Argv, cmd.Environment)
	}
}

func TestAdapterRejectsUnsupportedInputs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"@flags.cs", "-bad.cs", "../Other.cs", "src\\Helper.cs", "Helper.csx", "build.csproj", "NuGet.Config", "payload.dll"} {
		t.Run(name, func(t *testing.T) {
			req, lang := request()
			req.Code.Files[1].Path = name
			if _, err := (Adapter{}).BuildCommand(t.Context(), req, lang); !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("accepted unsupported input", err)
			}
		})
	}
	req, lang := request()
	req.Limits.MemoryMB = 64
	if _, err := (Adapter{}).BuildCommand(t.Context(), req, lang); !errors.Is(err, execution.ErrInvalidCommandPayload) {
		t.Error("accepted insufficient memory", err)
	}
}

func TestProfileDisabledByDefault(t *testing.T) {
	t.Parallel()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range manifest.Languages {
		if profile.ID == AdapterName {
			if profile.Enabled || profile.Adapter != AdapterName || len(profile.Versions) != 1 ||
				profile.Versions[0].Version != "10.0" || len(profile.Versions[0].TestModes) != 0 {
				t.Error("unexpected C# profile", profile)
			}
			return
		}
	}
	t.Error("C# profile is missing")
}

func request() (contracts.RunRequest, languages.ResolvedLanguage) {
	return contracts.RunRequest{
			Code:      contracts.CodeSpec{Files: []contracts.SourceFile{{Path: "src/Program.cs"}, {Path: "src/Helper.cs"}}},
			Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
			Limits:    contracts.Limits{MemoryMB: 128, PIDs: 16},
		}, languages.ResolvedLanguage{
			Version: config.LanguageVersionConfig{FilePolicy: config.LanguageFilePolicy{DefaultEntrypoint: "src/Program.cs"}},
		}
}
