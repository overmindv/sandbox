package app_test

import (
	"encoding/base64"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

func TestLiveWorkerExecutesJava(t *testing.T) {
	runLiveJVMWorker(t, "SANDBOX_GOJUDGE_JAVA_INTEGRATION", contracts.LanguageSpec{ID: "java", Version: "21"}, "Main.java", `import java.util.Scanner;
public class Main {
    public static void main(String[] args) {
        System.out.println(new Scanner(System.in).nextInt() * 2);
    }
}`)
}

func TestLiveWorkerExecutesKotlin(t *testing.T) {
	runLiveJVMWorker(t, "SANDBOX_GOJUDGE_KOTLIN_INTEGRATION", contracts.LanguageSpec{ID: "kotlin", Version: "2.4"}, "Main.kt", `fun main() {
    println(readln().toInt() * 2)
}`)
}

func runLiveJVMWorker(t *testing.T, enabled string, language contracts.LanguageSpec, entry, source string) {
	t.Helper()
	if os.Getenv(enabled) != "1" {
		t.Skip("requires isolated JVM compiler and runner")
	}
	cfg := config.Default()
	cfg.Executor.Type = "gojudge"
	cfg.Executor.GoJudge.BaseURL = os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	cfg.Executor.GoJudge.CompilerURL = os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	cfg.Config.LanguagesFile = enabledCompiledManifest(t, language.ID)
	cfg.FeatureFlags.GoJudgeExecutor = true
	cfg.FeatureFlags.FakeExecutor = false
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false

	req := validPipelineExecutionRunRequest()
	req.Language = language
	req.Code.Entrypoint = entry
	req.Code.Files = []contracts.SourceFile{{
		Path:       entry,
		ContentB64: base64.StdEncoding.EncodeToString([]byte(source)),
	}}
	req.Limits.MemoryMB = 96
	req.Limits.PIDs = 16
	req.Metadata = map[string]any{"attempt_deadline": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	result := runCompiledWorkerFixture(t, cfg, req)
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected JVM worker result", language.ID, result.Status, result.Error)
	}
	if result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
		t.Error("incorrect case outputs")
	}
}
