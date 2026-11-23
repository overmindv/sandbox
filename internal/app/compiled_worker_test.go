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
	runLiveCompiledWorker(t, "SANDBOX_GOJUDGE_JAVA_INTEGRATION", contracts.LanguageSpec{ID: "java", Version: "21"}, 96, "Main.java", `import java.util.Scanner;
public class Main {
    public static void main(String[] args) {
        System.out.println(new Scanner(System.in).nextInt() * 2);
    }
}`)
}

func TestLiveWorkerExecutesKotlin(t *testing.T) {
	runLiveCompiledWorker(t, "SANDBOX_GOJUDGE_KOTLIN_INTEGRATION", contracts.LanguageSpec{ID: "kotlin", Version: "2.4"}, 96, "Main.kt", `fun main() {
    println(readln().toInt() * 2)
}`)
}

func TestLiveWorkerExecutesTypeScript(t *testing.T) {
	runLiveCompiledWorker(t, "SANDBOX_GOJUDGE_TYPESCRIPT_INTEGRATION", contracts.LanguageSpec{ID: "typescript", Version: "7.0"}, 64, "main.ts", `import { readFileSync } from "node:fs";
const n: number = Number(readFileSync(0, "utf8"));
console.log(n * 2);`)
}

func TestLiveWorkerExecutesCSharp(t *testing.T) {
	runLiveCompiledWorker(t, "SANDBOX_GOJUDGE_CSHARP_INTEGRATION", contracts.LanguageSpec{ID: "csharp", Version: "10.0"}, 96, "Program.cs",
		"using System; Console.WriteLine(int.Parse(Console.ReadLine()!) * 2);")
}

func runLiveCompiledWorker(t *testing.T, enabled string, language contracts.LanguageSpec, memoryMB int, entry, source string) {
	t.Helper()
	if os.Getenv(enabled) != "1" {
		t.Skip("requires isolated compiler and runner")
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
	req.Limits.MemoryMB = memoryMB
	req.Limits.PIDs = 16
	req.Metadata = map[string]any{"attempt_deadline": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	result := runCompiledWorkerFixture(t, cfg, req)
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected worker result", language.ID, result.Status, result.Error)
	}
	if result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
		t.Error("incorrect case outputs")
	}
}
