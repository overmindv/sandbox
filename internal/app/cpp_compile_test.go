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
	"overmindv/sandbox/internal/languages/cpp"
)

func TestLiveWorkerExecutesCPP(t *testing.T) {
	if os.Getenv("SANDBOX_GOJUDGE_CPP_INTEGRATION") != "1" {
		t.Skip("requires an isolated C++ compiler and runtime")
	}
	cfg := config.Default()
	cfg.Executor.Type = "gojudge"
	cfg.Executor.GoJudge.BaseURL = os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	cfg.Executor.GoJudge.CompilerURL = os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	cfg.Config.LanguagesFile = enabledCPPManifest(t)
	cfg.FeatureFlags.GoJudgeExecutor = true
	cfg.FeatureFlags.FakeExecutor = false
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false

	req := validPipelineExecutionRunRequest()
	req.Language = contracts.LanguageSpec{ID: cpp.AdapterName, Version: "17"}
	req.Code.Entrypoint = "solution.cpp"
	req.Code.Files = []contracts.SourceFile{{
		Path: "solution.cpp",
		ContentB64: base64.StdEncoding.EncodeToString([]byte(`#include <cstdio>
int main() {
    int n;
    if (std::scanf("%d", &n) == 1) std::printf("%d\n", n + 3);
}`)),
	}}
	req.Limits.WorkspaceBytes = 5 << 20
	req.Metadata = map[string]any{"attempt_deadline": time.Now().Add(40 * time.Second).UTC().Format(time.RFC3339Nano)}
	result := runCompiledWorkerFixture(t, cfg, req)
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected C++ result", result.Status, result.Error)
	}
	if result.Cases[0].Stdout != "4\n" || result.Cases[1].Stdout != "5\n" {
		t.Error("C++ case outputs did not survive the worker pipeline")
	}
}

func enabledCPPManifest(t *testing.T) string {
	t.Helper()

	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Languages {
		if manifest.Languages[i].ID == cpp.AdapterName {
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
