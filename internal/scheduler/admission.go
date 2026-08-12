package scheduler

import (
	"errors"
	"time"

	"overmindv/sandbox/internal/config"
)

var (
	ErrExecutorUnhealthy = errors.New("executor is unhealthy")
	ErrJobTooOld         = errors.New("job exceeded max queue age")
	ErrInvalidJob        = errors.New("invalid scheduler job")
)

type ExecutorHealth interface {
	Healthy() bool
}

type ExecutorHealthFunc func() bool

func (f ExecutorHealthFunc) Healthy() bool {
	return f()
}

type AdmissionController struct {
	RejectWhenExecutorUnhealthy bool
	MaxQueueAge                 time.Duration
	ExecutorHealth              ExecutorHealth
	Now                         func() time.Time
}

func AdmissionFromConfig(cfg config.SchedulerAdmissionConf, health ExecutorHealth) AdmissionController {
	return AdmissionController{
		RejectWhenExecutorUnhealthy: cfg.RejectWhenExecutorUnhealthy,
		MaxQueueAge:                 time.Duration(cfg.MaxQueueAgeMS) * time.Millisecond,
		ExecutorHealth:              health,
		Now:                         func() time.Time { return time.Now().UTC() },
	}
}

func (a AdmissionController) Check(job Job) error {
	if job.ID == "" {
		return ErrInvalidJob
	}
	if job.LanguageID == "" {
		return ErrInvalidJob
	}
	if a.RejectWhenExecutorUnhealthy && a.ExecutorHealth != nil && !a.ExecutorHealth.Healthy() {
		return ErrExecutorUnhealthy
	}
	if a.MaxQueueAge > 0 && !job.CreatedAt.IsZero() {
		now := time.Now().UTC()
		if a.Now != nil {
			now = a.Now().UTC()
		}
		if now.Sub(job.CreatedAt.UTC()) > a.MaxQueueAge {
			return ErrJobTooOld
		}
	}
	return nil
}
