//go:build integration

package app_test

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"gopkg.in/yaml.v3"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/storage"
)

func TestKafkaWorkerExecutesJavaScript(t *testing.T) {
	t.Parallel()

	judge := rolloutEndpoint(t, "SANDBOX_TEST_JAVASCRIPT_GOJUDGE_URL")
	broker := integrationKafkaBroker(t, "PLAINTEXT")
	host, _, err := net.SplitHostPort(broker)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("isolated loopback Kafka broker required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	id := "javascript-" + storage.StableUUID(time.Now().String())
	cfg := drainConfig()
	cfg.Config.LanguagesFile = javascriptManifest(t)
	cfg.App.WorkerID = id
	cfg.Kafka.Brokers = []string{broker}
	cfg.Kafka.GroupID = id
	cfg.Kafka.Consumer.AutoOffsetReset = "earliest"
	cfg.Kafka.Topics = config.KafkaTopics{
		Requests: id + ".requests", Retry: id + ".retry", Events: id + ".events",
		Results: id + ".results", DeadLetter: id + ".deadletter",
	}
	cfg.Executor.Type = "gojudge"
	cfg.Executor.GoJudge.BaseURL = judge.String()
	cfg.FeatureFlags.GoJudgeExecutor = true
	cfg.FeatureFlags.FakeExecutor = false
	cfg.Storage.Artifacts.Enabled = false
	createDrainTopics(t, ctx, broker, cfg.Kafka.Topics)

	worker, err := app.NewWorker(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := worker.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	})
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	input := &kafkago.Writer{Addr: kafkago.TCP(broker), RequiredAcks: kafkago.RequireAll}
	output := kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{broker}, Topic: cfg.Kafka.Topics.Results})
	defer closeKafkaFixture(t, input, output)

	req := validPipelineExecutionRunRequest()
	req.SubmissionID = storage.StableUUID(id, "submission")
	req.AttemptID = storage.StableUUID(id, "attempt")
	req.Language = contracts.LanguageSpec{ID: "javascript", Version: "24"}
	req.Code = contracts.CodeSpec{
		Entrypoint: "solution.mjs",
		Files: []contracts.SourceFile{{Path: "solution.mjs", ContentB64: base64.StdEncoding.EncodeToString([]byte(
			"import {readFileSync} from 'node:fs';\nconsole.log(process.version, Number(readFileSync(0, 'utf8')) + 1);\n",
		))}},
	}
	value, err := kafka.EncodeRunRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.WriteMessages(ctx, kafkago.Message{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: value}); err != nil {
		t.Fatal(err)
	}
	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	result := readDrainResult(t, ctx, output, req.AttemptID)
	if result.Status != contracts.StatusOK || result.Language != req.Language || len(result.Cases) != 2 {
		t.Fatal("unexpected JavaScript result", result.Status, result.Error, result.Cases)
	}
	for index, want := range []string{"v24.22.3 2", "v24.22.3 3"} {
		if got := strings.TrimSpace(result.Cases[index].Stdout); got != want {
			t.Error("unexpected JavaScript output", got, "want", want)
		}
	}
}

func javascriptManifest(t *testing.T) string {
	t.Helper()
	manifest, err := languages.LoadManifest("configs/languages.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Languages {
		if manifest.Languages[i].ID == "javascript" {
			manifest.Languages[i].Enabled = true
		}
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "languages.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
