package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

func TestSlotPoolEnforcesGlobalAndLanguageLimits(t *testing.T) {
	pool, err := NewSlotPool(2, map[string]int{"python": 1})
	if err != nil {
		t.Errorf("new slot pool: %v", err)
	}

	pythonSlot, ok := pool.TryAcquire("python")
	if !ok {
		t.Error("expected first python slot")
	}
	if _, ok := pool.TryAcquire("python"); ok {
		t.Error("expected second python slot to be rejected by per-language limit")
	}

	goSlot, ok := pool.TryAcquire("go")
	if !ok {
		t.Error("expected go slot")
	}
	if _, ok := pool.TryAcquire("javascript"); ok {
		t.Error("expected global limit to reject third slot")
	}

	pythonSlot.Release()
	if _, ok := pool.TryAcquire("python"); !ok {
		t.Error("expected python slot after release")
	}
	goSlot.Release()
}

func TestAdmissionRejectsUnhealthyExecutorAndOldJobs(t *testing.T) {
	now := fixedSchedulerTime()
	admission := AdmissionController{
		RejectWhenExecutorUnhealthy: true,
		MaxQueueAge:                 time.Minute,
		ExecutorHealth:              ExecutorHealthFunc(func() bool { return false }),
		Now:                         func() time.Time { return now },
	}

	job := validSchedulerJob(now)
	if err := admission.Check(job); !errors.Is(err, ErrExecutorUnhealthy) {
		t.Errorf("expected executor unhealthy, got %v", err)
	}

	admission.ExecutorHealth = ExecutorHealthFunc(func() bool { return true })
	job.CreatedAt = now.Add(-2 * time.Minute)
	if err := admission.Check(job); !errors.Is(err, ErrJobTooOld) {
		t.Errorf("expected job too old, got %v", err)
	}
}

func TestDispatcherProcessesJobsAndTracksMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var handled atomic.Int64
	dispatcher := newTestDispatcher(t, schedulerTestConfig(), JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		handled.Add(1)
		return "ok", nil
	}))
	startDispatcher(t, ctx, dispatcher)

	result, err := dispatcher.DispatchAndWait(ctx, validSchedulerJob(fixedSchedulerTime()))
	if err != nil {
		t.Errorf("dispatch and wait: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("expected completed result, got %q", result.Status)
	}
	if handled.Load() != 1 {
		t.Errorf("expected handler once, got %d", handled.Load())
	}
	if result.Output != "ok" {
		t.Errorf("expected handler output, got %#v", result.Output)
	}

	metrics := dispatcher.Metrics()
	if metrics.Submitted["python"] != 1 {
		t.Errorf("expected submitted python=1, got %#v", metrics.Submitted)
	}
	if metrics.Started["python"] != 1 {
		t.Errorf("expected started python=1, got %#v", metrics.Started)
	}
	if metrics.Completed["completed"] != 1 {
		t.Errorf("expected completed=1, got %#v", metrics.Completed)
	}
	if metrics.QueueDepth != 0 {
		t.Errorf("expected empty queue, got %d", metrics.QueueDepth)
	}
}

func TestDispatcherEnforcesQueueFullPolicy(t *testing.T) {
	cfg := schedulerTestConfig()
	cfg.MaxInFlight = 1
	cfg.MaxInFlightPerLanguage = map[string]int{"python": 1}
	cfg.Queue.MaxDepth = 1
	cfg.Queue.EnqueueTimeoutMS = 0

	started := make(chan struct{}, 1)
	block := make(chan struct{})
	dispatcher := newTestDispatcher(t, cfg, JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDispatcher(t, ctx, dispatcher)

	if _, err := dispatcher.Submit(ctx, Job{ID: "job-1", AttemptID: "attempt-1", LanguageID: "python", CreatedAt: fixedSchedulerTime()}); err != nil {
		t.Errorf("submit job-1: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Error("expected job-1 to start")
	}
	if _, err := dispatcher.Submit(ctx, Job{ID: "job-2", AttemptID: "attempt-2", LanguageID: "python", CreatedAt: fixedSchedulerTime()}); err != nil {
		t.Errorf("submit job-2: %v", err)
	}
	if _, err := dispatcher.Submit(ctx, Job{ID: "job-3", AttemptID: "attempt-3", LanguageID: "python", CreatedAt: fixedSchedulerTime()}); !errors.Is(err, ErrQueueFull) {
		t.Errorf("expected queue full, got %v", err)
	}

	close(block)
}

func TestDispatcherAppliesJobTimeout(t *testing.T) {
	cfg := schedulerTestConfig()
	cfg.JobTimeoutMS = 20

	dispatcher := newTestDispatcher(t, cfg, JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDispatcher(t, ctx, dispatcher)

	result, err := dispatcher.DispatchAndWait(ctx, validSchedulerJob(fixedSchedulerTime()))
	if !errors.Is(err, ErrJobTimeout) {
		t.Errorf("expected job timeout, got result=%#v err=%v", result, err)
	}
	if result.Status != "timeout" {
		t.Errorf("expected timeout status, got %q", result.Status)
	}

	metrics := dispatcher.Metrics()
	if metrics.TimedOut["python"] != 1 {
		t.Errorf("expected timed out python=1, got %#v", metrics.TimedOut)
	}
}

func TestDispatcherTimeoutReturnsWhenHandlerIgnoresContext(t *testing.T) {
	cfg := schedulerTestConfig()
	cfg.JobTimeoutMS = 20

	release := make(chan struct{})
	dispatcher := newTestDispatcher(t, cfg, JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		<-release
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDispatcher(t, ctx, dispatcher)

	start := time.Now()
	result, err := dispatcher.DispatchAndWait(ctx, validSchedulerJob(fixedSchedulerTime()))
	if !errors.Is(err, ErrJobTimeout) {
		close(release)
		t.Errorf("expected job timeout, got result=%#v err=%v", result, err)
	}
	close(release)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("expected timeout result quickly, took %s", elapsed)
	}
}

func TestDispatcherRespectsPerLanguageConcurrency(t *testing.T) {
	cfg := schedulerTestConfig()
	cfg.MaxInFlight = 2
	cfg.MaxInFlightPerLanguage = map[string]int{"python": 1}
	cfg.Queue.MaxDepth = 4

	started := make(chan string, 2)
	release := make(chan struct{})
	dispatcher := newTestDispatcher(t, cfg, JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		started <- job.ID
		<-release
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startDispatcher(t, ctx, dispatcher)

	first, err := dispatcher.Submit(ctx, Job{ID: "job-1", AttemptID: "attempt-1", LanguageID: "python", CreatedAt: fixedSchedulerTime()})
	if err != nil {
		t.Errorf("submit first: %v", err)
	}
	second, err := dispatcher.Submit(ctx, Job{ID: "job-2", AttemptID: "attempt-2", LanguageID: "python", CreatedAt: fixedSchedulerTime()})
	if err != nil {
		t.Errorf("submit second: %v", err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Error("expected first job to start")
	}
	select {
	case jobID := <-started:
		t.Errorf("expected second python job to wait, but %s started", jobID)
	case <-time.After(30 * time.Millisecond):
	}

	close(release)
	waitReceipt(t, first)
	waitReceipt(t, second)
}

func newTestDispatcher(t *testing.T, cfg config.SchedulerConfig, handler JobHandler) *Dispatcher {
	t.Helper()
	metrics := observability.NewSchedulerMetrics()
	logger := observability.NewLogger("error", "json", nil)
	dispatcher, err := NewDispatcher(cfg, handler, metrics, logger, ExecutorHealthFunc(func() bool { return true }))
	if err != nil {
		t.Errorf("new dispatcher: %v", err)
	}
	dispatcher.now = fixedSchedulerTime
	dispatcher.admission.Now = fixedSchedulerTime
	return dispatcher
}

func startDispatcher(t *testing.T, ctx context.Context, dispatcher *Dispatcher) {
	t.Helper()
	if err := dispatcher.Start(ctx); err != nil {
		t.Errorf("start dispatcher: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = dispatcher.Stop(shutdownCtx)
	})
}

func waitReceipt(t *testing.T, receipt JobReceipt) JobResult {
	t.Helper()
	select {
	case result := <-receipt.Result:
		if result.Error != nil {
			t.Errorf("job %s failed: %v", result.JobID, result.Error)
		}
		return result
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for job %s", receipt.JobID)
		return JobResult{}
	}
}

func schedulerTestConfig() config.SchedulerConfig {
	return config.SchedulerConfig{
		MaxInFlight:            2,
		JobTimeoutMS:           1000,
		MaxInFlightPerLanguage: map[string]int{"python": 2},
		Queue: config.SchedulerQueueConfig{
			Policy:           "bounded_fifo",
			MaxDepth:         8,
			EnqueueTimeoutMS: 10,
		},
		Admission: config.SchedulerAdmissionConf{
			RejectWhenExecutorUnhealthy: true,
			MaxQueueAgeMS:               60000,
		},
	}
}

func validSchedulerJob(createdAt time.Time) Job {
	return Job{
		ID:         "job-1",
		AttemptID:  "attempt-1",
		LanguageID: "python",
		CreatedAt:  createdAt,
	}
}

func fixedSchedulerTime() time.Time {
	return time.Date(2026, 8, 12, 19, 0, 0, 0, time.UTC)
}

func TestCanceledQueuedJobNeverStarts(t *testing.T) {
	cfg := schedulerTestConfig()
	cfg.MaxInFlight = 1
	started := make(chan struct{})
	release := make(chan struct{})
	var called atomic.Int64
	d := newTestDispatcher(t, cfg, JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		if called.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil, nil
	}))
	startDispatcher(t, context.Background(), d)
	first, err := d.Submit(context.Background(), validSchedulerJob(fixedSchedulerTime()))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	second, err := d.Submit(ctx, validSchedulerJob(fixedSchedulerTime()))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	close(release)
	<-first.Result
	result := <-second.Result
	if called.Load() != 1 || result.Error == nil {
		t.Fatalf("canceled job executed: calls=%d error=%v", called.Load(), result.Error)
	}
}

func TestRunningJobReceivesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	d := newTestDispatcher(t, schedulerTestConfig(), JobHandlerFunc(func(ctx context.Context, job Job) (any, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}))
	startDispatcher(t, context.Background(), d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	receipt, err := d.Submit(ctx, validSchedulerJob(fixedSchedulerTime()))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach executor")
	}
	<-receipt.Result
}
