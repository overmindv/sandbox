package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

var (
	ErrDispatcherAlreadyStarted = errors.New("scheduler dispatcher already started")
	ErrDispatcherNotStarted     = errors.New("scheduler dispatcher is not started")
	ErrJobTimeout               = errors.New("scheduler job timed out")
)

type Job struct {
	ID         string
	AttemptID  string
	LanguageID string
	CreatedAt  time.Time
	Timeout    time.Duration
	Payload    any
}

type JobResult struct {
	JobID     string
	AttemptID string
	Status    string
	StartedAt time.Time
	EndedAt   time.Time
	QueueTime time.Duration
	RunTime   time.Duration
	Error     error
}

type JobReceipt struct {
	JobID     string
	AttemptID string
	Result    <-chan JobResult
}

type JobHandler interface {
	HandleJob(ctx context.Context, job Job) error
}

type JobHandlerFunc func(ctx context.Context, job Job) error

func (f JobHandlerFunc) HandleJob(ctx context.Context, job Job) error {
	return f(ctx, job)
}

type Dispatcher struct {
	slots       *SlotPool
	queue       *boundedQueue
	queuePolicy QueuePolicy
	admission   AdmissionController
	handler     JobHandler
	metrics     *observability.SchedulerMetrics
	logger      *observability.Logger
	jobTimeout  time.Duration
	workerCount int
	now         func() time.Time
	startedMu   sync.Mutex
	started     bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewDispatcher(cfg config.SchedulerConfig, handler JobHandler, metrics *observability.SchedulerMetrics, logger *observability.Logger, health ExecutorHealth) (*Dispatcher, error) {
	if handler == nil {
		return nil, errors.New("scheduler job handler is required")
	}

	slots, err := NewSlotPool(cfg.MaxInFlight, cfg.MaxInFlightPerLanguage)
	if err != nil {
		return nil, err
	}

	queuePolicy := QueuePolicyFromConfig(cfg.Queue)
	queue, err := newBoundedQueue(queuePolicy)
	if err != nil {
		return nil, err
	}

	if metrics == nil {
		metrics = observability.NewSchedulerMetrics()
	}
	if logger == nil {
		logger = observability.NewLogger("info", "json", nil)
	}

	jobTimeout := time.Duration(cfg.JobTimeoutMS) * time.Millisecond
	if jobTimeout <= 0 {
		return nil, errors.New("scheduler job timeout must be positive")
	}

	workerCount := cfg.MaxInFlight
	if workerCount <= 0 {
		return nil, errors.New("scheduler worker count must be positive")
	}

	dispatcher := &Dispatcher{
		slots:       slots,
		queue:       queue,
		queuePolicy: queuePolicy,
		admission:   AdmissionFromConfig(cfg.Admission, health),
		handler:     handler,
		metrics:     metrics,
		logger:      logger,
		jobTimeout:  jobTimeout,
		workerCount: workerCount,
		now:         func() time.Time { return time.Now().UTC() },
	}
	dispatcher.observeQueue()
	dispatcher.observeSlots()
	return dispatcher, nil
}

func (d *Dispatcher) Start(ctx context.Context) error {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	if d.started {
		return ErrDispatcherAlreadyStarted
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	d.started = true
	for i := 0; i < d.workerCount; i++ {
		d.wg.Add(1)
		go d.workerLoop(i)
	}
	d.logger.Info(ctx, "scheduler dispatcher started", "workers", d.workerCount, "queue_capacity", d.queue.Cap())
	return nil
}

func (d *Dispatcher) Stop(ctx context.Context) error {
	d.startedMu.Lock()
	if !d.started {
		d.startedMu.Unlock()
		return nil
	}
	d.started = false
	if d.cancel != nil {
		d.cancel()
	}
	d.queue.Close()
	d.startedMu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		d.logger.Info(ctx, "scheduler dispatcher stopped")
		return nil
	}
}

func (d *Dispatcher) Submit(ctx context.Context, job Job) (JobReceipt, error) {
	if err := d.ensureStarted(); err != nil {
		return JobReceipt{}, err
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = d.now()
	}
	if err := d.admission.Check(job); err != nil {
		d.metrics.IncRejected(errorKey(err))
		return JobReceipt{}, err
	}

	resultCh := make(chan JobResult, 1)
	item := queuedJob{job: job, resultCh: resultCh}
	if err := d.queue.Enqueue(ctx, item, d.queuePolicy.EnqueueTimeout); err != nil {
		d.metrics.IncRejected(errorKey(err))
		d.observeQueue()
		return JobReceipt{}, err
	}

	d.metrics.IncSubmitted(job.LanguageID)
	d.observeQueue()
	return JobReceipt{JobID: job.ID, AttemptID: job.AttemptID, Result: resultCh}, nil
}

func (d *Dispatcher) DispatchAndWait(ctx context.Context, job Job) (JobResult, error) {
	receipt, err := d.Submit(ctx, job)
	if err != nil {
		return JobResult{}, err
	}
	select {
	case <-ctx.Done():
		return JobResult{}, ctx.Err()
	case result := <-receipt.Result:
		return result, result.Error
	}
}

func (d *Dispatcher) Stats() SlotStats {
	return d.slots.Stats()
}

func (d *Dispatcher) QueueDepth() int {
	return d.queue.Len()
}

func (d *Dispatcher) Metrics() observability.SchedulerMetricsSnapshot {
	return d.metrics.Snapshot()
}

func (d *Dispatcher) workerLoop(workerIndex int) {
	defer d.wg.Done()
	for {
		item, err := d.queue.Dequeue(d.ctx)
		if err != nil {
			return
		}
		d.observeQueue()
		d.runQueuedJob(workerIndex, item)
	}
}

func (d *Dispatcher) runQueuedJob(workerIndex int, item queuedJob) {
	job := item.job
	startedAt := d.now()
	queueTime := startedAt.Sub(job.CreatedAt)

	acquireCtx, cancelAcquire := context.WithCancel(d.ctx)
	slot, err := d.slots.Acquire(acquireCtx, job.LanguageID)
	cancelAcquire()
	if err != nil {
		d.finish(item, JobResult{
			JobID:     job.ID,
			AttemptID: job.AttemptID,
			Status:    "canceled",
			StartedAt: startedAt,
			EndedAt:   d.now(),
			QueueTime: queueTime,
			Error:     err,
		})
		return
	}
	defer func() {
		slot.Release()
		d.observeSlots()
	}()

	d.observeSlots()
	d.metrics.IncStarted(job.LanguageID)

	timeout := d.jobTimeout
	if job.Timeout > 0 {
		timeout = job.Timeout
	}
	jobCtx, cancelJob := context.WithTimeout(d.ctx, timeout)
	defer cancelJob()

	d.logger.Debug(jobCtx, "scheduler job started", "worker_index", workerIndex, "job_id", job.ID, "attempt_id", job.AttemptID, "language", job.LanguageID)
	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- d.handler.HandleJob(jobCtx, job)
	}()

	select {
	case err = <-handlerErr:
	case <-jobCtx.Done():
		err = jobCtx.Err()
	}

	endedAt := d.now()
	runTime := endedAt.Sub(startedAt)
	status := "completed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
		err = ErrJobTimeout
		status = "timeout"
		d.metrics.IncTimedOut(job.LanguageID)
	}
	if err != nil && status != "timeout" {
		status = "failed"
	}
	d.metrics.IncCompleted(status)

	d.finish(item, JobResult{
		JobID:     job.ID,
		AttemptID: job.AttemptID,
		Status:    status,
		StartedAt: startedAt,
		EndedAt:   endedAt,
		QueueTime: queueTime,
		RunTime:   runTime,
		Error:     err,
	})
}

func (d *Dispatcher) finish(item queuedJob, result JobResult) {
	select {
	case item.resultCh <- result:
	default:
	}
	close(item.resultCh)
}

func (d *Dispatcher) ensureStarted() error {
	d.startedMu.Lock()
	defer d.startedMu.Unlock()
	if !d.started {
		return ErrDispatcherNotStarted
	}
	return nil
}

func (d *Dispatcher) observeQueue() {
	d.metrics.ObserveQueue(d.queue.Len(), d.queue.Cap())
}

func (d *Dispatcher) observeSlots() {
	stats := d.slots.Stats()
	d.metrics.ObserveSlots(stats.InFlight, stats.ByLanguage)
}

func errorKey(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrQueueFull):
		return "queue_full"
	case errors.Is(err, ErrQueueClosed):
		return "queue_closed"
	case errors.Is(err, ErrExecutorUnhealthy):
		return "executor_unhealthy"
	case errors.Is(err, ErrJobTooOld):
		return "job_too_old"
	case errors.Is(err, ErrInvalidJob):
		return "invalid_job"
	default:
		return fmt.Sprintf("%T", err)
	}
}
