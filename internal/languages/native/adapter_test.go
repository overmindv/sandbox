package native

import (
	"slices"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

const fixtureContent = "YQ=="

func TestBuildsCPPCommand(t *testing.T) {
	t.Parallel()

	req := sampleRequest(CPPName, "solution.cpp")
	req.Code.Entrypoint = "src/-main.cpp"
	req.Code.Files[0].Path = req.Code.Entrypoint
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "src/helper.cpp", ContentB64: fixtureContent},
		contracts.SourceFile{Path: "src/local.hpp", ContentB64: fixtureContent})
	req.Metadata = map[string]any{"CXXFLAGS": "-g"}
	command, err := CPP17().BuildCommand(t.Context(), req, nativeLanguage(t, CPPName))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/g++-14", "-std=c++17", "-O2", "-pipe", "-static", "-s", "-fdiagnostics-color=never",
		"-o", "_sandbox_cpp", "./src/-main.cpp", "./src/helper.cpp"}
	checkCommand(t, command, req, want, "./_sandbox_cpp")
}

func TestBuildsC17Command(t *testing.T) {
	t.Parallel()

	req := sampleRequest(CName, "-solution.c")
	req.Code.Entrypoint = "-solution.c"
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "helper.c", ContentB64: fixtureContent},
		contracts.SourceFile{Path: "helper.h", ContentB64: fixtureContent})
	req.Metadata = map[string]any{"CFLAGS": "-g"}
	command, err := C17().BuildCommand(t.Context(), req, nativeLanguage(t, CName))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/gcc-14", "-std=c17", "-O2", "-pipe", "-static", "-s", "-fdiagnostics-color=never",
		"-o", "_sandbox_c", "./-solution.c", "./helper.c", "-lm"}
	checkCommand(t, command, req, want, "./_sandbox_c")
}

func TestBuildsSwiftCommand(t *testing.T) {
	t.Parallel()

	req := sampleRequest(SwiftName, "src/main.swift")
	req.Language.Version = "6.4"
	req.Code.Entrypoint = "src/main.swift"
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "-helper.swift", ContentB64: fixtureContent})
	req.Metadata = map[string]any{"SWIFTFLAGS": "-v"}
	command, err := Swift().BuildCommand(t.Context(), req, nativeLanguage(t, SwiftName))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/swiftc", "-O", "-whole-module-optimization", "-j1", "-module-cache-path", "/tmp/sandbox-swift-cache", "-module-name", "SandboxSolution",
		"-o", "_sandbox_swift", "./src/main.swift", "./-helper.swift"}
	checkCommand(t, command, req, want, "./_sandbox_swift")
}

func TestSwiftRejectsNonSourceInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		file contracts.SourceFile
	}{
		{"package executable", contracts.SourceFile{Path: "helper.so", ContentB64: fixtureContent}},
		{"response file", contracts.SourceFile{Path: "options.rsp", ContentB64: fixtureContent}},
		{"invalid UTF-8", contracts.SourceFile{Path: "helper.swift", ContentB64: "/w=="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := sampleRequest(SwiftName, "main.swift")
			req.Code.Files = append(req.Code.Files, tc.file)
			if _, err := Swift().BuildCommand(t.Context(), req, nativeLanguage(t, SwiftName)); err == nil {
				t.Error("invalid Swift input reached the compiler")
			}
		})
	}
	req := sampleRequest(SwiftName, "entry.swift")
	req.Code.Entrypoint = "entry.swift"
	if _, err := Swift().BuildCommand(t.Context(), req, nativeLanguage(t, SwiftName)); err == nil {
		t.Error("invalid Swift entrypoint was accepted")
	}
}

func checkCommand(t *testing.T, command execution.ExecutionCommand, req contracts.RunRequest, want []string, run string) {
	t.Helper()

	if !slices.Equal(command.CompileArgv, want) || !slices.Equal(command.Argv, []string{run}) {
		t.Error("unexpected compiler or runtime arguments", command.CompileArgv, command.Argv)
	}
	if len(command.Environment) != 0 || len(command.CopyOut) != 0 ||
		command.RuntimeLimits != execution.RuntimeLimitsFromRequest(req.Limits) || !slices.Equal(command.Inputs, req.Execution.Inputs) {
		t.Error("unexpected execution controls", command)
	}
}

func TestRejectsInvalidNativeRequest(t *testing.T) {
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

			req := sampleRequest(CPPName, "solution.cpp")
			tc.change(&req)
			if _, err := CPP17().BuildCommand(t.Context(), req, nativeLanguage(t, CPPName)); err == nil {
				t.Error("invalid request reached the adapter")
			}
		})
	}
}

func TestC17RejectsCPPFiles(t *testing.T) {
	t.Parallel()

	req := sampleRequest(CName, "solution.c")
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "helper.cpp", ContentB64: fixtureContent})
	if _, err := C17().BuildCommand(t.Context(), req, nativeLanguage(t, CName)); err == nil {
		t.Error("C17 accepted a C++ source file")
	}
}

func nativeLanguage(t *testing.T, id string) languages.ResolvedLanguage {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == id {
			if language.Enabled || len(language.Versions) != 1 {
				t.Fatal("compiled language must stay opt-in", id)
			}
			return languages.ResolvedLanguage{Version: language.Versions[0]}
		}
	}
	t.Fatal("compiled language is missing from the manifest", id)
	return languages.ResolvedLanguage{}
}

func sampleRequest(id, path string) contracts.RunRequest {
	return contracts.RunRequest{
		Language:  contracts.LanguageSpec{ID: id, Version: "17"},
		Code:      contracts.CodeSpec{Files: []contracts.SourceFile{{Path: path, ContentB64: fixtureContent}}},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{CPUms: 500, Wallms: 1000, MemoryMB: 64, PIDs: 8},
	}
}
