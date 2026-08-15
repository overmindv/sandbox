package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/kafka"
	"overmindv/sandbox/internal/observability"
	"overmindv/sandbox/internal/scheduler"
)

type Worker struct {
	cfg           config.Config
	logger        *observability.Logger
	healthState   *HealthState
	healthServer  *HealthServer
	metricsServer *MetricsServer
	processor     *kafka.Processor
	dispatcher    *scheduler.Dispatcher
	started       atomic.Bool
}

func NewWorker(cfg config.Config, logger *observability.Logger) (*Worker, error) {
	return NewWorkerWithDeps(cfg, logger, WorkerDeps{})
}

func NewWorkerWithDeps(cfg config.Config, logger *observability.Logger, deps WorkerDeps) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate worker config: %w", err)
	}
	if logger == nil {
		logger = observability.NewLogger(cfg.Observability.LogLevel, cfg.Observability.LogFormat, nil)
	}

	healthState := NewHealthState(cfg)
	processor, dispatcher, err := newPipeline(cfg, logger, deps)
	if err != nil {
		return nil, err
	}

	return &Worker{
		cfg:           cfg,
		logger:        logger,
		healthState:   healthState,
		healthServer:  NewHealthServer(cfg.Server.HealthAddr, healthState, logger),
		metricsServer: NewMetricsServer(cfg.Server.MetricsAddr, dispatcher, logger, cfg.Observability.MetricsEnabled),
		processor:     processor,
		dispatcher:    dispatcher,
	}, nil
}

func (w *Worker) HealthAddr() string {
	if w == nil || w.healthServer == nil {
		return ""
	}
	return w.healthServer.Addr()
}

func (w *Worker) ProcessOnce(ctx context.Context) error {
	if w == nil || w.processor == nil {
		return kafka.ErrNoRunRequestHandler
	}
	return w.processor.ProcessOnce(ctx)
}
