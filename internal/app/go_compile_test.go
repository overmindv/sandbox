package app_test

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/kafka"
)

func TestLiveWorkerBuildsGoOutsideRunTimeout(t *testing.T) {
	compiler := os.Getenv("SANDBOX_GOJUDGE_COMPILER_URL")
	runner := os.Getenv("SANDBOX_GOJUDGE_BASE_URL")
	if os.Getenv("SANDBOX_GOJUDGE_INTEGRATION") != "1" || compiler == "" || runner == "" {
		t.Skip("requires a go-judge compiler and runner")
	}

	cfg := config.Default()
	cfg.Executor.Type = "gojudge"
	cfg.Executor.GoJudge.BaseURL = runner
	cfg.Executor.GoJudge.CompilerURL = compiler
	cfg.FeatureFlags.GoJudgeExecutor = true
	cfg.FeatureFlags.FakeExecutor = false
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false

	req := validPipelineExecutionRunRequest()
	req.Language = contracts.LanguageSpec{ID: "go", Version: "1.26"}
	req.Code.Entrypoint = "solution.go"
	req.Code.Files = []contracts.SourceFile{{
		Path: "solution.go",
		ContentB64: base64.StdEncoding.EncodeToString([]byte(
			"package main; import \"fmt\"; func main(){ var n int; fmt.Scan(&n); fmt.Println(n*2) }")),
	}}
	req.Metadata = map[string]any{"attempt_deadline": time.Now().Add(90 * time.Second).UTC().Format(time.RFC3339Nano)}
	payload, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &pipelineConsumer{messages: []kafka.Message{{
		Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: payload,
	}}}
	producer := &pipelineProducer{}
	worker, err := app.NewWorkerWithDeps(cfg, nil, app.WorkerDeps{Consumer: consumer, Producer: producer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := worker.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	}()
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	messages := producer.Messages()
	if len(consumer.committed) != 1 || len(messages) != 1 || messages[0].Topic != cfg.Kafka.Topics.Results {
		t.Fatal("worker did not publish a single result and commit")
	}
	result, err := kafka.DecodeRunResult(messages[0].Value)
	if err != nil || result.Status != contracts.StatusOK || len(result.Cases) != 2 {
		t.Fatal("unexpected Go result", result.Status, err, result.Error)
	}
	if result.Cases[0].Stdout != "2\n" || result.Cases[1].Stdout != "4\n" {
		t.Error("incorrect case outputs")
	}
}
