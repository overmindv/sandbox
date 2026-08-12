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
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
)

type WorkerDeps struct {
	Consumer kafka.Consumer
	Producer kafka.Producer
	Executor execution.Executor
	Metrics  *observability.SchedulerMetrics
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
	workerID   string
}

func newPipeline(cfg config.Config, logger *observability.Logger, deps WorkerDeps) (*kafka.Processor, *scheduler.Dispatcher, error) {
	hasKafkaDeps := deps.Consumer != nil || deps.Producer != nil
	if !hasKafkaDeps {
		consumer, err := kafka.NewConsumer(cfg.Kafka)
		if err != nil {
			return nil, nil, fmt.Errorf("create kafka consumer: %w", err)
		}
		deps.Consumer = consumer
		deps.Producer = kafka.NewProducer(cfg.Kafka)
	} else if deps.Consumer == nil || deps.Producer == nil {
		return nil, nil, errors.New("both kafka consumer and producer are required when pipeline is enabled")
	}

	executor := deps.Executor
	if executor == nil {
		var err error
		executor, err = executorFromConfig(cfg.Executor)
		if err != nil {
			return nil, nil, err
		}
	}

	dispatcher, err := scheduler.NewDispatcher(cfg.Scheduler, requestExecutor{executor: executor}, deps.Metrics, logger, scheduler.ExecutorHealthFunc(executor.Healthy))
	if err != nil {
		return nil, nil, fmt.Errorf("create scheduler dispatcher: %w", err)
	}

	topics := kafka.TopicNamesFromConfig(cfg.Kafka.Topics)
	handler := requestPipeline{
		dispatcher: dispatcher,
		producer:   deps.Producer,
		topics:     topics,
		workerID:   cfg.App.WorkerID,
	}
	processor, err := kafka.NewProcessor(deps.Consumer, deps.Producer, topics, cfg.App.WorkerID, handler, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("create kafka processor: %w", err)
	}

	return processor, dispatcher, nil
}

func executorFromConfig(cfg config.ExecutorConfig) (execution.Executor, error) {
	switch cfg.Type {
	case "fake":
		return fake.NewExecutor(cfg.Fake)
	default:
		return nil, fmt.Errorf("unsupported executor type %q for worker pipeline", cfg.Type)
	}
}

func (p requestPipeline) HandleRunRequest(ctx context.Context, envelope kafka.Message, req contracts.RunRequest) error {
	command, err := execution.CommandFromRunRequest(req)
	if err != nil {
		result := execution.RunResultFromExecution(req, execution.InternalErrorResult(err.Error(), false), execution.ResultTimingInput{}, p.workerID)
		result.Status = contracts.StatusInvalidRequest
		if result.Error != nil {
			result.Error.Code = "invalid_request"
		}
		return p.publishResult(ctx, result)
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

	result := execution.RunResultFromExecution(req, execResult, execution.ResultTimingInput{
		QueueTime: jobResult.QueueTime,
		RunTime:   jobResult.RunTime,
	}, p.workerID)
	return p.publishResult(ctx, result)
}

func (p requestPipeline) publishResult(ctx context.Context, result contracts.RunResult) error {
	msg, err := kafka.MessageForResult(p.topics.Results, result, p.workerID)
	if err != nil {
		return err
	}
	return p.producer.WriteMessages(ctx, msg)
}
