package execution

import (
	"time"

	"overmindv/sandbox/internal/contracts"
)

type ExecutionResult struct {
	Status          contracts.ExecutionStatus
	TestsTotal      int
	TestsPassed     int
	TestsFailed     int
	FirstFailedTest string
	TestCases       []contracts.TestCaseResult
	Stdout          string
	Stderr          string
	LogsTruncated   bool
	CPUms           int
	MemoryPeakBytes int64
	ExitCode        *int
	Signal          *string
	RunMS           int
	Error           *contracts.RunError
}

type RawExecutionResult struct {
	ExitCode        int
	Stdout          string
	Stderr          string
	ReportFiles     map[string][]byte
	TimedOut        bool
	MemoryExceeded  bool
	OutputExceeded  bool
	CPUms           int
	MemoryPeakBytes int64
	RunMS           int
}

type ResultTimingInput struct {
	QueueTime time.Duration
	RunTime   time.Duration
	PrepareMS int
	TotalMS   int
}

func RunResultFromExecution(req contracts.RunRequest, execResult ExecutionResult, timing ResultTimingInput, workerID string) contracts.RunResult {
	runMS := execResult.RunMS
	if runMS == 0 {
		runMS = int(timing.RunTime.Milliseconds())
	}

	totalMS := timing.TotalMS
	if totalMS == 0 {
		totalMS = int(timing.QueueTime.Milliseconds()) + timing.PrepareMS + runMS
	}

	return contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  req.SubmissionID,
		AttemptID:     req.AttemptID,
		UserID:        req.UserID,
		TaskID:        req.TaskID,
		Language:      req.Language,
		Status:        execResult.Status,
		Summary: contracts.ResultSummary{
			TestsTotal:      execResult.TestsTotal,
			TestsPassed:     execResult.TestsPassed,
			TestsFailed:     execResult.TestsFailed,
			FirstFailedTest: execResult.FirstFailedTest,
		},
		Timing: contracts.ResultTiming{
			QueueMS:   int(timing.QueueTime.Milliseconds()),
			PrepareMS: timing.PrepareMS,
			RunMS:     runMS,
			TotalMS:   totalMS,
		},
		Resources: contracts.ResultResources{
			CPUms:           execResult.CPUms,
			MemoryPeakBytes: execResult.MemoryPeakBytes,
			ExitCode:        execResult.ExitCode,
			Signal:          execResult.Signal,
		},
		Logs: contracts.ResultLogs{
			Stdout:    execResult.Stdout,
			Stderr:    execResult.Stderr,
			Truncated: execResult.LogsTruncated,
		},
		Tests:     execResult.TestCases,
		Error:     execResult.Error,
		TraceID:   req.TraceID,
		WorkerID:  workerID,
		CreatedAt: time.Now().UTC(),
	}
}

func TimeoutResult(message string) ExecutionResult {
	return ExecutionResult{
		Status: contracts.StatusTimeLimitExceeded,
		Error: &contracts.RunError{
			Code:      "tle",
			Message:   message,
			Retryable: false,
		},
	}
}

func InternalErrorResult(message string, retryable bool) ExecutionResult {
	return ExecutionResult{
		Status: contracts.StatusInternalError,
		Error: &contracts.RunError{
			Code:      "internal_error",
			Message:   message,
			Retryable: retryable,
		},
	}
}
