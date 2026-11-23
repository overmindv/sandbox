package cpp

import (
	"slices"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

func TestBuildsCPPCommand(t *testing.T) {
	t.Parallel()

	req := sampleRequest()
	req.Code.Entrypoint = "src/-main.cpp"
	req.Code.Files[0].Path = req.Code.Entrypoint
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "src/helper.cpp", ContentB64: "YQ=="},
		contracts.SourceFile{Path: "src/local.hpp", ContentB64: "YQ=="})
	req.Metadata = map[string]any{"CXXFLAGS": "-g"}
	command, err := NewAdapter().BuildCommand(t.Context(), req, cppLanguage(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CompilerPath, "-std=c++17", "-O2", "-pipe", "-static", "-s", "-fdiagnostics-color=never",
		"-o", ArtifactName, "./src/-main.cpp", "./src/helper.cpp"}
	if !slices.Equal(command.CompileArgv, want) || !slices.Equal(command.Argv, []string{"./" + ArtifactName}) {
		t.Error("unexpected compiler or runtime arguments", command.CompileArgv, command.Argv)
	}
	if len(command.Environment) != 0 || len(command.CopyOut) != 0 ||
		command.RuntimeLimits != execution.RuntimeLimitsFromRequest(req.Limits) || !slices.Equal(command.Inputs, req.Execution.Inputs) {
		t.Error("unexpected execution controls", command)
	}
}

func TestRejectsInvalidCPPRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*contracts.RunRequest)
	}{
		{"non-stdin mode", func(req *contracts.RunRequest) { req.Execution = nil }},
		{"missing entrypoint", func(req *contracts.RunRequest) { req.Code.Entrypoint = "other.cpp" }},
		{"parent path", func(req *contracts.RunRequest) { req.Code.Files[0].Path = "../solution.cpp" }},
		{"unsupported extension", func(req *contracts.RunRequest) { req.Code.Files[0].Path = "solution.txt" }},
		{"test files", func(req *contracts.RunRequest) { req.Tests.Files = req.Code.Files }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := sampleRequest()
			tc.change(&req)
			if _, err := NewAdapter().BuildCommand(t.Context(), req, cppLanguage(t)); err == nil {
				t.Error("invalid request reached the adapter")
			}
		})
	}
}

func cppLanguage(t *testing.T) languages.ResolvedLanguage {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == AdapterName {
			if language.Enabled || len(language.Versions) != 1 {
				t.Fatal("C++ must stay opt-in")
			}
			return languages.ResolvedLanguage{Version: language.Versions[0]}
		}
	}
	t.Fatal("C++ manifest is missing")
	return languages.ResolvedLanguage{}
}

func sampleRequest() contracts.RunRequest {
	return contracts.RunRequest{
		Language:  contracts.LanguageSpec{ID: AdapterName, Version: "17"},
		Code:      contracts.CodeSpec{Files: []contracts.SourceFile{{Path: "solution.cpp", ContentB64: "YQ=="}}},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 64, PIDs: 8},
	}
}
