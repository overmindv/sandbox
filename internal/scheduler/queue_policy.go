package scheduler

import (
	"context"
	"errors"
	"time"

	"overmindv/sandbox/internal/config"
)

var (
	ErrQueueFull   = errors.New("scheduler queue is full")
	ErrQueueClosed = errors.New("scheduler queue is closed")
)

type QueuePolicy struct {
	Name           string
	MaxDepth       int
	EnqueueTimeout time.Duration
}

func QueuePolicyFromConfig(cfg config.SchedulerQueueConfig) QueuePolicy {
	return QueuePolicy{
		Name:           cfg.Policy,
		MaxDepth:       cfg.MaxDepth,
		EnqueueTimeout: time.Duration(cfg.EnqueueTimeoutMS) * time.Millisecond,
	}
}

type boundedQueue struct {
	items  chan queuedJob
	closed chan struct{}
}

type queuedJob struct {
	ctx      context.Context
	job      Job
	resultCh chan JobResult
}

func newBoundedQueue(policy QueuePolicy) (*boundedQueue, error) {
	if policy.Name != "" && policy.Name != "bounded_fifo" {
		return nil, errors.New("queue policy must be bounded_fifo")
	}
	if policy.MaxDepth <= 0 {
		return nil, errors.New("queue max depth must be positive")
	}
	return &boundedQueue{
		items:  make(chan queuedJob, policy.MaxDepth),
		closed: make(chan struct{}),
	}, nil
}

func (q *boundedQueue) Enqueue(ctx context.Context, item queuedJob, timeout time.Duration) error {
	if timeout <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.closed:
			return ErrQueueClosed
		case q.items <- item:
			return nil
		default:
			return ErrQueueFull
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closed:
		return ErrQueueClosed
	case q.items <- item:
		return nil
	case <-timer.C:
		return ErrQueueFull
	}
}

func (q *boundedQueue) Dequeue(ctx context.Context) (queuedJob, error) {
	select {
	case <-ctx.Done():
		return queuedJob{}, ctx.Err()
	case <-q.closed:
		return queuedJob{}, ErrQueueClosed
	case item := <-q.items:
		return item, nil
	}
}

func (q *boundedQueue) Len() int {
	return len(q.items)
}

func (q *boundedQueue) Cap() int {
	return cap(q.items)
}

func (q *boundedQueue) Close() {
	select {
	case <-q.closed:
	default:
		close(q.closed)
	}
}
