package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"overmindv/sandbox/internal/artifacts"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/execution/fake"
	"overmindv/sandbox/internal/execution/gojudge"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/languages"
	"overmindv/sandbox/internal/languages/cpp"
	golangadapter "overmindv/sandbox/internal/languages/go"
	"overmindv/sandbox/internal/languages/javascript"
	pythonadapter "overmindv/sandbox/internal/languages/python"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
	"overmindv/sandbox/internal/security"
	"overmindv/sandbox/internal/storage"
	"overmindv/sandbox/internal/testsources"
)

type WorkerDeps struct {
	Consumer  kafka.Consumer
	Producer  kafka.Producer
	Executor  execution.Executor
	Languages *languages.Registry
	Metrics   *observability.SchedulerMetrics
	RunStore  storage.RunStore
	Artifacts *artifacts.Manager
	Tests     testsources.TestSourceProvider
}

type requestExecutor struct {
	executor execution.Executor
	metrics  *observability.SchedulerMetrics
}

func (h requestExecutor) HandleJob(ctx context.Context, job scheduler.Job) (any, error) {
	command, ok := job.Payload.(execution.ExecutionCommand)
	if !ok {
		return execution.ExecutionResult{}, execution.ErrInvalidCommandPayload
	}
	startedAt := time.Now()
	result, err := h.executor.Execute(ctx, command)
	if h.metrics != nil {
		h.metrics.ObserveExecutorLatency(command.Language.ID, time.Since(startedAt))
	}
	return result, err
}

type requestPipeline struct {
	legacyHarness  bool
	buildAllowance func(execution.ExecutionCommand) time.Duration
	dispatcher     *scheduler.Dispatcher
	producer       kafka.Producer
	topics         kafka.TopicNames
	languages      *languages.Registry
	workerID       string
	security       security.Policy
	store          storage.RunStore
	artifacts      *artifacts.Manager
	tests          testsources.TestSourceProvider
	metrics        *observability.SchedulerMetrics
	logger         *observability.Logger
	tracer         *observability.Tracer
	audit          *observability.AuditLogger
}

func newPipeline(cfg config.Config, logger *observability.Logger, deps WorkerDeps) (*kafka.Processor, *scheduler.Dispatcher, storage.RunStore, error) {
	hasKafkaDeps := deps.Consumer != nil || deps.Producer != nil
	ownsKafkaClients := false
	pipelineReady := false
	if !hasKafkaDeps {
		consumer, producer, err := kafka.NewClients(cfg.Kafka)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create kafka clients: %w", err)
		}
		deps.Consumer = consumer
		deps.Producer = producer
		ownsKafkaClients = true
	} else if deps.Consumer == nil || deps.Producer == nil {
		return nil, nil, nil, errors.New("both kafka consumer and producer are required when pipeline is enabled")
	}
	defer func() {
		if ownsKafkaClients && !pipelineReady {
			_ = deps.Consumer.Close()
			_ = deps.Producer.Close()
		}
	}()

	executor := deps.Executor
	if executor == nil {
		var err error
		executor, err = executorFromConfig(cfg.Executor)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	languageRegistry := deps.Languages
	if languageRegistry == nil {
		manifest, err := languages.LoadManifest(cfg.Config.LanguagesFile)
		if err != nil {
			return nil, nil, nil, err
		}
		languageRegistry, err = languages.NewRegistry(manifest,
			pythonadapter.NewAdapter(), golangadapter.NewAdapter(), javascript.NewAdapter(), cpp.NewAdapter())
		if err != nil {
			return nil, nil, nil, err
		}
	}

	metrics := deps.Metrics
	if metrics == nil {
		metrics = observability.NewSchedulerMetrics()
	}
	tracer := observability.NewTracer(cfg.Observability.TracingEnabled, logger)
	audit := observability.NewAuditLogger(cfg.Observability.AuditEnabled, logger)

	dispatcher, err := scheduler.NewDispatcher(cfg.Scheduler, requestExecutor{executor: executor, metrics: metrics}, metrics, logger, scheduler.ExecutorHealthFunc(executor.Healthy))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create scheduler dispatcher: %w", err)
	}

	limitsConfig, err := config.LoadLimits(cfg.Config.LimitsFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load limits config: %w", err)
	}

	runStore := deps.RunStore
	if runStore == nil {
		runStore, err = storage.NewRunStore(context.Background(), cfg.Storage)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create run store: %w", err)
		}
	}
	if err := registerWorkerInventory(context.Background(), runStore, cfg); err != nil {
		return nil, nil, nil, fmt.Errorf("register worker inventory: %w", err)
	}

	artifactManager := deps.Artifacts
	if artifactManager == nil {
		artifactManager, err = artifacts.NewManagerFromConfig(cfg.Storage.Artifacts)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create artifact manager: %w", err)
		}
	}
	testSources := deps.Tests
	if testSources == nil {
		testSources, err = testsources.NewProviderFromConfig(cfg.Tests)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create test source provider: %w", err)
		}
	}

	topics := kafka.TopicNamesFromConfig(cfg.Kafka.Topics)
	securityPolicy := security.PolicyFromConfigAndLimits(cfg, limitsConfig)
	handler := requestPipeline{
		legacyHarness: cfg.FeatureFlags.LegacyHarness,
		dispatcher:    dispatcher,
		producer:      deps.Producer,
		topics:        topics,
		languages:     languageRegistry,
		workerID:      cfg.App.WorkerID,
		security:      securityPolicy,
		store:         runStore,
		artifacts:     artifactManager,
		tests:         testSources,
		metrics:       metrics,
		logger:        logger,
		tracer:        tracer,
		audit:         audit,
	}
	if compiler, ok := executor.(interface {
		BuildAllowance(execution.ExecutionCommand) time.Duration
	}); ok {
		handler.buildAllowance = compiler.BuildAllowance
	}
	processor, err := kafka.NewProcessor(deps.Consumer, deps.Producer, topics, cfg.App.WorkerID, handler, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create kafka processor: %w", err)
	}
	processor.SecurityGuard = security.NewGuard(securityPolicy)
	processor.ValidationOptions = security.ValidationOptionsFromLimits(limitsConfig)
	processor.RetryPolicy = kafka.RetryPolicyFromConfig(cfg.Kafka.Retry)
	processor.RunStore = runStore
	processor.Metrics = metrics
	processor.Tracer = tracer
	processor.Audit = audit
	pipelineReady = true

	return processor, dispatcher, runStore, nil
}

func executorFromConfig(cfg config.ExecutorConfig) (execution.Executor, error) {
	switch cfg.Type {
	case "fake":
		return fake.NewExecutor(cfg.Fake)
	case "gojudge":
		goJudgeConfig := cfg.GoJudge
		if goJudgeConfig.RequestTimeoutMS <= 0 {
			goJudgeConfig.RequestTimeoutMS = cfg.ClientTimeoutMS
		}
		return gojudge.NewExecutor(goJudgeConfig)
	default:
		return nil, fmt.Errorf("unsupported executor type %q for worker pipeline", cfg.Type)
	}
}

func (p requestPipeline) HandleRunRequest(ctx context.Context, envelope kafka.Message, req contracts.RunRequest) error {
	_ = envelope
	reservation, err := p.store.ReserveAttempt(ctx, req, p.workerID)
	if err != nil {
		return classifyStorageReservationError(err)
	}
	switch reservation.State {
	case storage.AttemptDuplicateCompleted:
		p.logger.Info(ctx, "duplicate attempt result found, republishing known result", "attempt_id", req.AttemptID, "submission_id", req.SubmissionID)
		return p.publishResult(ctx, reservation.Result)
	case storage.AttemptDuplicateRunning:
		if _, durable := p.store.(storage.AttemptFencer); durable {
			// Lock уже принадлежит нам: прежнее выполнение оборвалось. Тот же attempt не запускаем вновь.
			result := execution.RunResultFromExecution(req, execution.InternalErrorResult("execution interrupted", false), execution.ResultTimingInput{}, p.workerID)
			result.Error.Code = "execution_interrupted"
			return p.completeAndPublish(ctx, result)
		}
		p.logger.Warn(ctx, "duplicate attempt is already running", "attempt_id", req.AttemptID, "submission_id", req.SubmissionID)
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "attempt_in_progress", storage.ErrAttemptInProgress, false)
	case storage.AttemptReserved:
	default:
		return fmt.Errorf("unknown attempt reservation state %q", reservation.State)
	}

	if req.Execution == nil && !p.legacyHarness {
		return p.completeInvalidRequest(ctx, req, "checker_error", errors.New("legacy harness is disabled"))
	}
	prepareStartedAt := time.Now()
	prepareCtx, endPrepare := p.startSpan(ctx, "prepare", "attempt_id", req.AttemptID, "language", req.Language.ID)
	ctx = prepareCtx
	finishPrepare := func(err error, attrs ...any) {
		endPrepare(err, attrs...)
		if p.metrics != nil {
			p.metrics.ObserveProcessingDuration("prepare", time.Since(prepareStartedAt))
		}
	}

	if p.tests != nil {
		resolved, err := p.tests.Resolve(ctx, req)
		if err != nil {
			finishPrepare(err, "stage", "test_source")
			return p.handleTestSourceError(ctx, req, err)
		}
		req = resolved
		if err := security.NewGuard(p.security).ValidateRunRequest(req); err != nil {
			if p.audit != nil {
				p.audit.SecurityReject(ctx, "request_rejected", err, "attempt_id", req.AttemptID, "submission_id", req.SubmissionID)
			}
			finishPrepare(err, "stage", "security")
			return p.completeInvalidRequest(ctx, req, "invalid_request", err)
		}
	}

	command, err := p.languages.BuildCommand(ctx, req)
	if err != nil {
		finishPrepare(err, "stage", "language")
		status := contracts.StatusInvalidRequest
		code := "invalid_request"
		if errors.Is(err, languages.ErrUnsupportedLanguage) {
			status = contracts.StatusUnsupportedLanguage
			code = "unsupported_language"
		}

		result := execution.RunResultFromExecution(req, execution.InternalErrorResult(err.Error(), false), execution.ResultTimingInput{}, p.workerID)
		result.Status = status
		if result.Error != nil {
			result.Error.Code = code
		}
		return p.completeAndPublish(ctx, result)
	}
	finishPrepare(nil, "stage", "language")

	timeout := time.Duration(req.Limits.Wallms) * time.Millisecond
	if p.buildAllowance != nil {
		timeout += p.buildAllowance(command)
	}
	executeStartedAt := time.Now()
	executeCtx, endExecute := p.startSpan(ctx, "execute", "attempt_id", req.AttemptID, "language", req.Language.ID)
	jobResult, err := p.dispatcher.DispatchAndWait(executeCtx, scheduler.Job{
		ID:         req.AttemptID,
		AttemptID:  req.AttemptID,
		LanguageID: req.Language.ID,
		CreatedAt:  req.CreatedAt,
		Timeout:    timeout,
		Payload:    command,
	})
	endExecute(err, "scheduler_status", jobResult.Status)
	if p.metrics != nil {
		p.metrics.ObserveProcessingDuration("execute", time.Since(executeStartedAt))
	}

	var execResult execution.ExecutionResult
	if errors.Is(err, scheduler.ErrJobTimeout) {
		execResult = execution.TimeoutResult(err.Error())
	} else if err != nil {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "execution_infra_error", err, true)
	} else if output, ok := jobResult.Output.(execution.ExecutionResult); ok {
		execResult = output
	} else {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInternal, "unexpected_execution_output", errors.New("scheduler returned unexpected execution output"), true)
	}
	if execResult.Error != nil && execResult.Error.Retryable {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, execResult.Error.Code, errors.New(execResult.Error.Message), true)
	}
	execResult = applyOutputPolicy(execResult, p.security)

	result := execution.RunResultFromExecution(req, execResult, execution.ResultTimingInput{
		QueueTime: jobResult.QueueTime,
		RunTime:   jobResult.RunTime,
	}, p.workerID)
	return p.completeAndPublish(ctx, result)
}

func (p requestPipeline) handleTestSourceError(ctx context.Context, req contracts.RunRequest, err error) error {
	var sourceErr testsources.SourceError
	if errors.As(err, &sourceErr) {
		if sourceErr.Retryable {
			return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, sourceErr.Reason, err, true)
		}
		return p.completeInvalidRequest(ctx, req, sourceErr.Reason, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "test_source_load_failed", err, true)
}

func (p requestPipeline) completeInvalidRequest(ctx context.Context, req contracts.RunRequest, code string, err error) error {
	result := execution.RunResultFromExecution(req, execution.InternalErrorResult(err.Error(), false), execution.ResultTimingInput{}, p.workerID)
	result.Status = contracts.StatusInvalidRequest
	if result.Error != nil {
		result.Error.Code = code
	}
	return p.completeAndPublish(ctx, result)
}

func (p requestPipeline) completeAndPublish(ctx context.Context, result contracts.RunResult) error {
	result = contracts.RedactDiagnostics(result)
	attempt, ok, err := p.store.GetAttempt(ctx, result.AttemptID)
	if err != nil {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "storage_get_attempt_failed", err, false)
	}
	if ok && p.artifacts != nil {
		processed, err := p.artifacts.ProcessResult(ctx, attempt.RunID, result)
		if err != nil {
			if p.metrics != nil {
				p.metrics.IncArtifactFailure("result_logs")
			}
			return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "artifact_store_failed", err, true)
		}
		p.recordArtifactMetrics(result.Logs, processed.Logs)
		result = processed
	}

	if err := p.store.CompleteAttempt(ctx, result); err != nil {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "storage_complete_failed", err, true)
	}
	if ok {
		event, err := storage.ResultOutboxEvent(attempt.RunID, result, p.topics.Results, time.Now().UTC())
		if err != nil {
			return kafka.NewProcessingError(kafka.ErrorClassInternal, "outbox_event_build_failed", err, false)
		}
		if _, err := p.store.AppendOutboxEvent(ctx, event); err != nil {
			return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "outbox_append_failed", err, false)
		}
	}
	if err := p.publishResult(ctx, result); err != nil {
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "result_publish_failed", err, false)
	}
	return nil
}

func (p requestPipeline) recordArtifactMetrics(before contracts.ResultLogs, after contracts.ResultLogs) {
	if p.metrics == nil {
		return
	}
	if before.StdoutArtifact == nil && after.StdoutArtifact != nil {
		p.metrics.IncArtifactStored(after.StdoutArtifact.Kind, after.StdoutArtifact.SizeBytes)
	}
	if before.StderrArtifact == nil && after.StderrArtifact != nil {
		p.metrics.IncArtifactStored(after.StderrArtifact.Kind, after.StderrArtifact.SizeBytes)
	}
}

func (p requestPipeline) publishResult(ctx context.Context, result contracts.RunResult) error {
	publishStartedAt := time.Now()
	publishCtx, endPublish := p.startSpan(ctx, "publish", "topic", p.topics.Results, "attempt_id", result.AttemptID, "status", string(result.Status))
	msg, err := kafka.MessageForResult(p.topics.Results, result, p.workerID)
	if err != nil {
		endPublish(err)
		if p.metrics != nil {
			p.metrics.ObserveProcessingDuration("publish", time.Since(publishStartedAt))
			p.metrics.IncMessageFailure("result_message_build_failed")
		}
		return err
	}
	err = p.producer.WriteMessages(publishCtx, msg)
	endPublish(err)
	if p.metrics != nil {
		p.metrics.ObserveProcessingDuration("publish", time.Since(publishStartedAt))
		if err != nil {
			p.metrics.IncMessageFailure("produce_failed")
		} else {
			p.metrics.IncProduced(msg.Topic)
			p.metrics.IncResultStatus(string(result.Status))
			p.metrics.IncResultLanguage(result.Language.ID)
		}
	}
	return err
}

func applyOutputPolicy(result execution.ExecutionResult, policy security.Policy) execution.ExecutionResult {
	stdout, stderr, truncated := security.TruncateOutput(result.Stdout, result.Stderr, policy)
	result.Stdout = stdout
	result.Stderr = stderr
	result.LogsTruncated = result.LogsTruncated || truncated
	return result
}

func classifyStorageReservationError(err error) error {
	switch {
	case errors.Is(err, storage.ErrAttemptInProgress):
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "attempt_in_progress", err, false)
	case errors.Is(err, storage.ErrAttemptConflict):
		return kafka.NewProcessingError(kafka.ErrorClassUser, "attempt_conflict", err, false)
	default:
		return kafka.NewRetryableProcessingError(kafka.ErrorClassInfra, "storage_reserve_failed", err, false)
	}
}

func (p requestPipeline) startSpan(ctx context.Context, name string, attrs ...any) (context.Context, func(error, ...any)) {
	if p.tracer == nil {
		return ctx, func(error, ...any) {}
	}
	return p.tracer.Start(ctx, name, attrs...)
}
