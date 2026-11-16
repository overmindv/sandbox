package gojudge

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/languages"
	golangadapter "overmindv/sandbox/internal/languages/go"
	"overmindv/sandbox/internal/languages/python"
)

func TestLiveMaliciousSolutionsCannotForgeGrading(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("isolated live gojudge required")
	}
	manifest, err := languages.LoadManifest("../../../configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := languages.NewRegistry(manifest, python.NewAdapter(), golangadapter.NewAdapter())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(config.GoJudgeClient{BaseURL: os.Getenv("SANDBOX_GOJUDGE_BASE_URL"), RequestTimeoutMS: 30000})
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"python", "go"} {
		for _, attack := range []string{"overwrite_harness", "fake_report", "exit_zero", "read_expected", "echo"} {
			t.Run(lang+"/"+attack, func(t *testing.T) {
				suffix, entry, version := ".py", "solution.py", "3.12"
				if lang == "go" {
					suffix = ".go.txt"
					entry = "main.go"
					version = "1.26"
				}
				source, err := os.ReadFile("../../../tests/fixtures/malicious/" + attack + suffix)
				if err != nil {
					t.Fatal(err)
				}
				req := contracts.RunRequest{AttemptID: "22222222-2222-4222-8222-222222222222", Language: contracts.LanguageSpec{ID: lang, Version: version},
					Code:      contracts.CodeSpec{Entrypoint: entry, Files: []contracts.SourceFile{{Path: entry, ContentB64: base64.StdEncoding.EncodeToString(source)}}},
					Execution: &contracts.ExecutionSpec{Mode: "execution", Inputs: []string{"probe\n"}},
					Limits:    contracts.Limits{CPUms: 10000, Wallms: 20000, MemoryMB: 512, PIDs: 64, StdoutBytes: 32768, StderrBytes: 32768, WorkspaceBytes: 10485760}}
				command, err := registry.BuildCommand(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if len(command.CopyOut) != 0 {
					t.Fatal("report channel exposed")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				result, err := executor.Execute(ctx, command)
				if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != 1 {
					t.Fatalf("runtime failed: %+v %v", result, err)
				}
				output := result.Cases[0].Stdout
				if attack == "echo" && output != "probe\n" {
					t.Fatalf("control failed: %q", output)
				}
				if output == "TRUSTED_EXPECTED_42\n" || result.TestsPassed != 0 || result.TestsTotal != 0 {
					t.Fatal("solution forged trusted verdict")
				}
				if attack == "read_expected" && output != "expected unavailable\n" {
					t.Fatalf("private data probe failed: %q", output)
				}
			})
		}
	}
}
