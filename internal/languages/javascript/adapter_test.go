package javascript

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"testing"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

func TestBuildsExecutionCommand(t *testing.T) {
	t.Parallel()

	req := executionRequest()
	req.Code.Entrypoint = "src/-entry.mjs"
	req.Code.Files[0].Path = req.Code.Entrypoint
	req.Code.Files = append(req.Code.Files, contracts.SourceFile{Path: "src/data.json", ContentB64: "e30="})
	req.Metadata = map[string]any{"NODE_OPTIONS": "ignored"}
	resolved := resolvedJavaScript(t)
	command, err := NewAdapter().BuildCommand(t.Context(), req, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Argv, []string{"/usr/local/bin/node", "--", "./src/-entry.mjs"}) {
		t.Error("entrypoint was not passed as a script", command.Argv)
	}
	if command.Adapter != AdapterName || command.RuntimeImage != resolved.Version.RuntimeImage || command.WorkingDir != "/workspace" {
		t.Error("runtime selection was not retained")
	}
	if command.Mode != contracts.ExecutionMode || !slices.Equal(command.Inputs, req.Execution.Inputs) ||
		len(command.Files) != 2 || string(command.Files[1].Content) != "{}" {
		t.Error("workspace or inputs were not preserved")
	}
	if command.RuntimeLimits != execution.RuntimeLimitsFromRequest(req.Limits) {
		t.Error("request limits were not preserved")
	}
	if len(command.Environment) != 0 || len(command.CopyOut) != 0 || len(command.CompileArgv) != 0 {
		t.Error("execution must not add environment variables, artifact paths or a build step")
	}
}

func TestUsesManifestDefaultEntrypoint(t *testing.T) {
	t.Parallel()

	cmd, err := NewAdapter().BuildCommand(t.Context(), executionRequest(), resolvedJavaScript(t))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Entrypoint != "solution.js" {
		t.Error("unexpected default entrypoint", cmd.Entrypoint)
	}
}

func TestRejectsInvalidWorkspaces(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*contracts.RunRequest, *languages.ResolvedLanguage)
	}{
		{"missing script", func(r *contracts.RunRequest, _ *languages.ResolvedLanguage) { r.Code.Entrypoint = "other.js" }},
		{"non-script entry", func(r *contracts.RunRequest, _ *languages.ResolvedLanguage) {
			r.Code.Files[0].Path = "data.json"
			r.Code.Entrypoint = "data.json"
		}},
		{"file extension", func(r *contracts.RunRequest, _ *languages.ResolvedLanguage) {
			r.Code.Files = append(r.Code.Files, contracts.SourceFile{Path: "notes.txt", ContentB64: "YQ=="})
		}},
		{"too many files", func(r *contracts.RunRequest, resolved *languages.ResolvedLanguage) {
			resolved.Version.FilePolicy.MaxFiles = 1
			r.Code.Files = append(r.Code.Files, contracts.SourceFile{Path: "data.json", ContentB64: "e30="})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req, resolved := executionRequest(), resolvedJavaScript(t)
			test.change(&req, &resolved)
			_, err := NewAdapter().BuildCommand(t.Context(), req, resolved)
			if !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("expected invalid workspace", err)
			}
		})
	}
}

func TestUnavailableByDefaultAndWithoutExecutionMode(t *testing.T) {
	t.Parallel()

	req := executionRequest()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == AdapterName && language.Enabled {
			t.Fatal("JavaScript must stay opt-in")
		}
	}
	req.Execution = nil
	_, err = NewAdapter().BuildCommand(t.Context(), req, resolvedJavaScript(t))
	if !errors.Is(err, languages.ErrUnsupportedLanguage) {
		t.Error("expected unsupported mode", err)
	}
}

func BenchmarkBuildCommand(b *testing.B) {
	req := executionRequest()
	for index := 1; index < 32; index++ {
		req.Code.Files = append(req.Code.Files, contracts.SourceFile{
			Path: fmt.Sprintf("module%d.js", index), ContentB64: req.Code.Files[0].ContentB64,
		})
	}
	resolved := resolvedJavaScript(b)
	adapter := NewAdapter()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := adapter.BuildCommand(b.Context(), req, resolved); err != nil {
			b.Fatal(err)
		}
	}
}

func executionRequest() contracts.RunRequest {
	return contracts.RunRequest{
		Language: contracts.LanguageSpec{ID: AdapterName, Version: "24"},
		Code: contracts.CodeSpec{Files: []contracts.SourceFile{
			{Path: "solution.js", ContentB64: base64.StdEncoding.EncodeToString([]byte("console.log(1)\n"))},
		}},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{"1", "2"}},
		Limits:    contracts.Limits{CPUms: 1000, Wallms: 1500, MemoryMB: 128, PIDs: 32},
	}
}

func resolvedJavaScript(t testing.TB) languages.ResolvedLanguage {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range manifest.Languages {
		if language.ID == AdapterName {
			return languages.ResolvedLanguage{Language: language, Version: language.Versions[0]}
		}
	}
	t.Fatal("JavaScript is missing from the manifest")
	return languages.ResolvedLanguage{}
}
