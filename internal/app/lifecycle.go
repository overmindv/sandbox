package app

import (
	"context"
	"errors"
	"time"
)

var ErrWorkerAlreadyStarted = errors.New("worker already started")

const processorErrorBackoff = 250 * time.Millisecond

func (w *Worker) Start(ctx context.Context) error {
	if w == nil {
		return errors.New("worker is nil")
	}
	if !w.started.CompareAndSwap(false, true) {
		return ErrWorkerAlreadyStarted
	}

	if err := w.healthServer.Start(ctx); err != nil {
		w.started.Store(false)
		return err
	}
	if w.metricsServer != nil {
		if err := w.metricsServer.Start(ctx); err != nil {
			_ = w.healthServer.Shutdown(context.Background())
			w.started.Store(false)
			return err
		}
	}
	if w.dispatcher != nil {
		if err := w.dispatcher.Start(ctx); err != nil {
			if w.metricsServer != nil {
				_ = w.metricsServer.Shutdown(context.Background())
			}
			_ = w.healthServer.Shutdown(context.Background())
			w.started.Store(false)
			return err
		}
	}
	w.healthState.SetReady(true)
	w.logger.Info(ctx, "worker started", "worker_id", w.cfg.App.WorkerID, "environment", w.cfg.App.Environment)
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.Start(ctx); err != nil {
		return err
	}

	processorErr := w.runProcessorLoop(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), w.cfg.ShutdownTimeout())
	defer cancel()
	shutdownErr := w.Shutdown(shutdownCtx)
	return errors.Join(processorErr, shutdownErr)
}

func (w *Worker) runProcessorLoop(ctx context.Context) error {
	for {
		if err := w.ProcessOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}

			w.logger.Error(ctx, "kafka message processing failed", "error", err)
			timer := time.NewTimer(processorErrorBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}

func (w *Worker) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.healthState.SetReady(false)
	w.logger.Info(ctx, "worker shutting down", "worker_id", w.cfg.App.WorkerID)
	if w.dispatcher != nil {
		if err := w.dispatcher.Stop(ctx); err != nil {
			return err
		}
	}
	if w.processor != nil {
		if err := w.processor.Close(); err != nil {
			return err
		}
	}
	if w.runStore != nil {
		if err := w.runStore.Close(); err != nil {
			return err
		}
	}
	if w.metricsServer != nil {
		if err := w.metricsServer.Shutdown(ctx); err != nil {
			return err
		}
	}
	if err := w.healthServer.Shutdown(ctx); err != nil {
		return err
	}
	w.started.Store(false)
	w.logger.Info(ctx, "worker stopped", "worker_id", w.cfg.App.WorkerID)
	return nil
}
