package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"overmindv/sandbox/internal/storage"
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
	// Stop intake immediately without canceling the message already being processed.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	if err := w.Start(workCtx); err != nil {
		return err
	}

	shutdown := make(chan context.Context, 1)
	stopped := make(chan error, 1)
	go func() {
		w.runProcessorLoop(workCtx, ctx)
		stopped <- w.Shutdown(<-shutdown)
	}()

	<-ctx.Done()
	w.healthState.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), w.cfg.ShutdownTimeout())
	defer cancel()
	shutdown <- shutdownCtx
	select {
	case <-shutdownCtx.Done():
		return fmt.Errorf("worker shutdown: %w", shutdownCtx.Err())
	case err := <-stopped:
		return err
	}
}

func (w *Worker) runProcessorLoop(ctx, intakeCtx context.Context) {
	for intakeCtx.Err() == nil {
		if err := w.processor.ProcessOnceWithIntake(ctx, intakeCtx); err != nil {
			if intakeCtx.Err() != nil {
				return
			}

			w.logger.Error(ctx, "kafka message processing failed", "error", err)
			timer := time.NewTimer(processorErrorBackoff)
			select {
			case <-intakeCtx.Done():
				timer.Stop()
				return
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
	var err error
	if w.dispatcher != nil {
		err = errors.Join(err, w.dispatcher.Stop(ctx))
	}
	if w.processor != nil {
		err = errors.Join(err, w.processor.Close())
	}
	if w.runStore != nil {
		now := time.Now().UTC()
		err = errors.Join(err, w.runStore.UpsertWorkerInstance(ctx, storage.WorkerInstanceRecord{
			WorkerInstanceID: w.cfg.App.WorkerID,
			Status:           storage.WorkerStatusStopped,
			LastSeenAt:       now,
			StoppedAt:        &now,
			UpdatedAt:        now,
		}), w.runStore.Close())
	}
	if w.metricsServer != nil {
		err = errors.Join(err, w.metricsServer.Shutdown(ctx))
	}
	err = errors.Join(err, w.healthServer.Shutdown(ctx))
	w.started.Store(false)
	w.logger.Info(ctx, "worker stopped", "worker_id", w.cfg.App.WorkerID)
	return err
}
