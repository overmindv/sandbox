package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/execution/fake"
	"overmindv/sandbox/internal/execution/gojudge"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/languages"
	pythonadapter "overmindv/sandbox/internal/languages/python"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
	"overmindv/sandbox/internal/security"
	"overmindv/sandbox/internal/storage"
)

type WorkerDeps struct {
	Consumer  kafka.Consumer
	Producer  kafka.Producer
	Executor  execution.Executor
	Languages *languages.Registry
	Metrics   *observability.SchedulerMetrics
	RunStore  storage.RunStore
}

type requestExecutor struct {
	executor execution.Executor
}

func (h requestExecutor) HandleJob(ctx context.Context, job scheduler.Job) (any, error) {
	command, ok := job.Payload.(execution.ExecutionCommand)
	if !ok {
		return execution.ExecutionResult{}, execution.ErrInvalidCommandPayload
	}
	return h.executor.Execute(ctx, command)
}

type requestPipeline struct {
	dispatcher *scheduler.Dispatcher
	producer   kafka.Producer
	topics     kafka.TopicNames
	languages  *languages.Registry
	workerID   string
	security   security.Policy
	store      storage.RunStore
	logger     *observability.Logger
}

func newPipeline(cfg config.Config, logger *observability.Logger, deps WorkerDeps) (*kafka.Processor, *scheduler.Dispatcher, storage.RunStore, error) {
	hasKafkaDeps := deps.Consumer != nil || deps.Producer != nil
	if !hasKafkaDeps {
		consumer, err := kafka.NewConsumer(cfg.Kafka)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("create kafka consumer: %w", err)
		}
		deps.Consumer = consumer
		deps.Producer = kafka.NewProducer(cfg.Kafka)
	} else if deps.Consumer == nil || deps.Producer == nil {
		return nil, nil, nil, errors.New("both kafka consumer and producer are required when pipeline is enabled")
	}

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
		languageRegistry, err = languages.NewRegistry(manifest, pythonadapter.NewAdapter())
		if err != nil {
			return nil, nil, nil, err
		}
	}

	dispatcher, err := scheduler.NewDispatcher(cfg.Scheduler, requestExecutor{executor: executor}, deps.Metrics, logger, scheduler.ExecutorHealthFunc(executor.Healthy))
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

	topics := kafka.TopicNamesFromConfig(cfg.Kafka.Topics)
	securityPolicy := security.PolicyFromConfigAndLimits(cfg, limitsConfig)
	handler := requestPipeline{
		dispatcher: dispatcher,
		producer:   deps.Producer,
		topics:     topics,
		languages:  languageRegistry,
		workerID:   cfg.App.WorkerID,
		security:   securityPolicy,
		store:      runStore,
		logger:     logger,
	}
	processor, err := kafka.NewProcessor(deps.Consumer, deps.Producer, topics, cfg.App.WorkerID, handler, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create kafka processor: %w", err)
	}
	processor.SecurityGuard = security.NewGuard(securityPolicy)
	processor.ValidationOptions = security.ValidationOptionsFromLimits(limitsConfig)

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
	reservation, err := p.store.ReserveAttempt(ctx, req, p.workerID)
	if err != nil {
		return err
	}
	switch reservation.State {
	case storage.AttemptDuplicateCompleted:
		p.logger.Info(ctx, "duplicate attempt result found, republishing known result", "attempt_id", req.AttemptID, "submission_id", req.SubmissionID)
		return p.publishResult(ctx, reservation.Result)
	case storage.AttemptDuplicateRunning:
		p.logger.Warn(ctx, "duplicate attempt is already running", "attempt_id", req.AttemptID, "submission_id", req.SubmissionID)
		return storage.ErrAttemptInProgress
	case storage.AttemptReserved:
	default:
		return fmt.Errorf("unknown attempt reservation state %q", reservation.State)
	}

	command, err := p.languages.BuildCommand(ctx, req)
	if err != nil {
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

	timeout := time.Duration(req.Limits.Wallms) * time.Millisecond
	jobResult, err := p.dispatcher.DispatchAndWait(ctx, scheduler.Job{
		ID:         req.AttemptID,
		AttemptID:  req.AttemptID,
		LanguageID: req.Language.ID,
		CreatedAt:  req.CreatedAt,
		Timeout:    timeout,
		Payload:    command,
	})

	var execResult execution.ExecutionResult
	if errors.Is(err, scheduler.ErrJobTimeout) {
		execResult = execution.TimeoutResult(err.Error())
	} else if err != nil {
		execResult = execution.InternalErrorResult(err.Error(), true)
	} else if output, ok := jobResult.Output.(execution.ExecutionResult); ok {
		execResult = output
	} else {
		execResult = execution.InternalErrorResult("scheduler returned unexpected execution output", true)
	}
	execResult = applyOutputPolicy(execResult, p.security)

	result := execution.RunResultFromExecution(req, execResult, execution.ResultTimingInput{
		QueueTime: jobResult.QueueTime,
		RunTime:   jobResult.RunTime,
	}, p.workerID)
	return p.completeAndPublish(ctx, result)
}

func (p requestPipeline) completeAndPublish(ctx context.Context, result contracts.RunResult) error {
	if err := p.store.CompleteAttempt(ctx, result); err != nil {
		return err
	}
	attempt, ok, err := p.store.GetAttempt(ctx, result.AttemptID)
	if err != nil {
		return err
	}
	if ok {
		event, err := storage.ResultOutboxEvent(attempt.RunID, result, p.topics.Results, time.Now().UTC())
		if err != nil {
			return err
		}
		if _, err := p.store.AppendOutboxEvent(ctx, event); err != nil {
			return err
		}
	}
	return p.publishResult(ctx, result)
}

func (p requestPipeline) publishResult(ctx context.Context, result contracts.RunResult) error {
	msg, err := kafka.MessageForResult(p.topics.Results, result, p.workerID)
	if err != nil {
		return err
	}
	return p.producer.WriteMessages(ctx, msg)
}

func applyOutputPolicy(result execution.ExecutionResult, policy security.Policy) execution.ExecutionResult {
	stdout, stderr, truncated := security.TruncateOutput(result.Stdout, result.Stderr, policy)
	result.Stdout = stdout
	result.Stderr = stderr
	result.LogsTruncated = result.LogsTruncated || truncated
	return result
}
