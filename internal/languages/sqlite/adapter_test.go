package sqlite

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
)

func request() contracts.RunRequest {
	return contracts.RunRequest{
		Language: contracts.LanguageSpec{ID: "sqlite", Version: "3.46"},
		Code: contracts.CodeSpec{Files: []contracts.SourceFile{
			{Path: "solution.sql", ContentB64: base64.StdEncoding.EncodeToString([]byte("SELECT 1"))},
		}},
		Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{`{"ordered":false,"tables":[]}`}},
		Limits:    contracts.Limits{CPUms: 1000, Wallms: 2000, MemoryMB: 128, PIDs: 32, StdoutBytes: 2048, StderrBytes: 512},
	}
}

func resolved() languages.ResolvedLanguage {
	return languages.ResolvedLanguage{Version: config.LanguageVersionConfig{
		RuntimeImage: "overmindv/sandbox-gojudge:local",
		FilePolicy:   config.LanguageFilePolicy{DefaultEntrypoint: "solution.sql"},
	}}
}

func TestBuildCommand(t *testing.T) {
	req := request()
	cmd, err := (Adapter{}).BuildCommand(t.Context(), req, resolved())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"python3", "-I", "./_sandbox_sqlite.py", "./solution.sql", "2048"}
	if !slices.Equal(cmd.Argv, want) || !slices.Equal(cmd.Inputs, req.Execution.Inputs) ||
		cmd.RuntimeLimits != execution.RuntimeLimitsFromRequest(req.Limits) || len(cmd.CopyOut) != 0 {
		t.Error("SQLite command changed the execution contract")
	}
	if len(cmd.Files) != 2 || cmd.Files[1].Path != harnessPath ||
		cmd.Files[1].Kind != execution.FileKindBuild || string(cmd.Files[1].Content) != runner {
		t.Error("trusted runner missing from workspace")
	}
}

func TestRejectsInvalidCommand(t *testing.T) {
	for name, change := range map[string]func(*contracts.RunRequest){
		"missing tests":    func(r *contracts.RunRequest) { r.Execution.Inputs = nil },
		"harness path":     func(r *contracts.RunRequest) { r.Code.Files[0].Path = harnessPath },
		"invalid path":     func(r *contracts.RunRequest) { r.Code.Entrypoint = "../solution.sql" },
		"extra file":       func(r *contracts.RunRequest) { r.Code.Files = append(r.Code.Files, r.Code.Files[0]) },
		"invalid UTF-8":    func(r *contracts.RunRequest) { r.Code.Files[0].ContentB64 = "/w==" },
		"large fixture":    func(r *contracts.RunRequest) { r.Execution.Inputs[0] = strings.Repeat("x", maxFixtureBytes+1) },
		"no output budget": func(r *contracts.RunRequest) { r.Limits.StdoutBytes = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			req := request()
			change(&req)
			if _, err := (Adapter{}).BuildCommand(t.Context(), req, resolved()); !errors.Is(err, execution.ErrInvalidCommandPayload) {
				t.Error("expected invalid SQLite command, got", err)
			}
		})
	}
}

func BenchmarkBuildCommand(b *testing.B) {
	req := request()
	runtime := resolved()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (Adapter{}).BuildCommand(b.Context(), req, runtime); err != nil {
			b.Fatal(err)
		}
	}
}
