package gojudge

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/sqlite"
)

func TestSQLiteCaseErrors(t *testing.T) {
	cases := []struct {
		name, adapter, code string
		exit                int
		status              contracts.ExecutionStatus
	}{
		{"fixture", sqlite.AdapterName, "checker_error", sqlite.FixtureExitCode, contracts.StatusInternalError},
		{"output", sqlite.AdapterName, "ole", sqlite.OutputExitCode, contracts.StatusOutputLimitExceeded},
		{"other adapter", "python", "runtime_error", sqlite.FixtureExitCode, contracts.StatusRuntimeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ExecutionResultFromResponseBatch(execution.ExecutionCommand{Adapter: tc.adapter}, []Result{
				{Status: StatusNonzeroExitStatus, ExitStatus: tc.exit},
			})
			if res.Status != tc.status || res.Error == nil || res.Error.Code != tc.code || res.Cases[0].Status != tc.status {
				t.Error("unexpected SQLite case mapping", res)
			}
		})
	}
}

func TestLiveGoJudgeSQLite(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" {
		t.Skip("set SANDBOX_GOJUDGE_INTEGRATION=1 to run live go-judge integration test")
	}
	executor, err := NewExecutor(liveGoJudgeConfig())
	if err != nil {
		t.Fatal(err)
	}
	fixture := `{"ordered":false,"tables":[{"name":"items","columns":[{"name":"id","type":"INTEGER"}],"rows":[[2],[1],[2]]}]}`
	for _, tc := range []struct {
		name, query, stdout string
		cpuMS, budget       int
		status              contracts.ExecutionStatus
	}{
		{"select", "SELECT id FROM items ORDER BY id DESC", "[[1],[2],[2]]", 1000, 2048, contracts.StatusOK},
		{"write rejected", "UPDATE items SET id = 0", "", 1000, 2048, contracts.StatusRuntimeError},
		{"output budget", "SELECT id FROM items", "", 1000, 4, contracts.StatusOutputLimitExceeded},
		{"cpu budget", "WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<10000000) SELECT sum(n) FROM seq", "",
			75, 2048, contracts.StatusTimeLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := contracts.RunRequest{
				AttemptID: "sqlite-live",
				Language:  contracts.LanguageSpec{ID: "sqlite", Version: "3.46"},
				Code: contracts.CodeSpec{Files: []contracts.SourceFile{{
					Path: "solution.sql", ContentB64: base64.StdEncoding.EncodeToString([]byte(tc.query)),
				}}},
				Execution: &contracts.ExecutionSpec{Mode: contracts.ExecutionMode, Inputs: []string{fixture}},
				Limits: contracts.Limits{
					CPUms: tc.cpuMS, Wallms: 2000, MemoryMB: 128, PIDs: 32, StdoutBytes: tc.budget, StderrBytes: 1024,
				},
			}
			cmd, err := (sqlite.Adapter{}).BuildCommand(t.Context(), req, languages.ResolvedLanguage{
				Version: config.LanguageVersionConfig{FilePolicy: config.LanguageFilePolicy{DefaultEntrypoint: "solution.sql"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := executor.Execute(ctx, cmd)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Cases) != 1 || result.Status != tc.status || result.Cases[0].Stdout != tc.stdout {
				t.Error("unexpected live SQLite result", result)
			}
		})
	}
}
