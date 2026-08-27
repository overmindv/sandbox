package storage

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"overmindv/sandbox/internal/contracts"
)

type MemoryRunStore struct {
	mu                 sync.RWMutex
	runs               map[string]RunRecord
	runIDsBySubmission map[string][]string
	attempts           map[string]AttemptRecord
	attemptResults     map[string]contracts.RunResult
	idempotencyKeys    map[string]IdempotencyKeyRecord
	workerInstances    map[string]WorkerInstanceRecord
	workerCapabilities map[string]map[string]WorkerCapabilityRecord
	runFiles           map[string][]RunFileRecord
	runTests           map[string][]RunTestRecord
	attemptTestResults map[string][]AttemptTestResultRecord
	artifacts          map[string]ArtifactRecord
	outbox             map[string]OutboxEventRecord
	now                func() time.Time
}

func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{
		runs:               make(map[string]RunRecord),
		runIDsBySubmission: make(map[string][]string),
		attempts:           make(map[string]AttemptRecord),
		attemptResults:     make(map[string]contracts.RunResult),
		idempotencyKeys:    make(map[string]IdempotencyKeyRecord),
		workerInstances:    make(map[string]WorkerInstanceRecord),
		workerCapabilities: make(map[string]map[string]WorkerCapabilityRecord),
		runFiles:           make(map[string][]RunFileRecord),
		runTests:           make(map[string][]RunTestRecord),
		attemptTestResults: make(map[string][]AttemptTestResultRecord),
		artifacts:          make(map[string]ArtifactRecord),
		outbox:             make(map[string]OutboxEventRecord),
		now:                func() time.Time { return time.Now().UTC() },
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
		if result, ok := s.attemptResults[req.AttemptID]; ok {
			cloned, err := cloneRunResult(result)
			if err != nil {
				return AttemptReservation{}, err
			}
			return AttemptReservation{State: AttemptDuplicateCompleted, Result: cloned}, nil
		}
		if existing.Status == AttemptStatusRunning {
			return AttemptReservation{State: AttemptDuplicateRunning}, nil
		}
		existing.Status = AttemptStatusRunning
		existing.WorkerInstanceID = workerID
		existing.StartedAt = s.now()
		existing.CompletedAt = nil
		existing.LeaseExpiresAt = nil
		existing.LastErrorClass = ""
		existing.LastErrorReason = ""
		existing.LastErrorMessage = ""
		existing.UpdatedAt = existing.StartedAt
		s.attempts[req.AttemptID] = existing
		return AttemptReservation{State: AttemptReserved}, nil
	}

	now := s.now()
	snapshot, err := BuildRunSnapshot(req, now)
	if err != nil {
		return AttemptReservation{}, err
	}
	s.upsertWorkerInstanceLocked(WorkerInstanceRecord{
		WorkerInstanceID: workerID,
		Hostname:         workerID,
		Status:           WorkerStatusActive,
		StartedAt:        now,
		LastSeenAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	if _, exists := s.runs[snapshot.RunID]; !exists {
		s.runs[snapshot.RunID] = snapshot.Run
		s.runIDsBySubmission[req.SubmissionID] = append(s.runIDsBySubmission[req.SubmissionID], snapshot.RunID)
		s.runFiles[snapshot.RunID] = cloneRunFiles(snapshot.Files)
		s.runTests[snapshot.RunID] = cloneRunTests(snapshot.Tests)
	} else {
		run := s.runs[snapshot.RunID]
		run.UpdatedAt = now
		s.runs[snapshot.RunID] = run
	}

	requestJSON, err := json.Marshal(req)
	if err != nil {
		return AttemptReservation{}, err
	}
	s.attempts[req.AttemptID] = AttemptRecord{
		AttemptID:        req.AttemptID,
		RunID:            snapshot.RunID,
		SubmissionID:     req.SubmissionID,
		AttemptNo:        s.nextAttemptNoLocked(snapshot.RunID),
		Status:           AttemptStatusRunning,
		RequestHash:      snapshot.RequestHash,
		RequestJSON:      append(json.RawMessage(nil), requestJSON...),
		WorkerInstanceID: workerID,
		StartedAt:        now,
		CreatedAt:        now,
		UpdatedAt:        now,
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
	resultJSON, err := json.Marshal(clonedResult)
	if err != nil {
		return err
	}

	now := s.now()
	attempt.ResultJSON = append(json.RawMessage(nil), resultJSON...)
	attempt.WorkerInstanceID = result.WorkerID
	attempt.Status = AttemptStatusCompleted
	attempt.CompletedAt = &now
	attempt.UpdatedAt = now
	s.attempts[result.AttemptID] = attempt
	s.attemptResults[result.AttemptID] = clonedResult

	if run, ok := s.runs[attempt.RunID]; ok {
		run.Status = RunStatusCompleted
		if run.CompletedAt == nil {
			run.CompletedAt = &now
		}
		run.UpdatedAt = now
		s.runs[attempt.RunID] = run
	}

	s.attemptTestResults[result.AttemptID] = cloneAttemptTestResults(AttemptTestResultsFromRunResult(result, now))
	for _, artifact := range ArtifactsFromRunResult(attempt.RunID, result, now) {
		s.artifacts[artifact.ArtifactID] = artifact
	}
	return nil
}

func (s *MemoryRunStore) FailAttempt(ctx context.Context, failure AttemptFailure) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attempt, ok := s.attempts[failure.AttemptID]
	if !ok {
		return ErrAttemptNotFound
	}
	if attempt.SubmissionID != failure.SubmissionID {
		return ErrAttemptConflict
	}

	failedAt := failure.FailedAt.UTC()
	if failedAt.IsZero() {
		failedAt = s.now()
	}
	status := failure.Status
	if status == "" {
		status = AttemptStatusFailedInternal
	}
	attempt.Status = status
	if failure.WorkerInstanceID != "" {
		attempt.WorkerInstanceID = failure.WorkerInstanceID
	}
	attempt.DeliveryAttempts = failure.DeliveryAttempts
	attempt.LastErrorClass = failure.ErrorClass
	attempt.LastErrorReason = failure.Reason
	attempt.LastErrorMessage = failure.Message
	attempt.CompletedAt = &failedAt
	attempt.LeaseExpiresAt = nil
	attempt.UpdatedAt = failedAt
	s.attempts[failure.AttemptID] = attempt

	if run, ok := s.runs[attempt.RunID]; ok {
		run.Status = RunStatusRunning
		run.UpdatedAt = failedAt
		s.runs[attempt.RunID] = run
	}
	return nil
}

func (s *MemoryRunStore) GetAttemptResult(ctx context.Context, attemptID string) (contracts.RunResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.RunResult{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result, ok := s.attemptResults[attemptID]
	if !ok {
		return contracts.RunResult{}, false, nil
	}
	cloned, err := cloneRunResult(result)
	if err != nil {
		return contracts.RunResult{}, false, err
	}
	return cloned, true, nil
}

func (s *MemoryRunStore) BindIdempotencyKey(ctx context.Context, key string, attemptID string) (IdempotencyKeyBinding, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyKeyBinding{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.idempotencyKeys[key]; ok {
		if existing.AttemptID != attemptID {
			return IdempotencyKeyBinding{}, ErrIdempotencyConflict
		}
		return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: false}, nil
	}

	attempt, ok := s.attempts[attemptID]
	if !ok {
		return IdempotencyKeyBinding{}, ErrAttemptNotFound
	}

	now := s.now()
	s.idempotencyKeys[key] = IdempotencyKeyRecord{
		Key:         key,
		RunID:       attempt.RunID,
		AttemptID:   attemptID,
		RequestHash: attempt.RequestHash,
		CreatedAt:   now,
	}
	return IdempotencyKeyBinding{Key: key, AttemptID: attemptID, Created: true}, nil
}

func (s *MemoryRunStore) UpsertWorkerInstance(ctx context.Context, worker WorkerInstanceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertWorkerInstanceLocked(worker)
	return nil
}

func (s *MemoryRunStore) ReplaceWorkerCapabilities(ctx context.Context, workerID string, capabilities []WorkerCapabilityRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	records := make(map[string]WorkerCapabilityRecord, len(capabilities))
	for _, capability := range capabilities {
		if capability.WorkerInstanceID == "" {
			capability.WorkerInstanceID = workerID
		}
		if capability.CreatedAt.IsZero() {
			capability.CreatedAt = now
		}
		if capability.UpdatedAt.IsZero() {
			capability.UpdatedAt = now
		}
		key := capability.LanguageID + "\x00" + capability.LanguageVersion
		records[key] = capability
	}
	s.workerCapabilities[workerID] = records
	return nil
}

func (s *MemoryRunStore) GetWorkerInstance(ctx context.Context, workerID string) (WorkerInstanceRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return WorkerInstanceRecord{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	worker, ok := s.workerInstances[workerID]
	if !ok {
		return WorkerInstanceRecord{}, false, nil
	}
	return cloneWorkerInstance(worker), true, nil
}

func (s *MemoryRunStore) ListWorkerCapabilities(ctx context.Context, workerID string) ([]WorkerCapabilityRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	recordsByKey := s.workerCapabilities[workerID]
	records := make([]WorkerCapabilityRecord, 0, len(recordsByKey))
	for _, record := range recordsByKey {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].LanguageID == records[j].LanguageID {
			return records[i].LanguageVersion < records[j].LanguageVersion
		}
		return records[i].LanguageID < records[j].LanguageID
	})
	return records, nil
}

func (s *MemoryRunStore) GetRunBySubmission(ctx context.Context, submissionID string) (RunRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return RunRecord{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	runIDs := s.runIDsBySubmission[submissionID]
	if len(runIDs) == 0 {
		return RunRecord{}, false, nil
	}
	run := s.runs[runIDs[0]]
	for _, runID := range runIDs[1:] {
		candidate := s.runs[runID]
		if candidate.UpdatedAt.After(run.UpdatedAt) {
			run = candidate
		}
	}
	return cloneRunRecord(run), true, nil
}

func (s *MemoryRunStore) GetAttempt(ctx context.Context, attemptID string) (AttemptRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return AttemptRecord{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	attempt, ok := s.attempts[attemptID]
	if !ok {
		return AttemptRecord{}, false, nil
	}
	return cloneAttemptRecord(attempt), true, nil
}

func (s *MemoryRunStore) ListRunFiles(ctx context.Context, runID string) ([]RunFileRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRunFiles(s.runFiles[runID]), nil
}

func (s *MemoryRunStore) ListRunTests(ctx context.Context, runID string) ([]RunTestRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRunTests(s.runTests[runID]), nil
}

func (s *MemoryRunStore) ListAttemptTestResults(ctx context.Context, attemptID string) ([]AttemptTestResultRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAttemptTestResults(s.attemptTestResults[attemptID]), nil
}

func (s *MemoryRunStore) ListArtifacts(ctx context.Context, runID string, attemptID string) ([]ArtifactRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]ArtifactRecord, 0)
	for _, artifact := range s.artifacts {
		if runID != "" && artifact.RunID != runID {
			continue
		}
		if attemptID != "" && artifact.AttemptID != attemptID {
			continue
		}
		records = append(records, artifact)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ArtifactID < records[j].ArtifactID
	})
	return records, nil
}

func (s *MemoryRunStore) AppendOutboxEvent(ctx context.Context, event OutboxEventRecord) (OutboxEventRecord, error) {
	if err := ctx.Err(); err != nil {
		return OutboxEventRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.outbox[event.EventID]; ok {
		return cloneOutboxEvent(existing), nil
	}

	now := s.now()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.UpdatedAt.IsZero() {
		event.UpdatedAt = event.CreatedAt
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = event.CreatedAt
	}
	if event.Status == "" {
		event.Status = OutboxStatusPending
	}
	if len(event.HeadersJSON) == 0 {
		event.HeadersJSON = EmptyHeadersJSON()
	}
	s.outbox[event.EventID] = cloneOutboxEvent(event)
	return cloneOutboxEvent(event), nil
}

func (s *MemoryRunStore) ListOutboxEvents(ctx context.Context, filter OutboxFilter) ([]OutboxEventRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]OutboxEventRecord, 0, len(s.outbox))
	for _, event := range s.outbox {
		if filter.Status != "" && event.Status != filter.Status {
			continue
		}
		records = append(records, cloneOutboxEvent(event))
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].AvailableAt.Equal(records[j].AvailableAt) {
			return records[i].EventID < records[j].EventID
		}
		return records[i].AvailableAt.Before(records[j].AvailableAt)
	})
	if filter.Limit > 0 && len(records) > filter.Limit {
		records = records[:filter.Limit]
	}
	return records, nil
}

func (s *MemoryRunStore) MarkOutboxPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	event, ok := s.outbox[eventID]
	if !ok {
		return ErrOutboxEventNotFound
	}
	publishedAt = publishedAt.UTC()
	event.Status = OutboxStatusPublished
	event.PublishedAt = &publishedAt
	event.UpdatedAt = publishedAt
	s.outbox[eventID] = event
	return nil
}

func (s *MemoryRunStore) Close() error {
	return nil
}

func (s *MemoryRunStore) upsertWorkerInstanceLocked(worker WorkerInstanceRecord) {
	if worker.WorkerInstanceID == "" {
		return
	}
	now := s.now()
	if worker.CreatedAt.IsZero() {
		worker.CreatedAt = now
	}
	if worker.UpdatedAt.IsZero() {
		worker.UpdatedAt = now
	}
	if worker.StartedAt.IsZero() {
		worker.StartedAt = worker.CreatedAt
	}
	if worker.LastSeenAt.IsZero() {
		worker.LastSeenAt = worker.UpdatedAt
	}
	if worker.Status == "" {
		worker.Status = WorkerStatusActive
	}
	if existing, ok := s.workerInstances[worker.WorkerInstanceID]; ok {
		if worker.Hostname == "" {
			worker.Hostname = existing.Hostname
		}
		if worker.Environment == "" {
			worker.Environment = existing.Environment
		}
		if worker.ExecutorType == "" {
			worker.ExecutorType = existing.ExecutorType
		}
		if worker.Version == "" {
			worker.Version = existing.Version
		}
		worker.CreatedAt = existing.CreatedAt
		worker.StartedAt = existing.StartedAt
	}
	s.workerInstances[worker.WorkerInstanceID] = worker
}

func (s *MemoryRunStore) nextAttemptNoLocked(runID string) int {
	attemptNo := 1
	for _, attempt := range s.attempts {
		if attempt.RunID == runID && attempt.AttemptNo >= attemptNo {
			attemptNo = attempt.AttemptNo + 1
		}
	}
	return attemptNo
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

func cloneRunRecord(record RunRecord) RunRecord {
	if record.CompletedAt != nil {
		completedAt := *record.CompletedAt
		record.CompletedAt = &completedAt
	}
	return record
}

func cloneAttemptRecord(record AttemptRecord) AttemptRecord {
	record.RequestJSON = append(json.RawMessage(nil), record.RequestJSON...)
	record.ResultJSON = append(json.RawMessage(nil), record.ResultJSON...)
	if record.CompletedAt != nil {
		completedAt := *record.CompletedAt
		record.CompletedAt = &completedAt
	}
	if record.LeaseExpiresAt != nil {
		leaseExpiresAt := *record.LeaseExpiresAt
		record.LeaseExpiresAt = &leaseExpiresAt
	}
	return record
}

func cloneWorkerInstance(record WorkerInstanceRecord) WorkerInstanceRecord {
	if record.StoppedAt != nil {
		stoppedAt := *record.StoppedAt
		record.StoppedAt = &stoppedAt
	}
	return record
}

func cloneRunFiles(records []RunFileRecord) []RunFileRecord {
	cloned := append([]RunFileRecord(nil), records...)
	sort.Slice(cloned, func(i, j int) bool {
		if cloned[i].Kind == cloned[j].Kind {
			return cloned[i].Path < cloned[j].Path
		}
		return cloned[i].Kind < cloned[j].Kind
	})
	return cloned
}

func cloneRunTests(records []RunTestRecord) []RunTestRecord {
	cloned := make([]RunTestRecord, len(records))
	for i, record := range records {
		record.TestJSON = append(json.RawMessage(nil), record.TestJSON...)
		cloned[i] = record
	}
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].OrderIndex < cloned[j].OrderIndex
	})
	return cloned
}

func cloneAttemptTestResults(records []AttemptTestResultRecord) []AttemptTestResultRecord {
	cloned := make([]AttemptTestResultRecord, len(records))
	for i, record := range records {
		record.ErrorJSON = append(json.RawMessage(nil), record.ErrorJSON...)
		cloned[i] = record
	}
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].OrderIndex < cloned[j].OrderIndex
	})
	return cloned
}

func cloneOutboxEvent(event OutboxEventRecord) OutboxEventRecord {
	event.PayloadJSON = append(json.RawMessage(nil), event.PayloadJSON...)
	event.HeadersJSON = append(json.RawMessage(nil), event.HeadersJSON...)
	if event.PublishedAt != nil {
		publishedAt := *event.PublishedAt
		event.PublishedAt = &publishedAt
	}
	return event
}

var _ RunStore = (*MemoryRunStore)(nil)
