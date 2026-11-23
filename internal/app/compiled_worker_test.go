package app_test

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/kafka"
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

func TestLiveWorkerExecutesSwift(t *testing.T) {
	runLiveCompiledWorker(t, "SANDBOX_GOJUDGE_SWIFT_INTEGRATION", contracts.LanguageSpec{ID: "swift", Version: "6.4"}, 64, "main.swift",
		"if let line = readLine(), let n = Int(line) { print(n * 2) }")
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
	var result contracts.RunResult
	if broker := os.Getenv("SANDBOX_SWIFT_TEST_KAFKA_BROKER"); language.ID == "swift" && broker != "" {
		result = runSwiftKafkaFixture(t, cfg, req, broker)
	} else {
		result = runCompiledWorkerFixture(t, cfg, req)
	}
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected worker result", language.ID, result.Status, result.Error)
	}
	if result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
		t.Error("incorrect case outputs")
	}
}

func runSwiftKafkaFixture(t *testing.T, cfg config.Config, req contracts.RunRequest, broker string) contracts.RunResult {
	t.Helper()

	ctx, stop := context.WithTimeout(t.Context(), 45*time.Second)
	defer stop()

	cfg.Kafka.Brokers = []string{broker}
	cfg.Kafka.GroupID = "swift-live"
	cfg.Kafka.Consumer.AutoOffsetReset = "earliest"
	cfg.Kafka.Topics = config.KafkaTopics{
		Requests: "swift.requests", Retry: "swift.retry", Events: "swift.events",
		Results: "swift.results", DeadLetter: "swift.deadletter",
	}
	topicNames := []string{cfg.Kafka.Topics.Requests, cfg.Kafka.Topics.Retry, cfg.Kafka.Topics.Events,
		cfg.Kafka.Topics.Results, cfg.Kafka.Topics.DeadLetter}
	topics := make([]kafkago.TopicConfig, 0, len(topicNames))
	for _, name := range topicNames {
		topics = append(topics, kafkago.TopicConfig{Topic: name, NumPartitions: 1, ReplicationFactor: 1})
	}
	conn, err := kafkago.DialContext(ctx, "tcp", broker)
	if err != nil {
		t.Fatal(err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
	}
	createErr := conn.CreateTopics(topics...)
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
	if createErr != nil {
		t.Fatal(createErr)
	}
	worker, err := app.NewWorker(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := worker.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	}()
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	input := &kafkago.Writer{Addr: kafkago.TCP(broker), RequiredAcks: kafkago.RequireAll}
	output := kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{broker}, Topic: cfg.Kafka.Topics.Results})
	defer func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		if err := output.Close(); err != nil {
			t.Error(err)
		}
	}()
	payload, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.WriteMessages(ctx, kafkago.Message{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: payload}); err != nil {
		t.Fatal(err)
	}
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	message, err := output.ReadMessage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := kafka.DecodeRunResult(message.Value)
	if err != nil || result.AttemptID != req.AttemptID {
		t.Fatal("unexpected Kafka result", result.AttemptID, err)
	}
	return result
}
