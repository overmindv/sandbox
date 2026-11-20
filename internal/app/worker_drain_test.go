package app_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/kafka"
)

func TestWorkerDrain(t *testing.T) {
	for _, mode := range []string{"complete", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			cfg := drainConfig()
			if mode == "timeout" {
				cfg.App.ShutdownTimeoutMS = 50
			}
			req := validPipelineExecutionRunRequest()
			payload, err := kafka.EncodeRunRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			msg := kafka.Message{Topic: cfg.Kafka.Topics.Requests, Key: []byte(req.SubmissionID), Value: payload}
			queue := &pipelineConsumer{messages: []kafka.Message{msg, msg}}
			consumer := &drainConsumer{Consumer: queue, closed: make(chan struct{})}
			producer := &pipelineProducer{}
			executor := newDrainExecutor()
			w, err := app.NewWorkerWithDeps(cfg, nil, app.WorkerDeps{
				Consumer: consumer, Producer: producer, Executor: executor,
			})
			if err != nil {
				t.Fatal(err)
			}
			stop, done := startDrainWorker(t, w)
			waitDrainSignal(t, executor.started)
			stop()
			if mode == "complete" {
				waitNotReady(t, w)
				close(executor.release)
			}
			err = waitDrainResult(t, done)
			waitDrainSignal(t, consumer.closed)
			if len(queue.messages) != 1 {
				t.Error("worker fetched another job during shutdown")
			}
			if mode == "timeout" {
				waitDrainSignal(t, executor.canceled)
				if !errors.Is(err, context.DeadlineExceeded) || len(queue.committed) != 0 || len(producer.Messages()) != 0 {
					t.Error("timed-out work was not canceled without a result or commit", err)
				}
				return
			}
			if err != nil || len(queue.committed) != 1 || len(producer.Messages()) != 1 {
				t.Fatal("worker did not publish and commit its current job", err)
			}
			result, err := kafka.DecodeRunResult(producer.Messages()[0].Value)
			if err != nil || result.AttemptID != req.AttemptID || result.Status != contracts.StatusPassed {
				t.Error("unexpected drained result", result.Status, err)
			}
		})
	}
}

func TestWorkerDrainWhileIdle(t *testing.T) {
	cfg := drainConfig()
	idle := &pollingConsumer{polling: make(chan struct{})}
	consumer := &drainConsumer{Consumer: idle, closed: make(chan struct{})}
	w, err := app.NewWorkerWithDeps(cfg, nil, app.WorkerDeps{Consumer: consumer, Producer: &pipelineProducer{}})
	if err != nil {
		t.Fatal(err)
	}
	stop, done := startDrainWorker(t, w)
	waitDrainSignal(t, idle.polling)
	stop()
	if err := waitDrainResult(t, done); err != nil {
		t.Error("idle worker did not stop", err)
	}
	waitDrainSignal(t, consumer.closed)
}

type pollingConsumer struct {
	pipelineConsumer
	polling chan struct{}
}

func (c *pollingConsumer) FetchMessage(ctx context.Context) (kafka.Message, error) {
	close(c.polling)
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

type drainConsumer struct {
	kafka.Consumer
	closed chan struct{}
}

func (c *drainConsumer) Close() error {
	defer close(c.closed)
	return c.Consumer.Close()
}

type drainExecutor struct {
	countingExecutor
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func newDrainExecutor() *drainExecutor {
	return &drainExecutor{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
}

func (e *drainExecutor) Execute(ctx context.Context, cmd execution.ExecutionCommand) (execution.ExecutionResult, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		close(e.canceled)
		return execution.ExecutionResult{}, ctx.Err()
	case <-e.release:
		return e.countingExecutor.Execute(ctx, cmd)
	}
}

func drainConfig() config.Config {
	cfg := config.Default()
	cfg.Server.HealthAddr = "127.0.0.1:0"
	cfg.Observability.MetricsEnabled = false
	cfg.App.ShutdownTimeoutMS = 3000
	return cfg
}

func startDrainWorker(t *testing.T, w *app.Worker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return cancel, done
}

func waitDrainSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(8 * time.Second):
		t.Fatal("worker did not reach the expected state")
	}
}

func waitDrainResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("worker did not stop")
		return nil
	}
}

func waitNotReady(t *testing.T, w *app.Worker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+w.HealthAddr()+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusServiceUnavailable {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker stayed ready while draining")
		case <-ticker.C:
		}
	}
}
