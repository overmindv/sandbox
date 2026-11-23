package app_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/native"
)

func TestLiveWorkerExecutesCPP(t *testing.T) {
	runLiveNativeWorker(t, native.CPPName, "solution.cpp", `#include <cstdio>
int main() {
    int n;
    if (std::scanf("%d", &n) == 1) std::printf("%d\n", n + 3);
}`)
}

func TestLiveWorkerExecutesC17(t *testing.T) {
	runLiveNativeWorker(t, native.CName, "solution.c", `#include <stdio.h>
int main(void) {
    int n;
    if (scanf("%d", &n) == 1) printf("%d\n", n + 3);
}`)
}

func runLiveNativeWorker(t *testing.T, id, filename, source string) {
	t.Helper()

	if os.Getenv("SANDBOX_GOJUDGE_CPP_INTEGRATION") != "1" {
		t.Skip("requires an isolated compiler and runtime")
	}
	cfg := config.Default()
	cfg.Executor.Type = "gojudge"
	cfg.Executor.GoJudge.BaseURL = os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	cfg.Executor.GoJudge.CompilerURL = os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	cfg.Config.LanguagesFile = enabledNativeManifest(t, id)
	cfg.FeatureFlags.GoJudgeExecutor = true
	cfg.FeatureFlags.FakeExecutor = false
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false

	req := validPipelineExecutionRunRequest()
	req.Language = contracts.LanguageSpec{ID: id, Version: "17"}
	req.Code.Entrypoint = filename
	req.Code.Files = []contracts.SourceFile{{
		Path:       filename,
		ContentB64: base64.StdEncoding.EncodeToString([]byte(source)),
	}}
	req.Limits.WorkspaceBytes = 5 << 20
	req.Metadata = map[string]any{"attempt_deadline": time.Now().Add(40 * time.Second).UTC().Format(time.RFC3339Nano)}
	result := runCompiledWorkerFixture(t, cfg, req)
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected result", result.Status, result.Error)
	}
	if result.Cases[0].Stdout != "4\n" || result.Cases[1].Stdout != "5\n" {
		t.Error("case outputs did not survive the worker pipeline")
	}
}

func enabledNativeManifest(t *testing.T, id string) string {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Languages {
		if manifest.Languages[i].ID == id {
			manifest.Languages[i].Enabled = true
		}
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "languages.yaml")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}
