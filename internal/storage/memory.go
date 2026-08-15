package storage

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"overmindv/sandbox/internal/contracts"
)

type MemoryRunStore struct {
	mu              sync.RWMutex
	runs            map[string]memoryRun
	attempts        map[string]memoryAttempt
	idempotencyKeys map[string]string
	now             func() time.Time
}

type memoryRun struct {
	SubmissionID   string
	UserID         string
	TaskID         string
	Language       contracts.LanguageSpec
	FirstAttemptID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type memoryAttempt struct {
	SubmissionID string
	AttemptID    string
	Request      contracts.RunRequest
	Result       *contracts.RunResult
	WorkerID     string
	StartedAt    time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{
		runs:            make(map[string]memoryRun),
		attempts:        make(map[string]memoryAttempt),
		idempotencyKeys: make(map[string]string),
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemoryRunStore) ReserveAttempt(ctx context.Context, req contracts.RunRequest, workerID string) (AttemptReservation, error) {
	if err := ctx.Err(); err != nil {
		return AttemptReservation{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.attempts[req.AttemptID]; ok {
		if existing.SubmissionID != req.SubmissionID {
			return AttemptReservation{}, ErrAttemptConflict
		}
		if existing.Result != nil {
			result, err := cloneRunResult(*existing.Result)
			if err != nil {
				return AttemptReservation{}, err
			}
			return AttemptReservation{State: AttemptDuplicateCompleted, Result: result}, nil
		}
		return AttemptReservation{State: AttemptDuplicateRunning}, nil
	}

	now := s.now()
	if _, ok := s.runs[req.SubmissionID]; !ok {
		s.runs[req.SubmissionID] = memoryRun{
			SubmissionID:   req.SubmissionID,
			UserID:         req.UserID,
			TaskID:         req.TaskID,
			Language:       req.Language,
			FirstAttemptID: req.AttemptID,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	} else {
		run := s.runs[req.SubmissionID]
		run.UpdatedAt = now
		s.runs[req.SubmissionID] = run
	}

	clonedRequest, err := cloneRunRequest(req)
	if err != nil {
		return AttemptReservation{}, err
	}
	s.attempts[req.AttemptID] = memoryAttempt{
		SubmissionID: req.SubmissionID,
		AttemptID:    req.AttemptID,
		Request:      clonedRequest,
		WorkerID:     workerID,
		StartedAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return AttemptReservation{State: AttemptReserved}, nil
}

func (s *MemoryRunStore) CompleteAttempt(ctx context.Context, result contracts.RunResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attempt, ok := s.attempts[result.AttemptID]
	if !ok {
		return ErrAttemptNotFound
	}
	if attempt.SubmissionID != result.SubmissionID {
		return ErrAttemptConflict
	}

	clonedResult, err := cloneRunResult(result)
	if err != nil {
		return err
	}
	now := s.now()
	attempt.Result = &clonedResult
	attempt.WorkerID = result.WorkerID
	attempt.CompletedAt = &now
	attempt.UpdatedAt = now
	s.attempts[result.AttemptID] = attempt

	if run, ok := s.runs[result.SubmissionID]; ok {
		run.UpdatedAt = now
		s.runs[result.SubmissionID] = run
	}
	return nil
}

func (s *MemoryRunStore) GetAttemptResult(ctx context.Context, attemptID string) (contracts.RunResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.RunResult{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	attempt, ok := s.attempts[attemptID]
	if !ok || attempt.Result == nil {
		return contracts.RunResult{}, false, nil
	}
	result, err := cloneRunResult(*attempt.Result)
	if err != nil {
		return contracts.RunResult{}, false, err
	}
	return result, true, nil
}

func (s *MemoryRunStore) BindIdempotencyKey(ctx context.Context, key string, attemptID string) (IdempotencyKeyBinding, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyKeyBinding{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existingAttemptID, ok := s.idempotencyKeys[key]; ok {
		if existingAttemptID != attemptID {
			return IdempotencyKeyBinding{}, ErrIdempotencyConflict
		}
		return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: false}, nil
	}

	s.idempotencyKeys[key] = attemptID
	return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: true}, nil
}

func (s *MemoryRunStore) Close() error {
	return nil
}

func cloneRunRequest(req contracts.RunRequest) (contracts.RunRequest, error) {
	var cloned contracts.RunRequest
	if err := cloneJSON(req, &cloned); err != nil {
		return contracts.RunRequest{}, err
	}
	return cloned, nil
}

func cloneRunResult(result contracts.RunResult) (contracts.RunResult, error) {
	var cloned contracts.RunResult
	if err := cloneJSON(result, &cloned); err != nil {
		return contracts.RunResult{}, err
	}
	return cloned, nil
}

func cloneJSON(source any, target any) error {
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

var _ RunStore = (*MemoryRunStore)(nil)
