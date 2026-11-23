package typescript

import (
	"errors"
	"slices"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/tsjob"
)

func TestBuildCommand(t *testing.T) {
	t.Parallel()
	req := request()
	cmd, err := (Adapter{}).BuildCommand(t.Context(), req, language())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmd.CompileArgv, []string{tsjob.CompilerPath, "src/main.ts", "src/helper.ts"}) ||
		!slices.Equal(cmd.Argv, []string{tsjob.RunnerPath, "src/main.js", "64"}) || len(cmd.Environment) != 0 {
		t.Error("unexpected compile or runtime command", cmd.CompileArgv, cmd.Argv)
	}
}

func TestRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"@args.ts", "-flags.ts", "abs/../helper.ts", "helper.js", "helper.d.ts", "tsconfig.json", "package.json"} {
		t.Run(name, func(t *testing.T) {
			req := request()
			req.Code.Files[1].Path = name
			if _, err := (Adapter{}).BuildCommand(t.Context(), req, language()); !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("accepted unsupported file", err)
			}
		})
	}
	req := request()
	req.Limits.MemoryMB = 32
	if _, err := (Adapter{}).BuildCommand(t.Context(), req, language()); !errors.Is(err, execution.ErrInvalidCommandPayload) {
		t.Error("accepted insufficient memory", err)
	}
}

func TestProfileDisabledByDefault(t *testing.T) {
	t.Parallel()
	m, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range m.Languages {
		if profile.ID == AdapterName {
			if profile.Enabled || profile.Adapter != AdapterName || len(profile.Versions) != 1 ||
				profile.Versions[0].Version != "7.0" || len(profile.Versions[0].TestModes) != 0 {
				t.Error("unexpected TypeScript profile", profile)
			}
			return
		}
	}
	t.Error("TypeScript profile missing")
}

func request() contracts.RunRequest {
	return contracts.RunRequest{
		Code:      contracts.CodeSpec{Files: []contracts.SourceFile{{Path: "src/main.ts"}, {Path: "src/helper.ts"}}},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{MemoryMB: 128, PIDs: 32},
	}
}

func language() languages.ResolvedLanguage {
	return languages.ResolvedLanguage{Version: config.LanguageVersionConfig{
		FilePolicy: config.LanguageFilePolicy{DefaultEntrypoint: "src/main.ts"},
	}}
}
