package execution

import (
	"time"

	"overmindv/sandbox/internal/contracts"
)

// ExecutionCaseResult описывает результат исполнения одного входного кейса в execution-режиме.
type ExecutionCaseResult struct {
	Stdout          string
	Stderr          string
	Truncated       bool
	ExitCode        *int
	Signal          *string
	CPUms           int
	MemoryPeakBytes int64
	DurationMS      int
	Status          contracts.ExecutionStatus
}

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
	Cases           []ExecutionCaseResult
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

	if len(execResult.Cases) > 0 {
		return executionRunResult(req, execResult, timing, runMS, totalMS, workerID)
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

// executionRunResult строит result execution-режима: вывод и мета-данные каждого кейса.
func executionRunResult(req contracts.RunRequest, execResult ExecutionResult, timing ResultTimingInput, runMS, totalMS int, workerID string) contracts.RunResult {
	casesWithError := 0
	cases := make([]contracts.CaseExecutionResult, 0, len(execResult.Cases))
	var memoryPeak int64
	for _, run := range execResult.Cases {
		if run.Status != contracts.StatusOK {
			casesWithError++
		}
		if run.MemoryPeakBytes > memoryPeak {
			memoryPeak = run.MemoryPeakBytes
		}
		cases = append(cases, contracts.CaseExecutionResult{
			Index:           len(cases),
			Stdout:          run.Stdout,
			Stderr:          run.Stderr,
			Truncated:       run.Truncated,
			ExitCode:        run.ExitCode,
			Signal:          run.Signal,
			CPUms:           run.CPUms,
			MemoryPeakBytes: run.MemoryPeakBytes,
			DurationMS:      run.DurationMS,
			Status:          string(run.Status),
		})
	}
	status := execResult.Status
	if status == "" {
		status = contracts.StatusOK
	}
	var runErr *contracts.RunError
	if status != contracts.StatusOK {
		runErr = execResult.Error
		if runErr == nil {
			runErr = &contracts.RunError{Code: string(status), Message: string(status), Retryable: false}
		}
	}

	return contracts.RunResult{
		SchemaVersion: contracts.RunResultSchemaVersion,
		SubmissionID:  req.SubmissionID,
		AttemptID:     req.AttemptID,
		UserID:        req.UserID,
		TaskID:        req.TaskID,
		Language:      req.Language,
		Status:        status,
		Summary: contracts.ResultSummary{
			CasesTotal:     len(cases),
			CasesWithError: casesWithError,
		},
		Timing: contracts.ResultTiming{
			QueueMS:   int(timing.QueueTime.Milliseconds()),
			PrepareMS: timing.PrepareMS,
			RunMS:     runMS,
			TotalMS:   totalMS,
		},
		Resources: contracts.ResultResources{
			CPUms:           sumCPUms(execResult.Cases),
			MemoryPeakBytes: memoryPeak,
		},
		Logs:      contracts.ResultLogs{Truncated: execResult.LogsTruncated},
		Cases:     cases,
		Error:     runErr,
		TraceID:   req.TraceID,
		WorkerID:  workerID,
		CreatedAt: time.Now().UTC(),
	}
}

func sumCPUms(cases []ExecutionCaseResult) int {
	total := 0
	for _, run := range cases {
		total += run.CPUms
	}
	return total
}
