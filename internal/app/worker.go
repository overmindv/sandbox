package app

import (
	"fmt"
	"sync/atomic"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

type Worker struct {
	cfg          config.Config
	logger       *observability.Logger
	healthState  *HealthState
	healthServer *HealthServer
	started      atomic.Bool
}

func NewWorker(cfg config.Config, logger *observability.Logger) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate worker config: %w", err)
	}
	if logger == nil {
		logger = observability.NewLogger(cfg.Observability.LogLevel, cfg.Observability.LogFormat, nil)
	}

	healthState := NewHealthState(cfg)
	return &Worker{
		cfg:          cfg,
		logger:       logger,
		healthState:  healthState,
		healthServer: NewHealthServer(cfg.Server.HealthAddr, healthState, logger),
	}, nil
}

func (w *Worker) HealthAddr() string {
	if w == nil || w.healthServer == nil {
		return ""
	}
	return w.healthServer.Addr()
}
