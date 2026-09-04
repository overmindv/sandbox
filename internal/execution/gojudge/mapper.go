package gojudge

import (
	"fmt"
	"strings"
	"time"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	golang "overmindv/sandbox/internal/languages/go"
	"overmindv/sandbox/internal/languages/python"
)

func ExecutionResultFromResponse(command execution.ExecutionCommand, response Result) execution.ExecutionResult {
	switch response.Status {
	case StatusFileError:
		return goJudgeErrorResult(command, response, contracts.StatusInternalError, "gojudge_file_error", false)
	case StatusInternalError:
		return goJudgeErrorResult(command, response, contracts.StatusInternalError, "gojudge_internal_error", true)
	case StatusSignalled, StatusDangerousSyscall:
		raw := RawExecutionResult(command, response)
		return genericExecutionResult(response, raw)
	}

	raw := RawExecutionResult(command, response)
	if command.Adapter == python.AdapterName && command.TestMode == contracts.TestModePytest {
		return python.ParsePytestResult(python.PytestResultInput{Raw: raw})
	}
	if command.Adapter == golang.AdapterName && command.TestMode == contracts.TestModeGoTest {
		return golang.ParseGoTestResult(golang.GoTestResultInput{Raw: raw})
	}
	return genericExecutionResult(response, raw)
}

func RawExecutionResult(command execution.ExecutionCommand, response Result) execution.RawExecutionResult {
	files := response.Files
	reportFiles := make(map[string][]byte, len(command.CopyOut))
	for _, copyOut := range command.CopyOut {
		content, ok := lookupFile(files, copyOut)
		if !ok {
			continue
		}
		reportFiles[copyOut] = []byte(content)
	}

	return execution.RawExecutionResult{
		ExitCode:        response.ExitStatus,
		Stdout:          files[stdoutName],
		Stderr:          files[stderrName],
		ReportFiles:     reportFiles,
		TimedOut:        response.Status == StatusTimeLimitExceeded,
		MemoryExceeded:  response.Status == StatusMemoryLimitExceeded,
		OutputExceeded:  response.Status == StatusOutputLimitExceeded,
		CPUms:           nanosToMillis(response.Time),
		MemoryPeakBytes: response.Memory,
		RunMS:           nanosToMillis(response.RunTime),
	}
}

func genericExecutionResult(response Result, raw execution.RawExecutionResult) execution.ExecutionResult {
	exitCode := response.ExitStatus
	result := execution.ExecutionResult{
		Stdout:          raw.Stdout,
		Stderr:          raw.Stderr,
		LogsTruncated:   raw.OutputExceeded,
		CPUms:           raw.CPUms,
		MemoryPeakBytes: raw.MemoryPeakBytes,
		ExitCode:        &exitCode,
		RunMS:           raw.RunMS,
	}

	switch response.Status {
	case StatusAccepted:
		result.Status = contracts.StatusPassed
	case StatusTimeLimitExceeded:
		result = withGoJudgeError(result, contracts.StatusTimeLimitExceeded, "tle", "go-judge reported time limit exceeded", false)
	case StatusMemoryLimitExceeded:
		result = withGoJudgeError(result, contracts.StatusMemoryLimitExceeded, "mle", "go-judge reported memory limit exceeded", false)
	case StatusOutputLimitExceeded:
		result = withGoJudgeError(result, contracts.StatusOutputLimitExceeded, "ole", "go-judge reported output limit exceeded", false)
	case StatusNonzeroExitStatus:
		result = withGoJudgeError(result, contracts.StatusRuntimeError, "nonzero_exit", "process exited with non-zero status", false)
	case StatusSignalled:
		result = withGoJudgeError(result, contracts.StatusRuntimeError, "signalled", "process was terminated by signal", false)
	case StatusDangerousSyscall:
		result = withGoJudgeError(result, contracts.StatusRuntimeError, "dangerous_syscall", "process was blocked by sandbox syscall policy", false)
	default:
		result = withGoJudgeError(result, contracts.StatusInternalError, "gojudge_unknown_status", "go-judge returned unknown status "+response.Status, true)
	}
	return result
}

func goJudgeErrorResult(command execution.ExecutionCommand, response Result, status contracts.ExecutionStatus, code string, retryable bool) execution.ExecutionResult {
	raw := RawExecutionResult(command, response)
	exitCode := response.ExitStatus
	message := strings.TrimSpace(response.Error)
	if message == "" {
		message = strings.TrimSpace(fileErrorMessage(response.FileError))
	}
	if message == "" {
		message = "go-judge reported " + response.Status
	}
	return execution.ExecutionResult{
		Status:          status,
		Stdout:          raw.Stdout,
		Stderr:          raw.Stderr,
		LogsTruncated:   raw.OutputExceeded,
		CPUms:           raw.CPUms,
		MemoryPeakBytes: raw.MemoryPeakBytes,
		ExitCode:        &exitCode,
		RunMS:           raw.RunMS,
		Error: &contracts.RunError{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
	}
}

func withGoJudgeError(result execution.ExecutionResult, status contracts.ExecutionStatus, code string, message string, retryable bool) execution.ExecutionResult {
	result.Status = status
	result.Error = &contracts.RunError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
	return result
}

func lookupFile(files map[string]string, name string) (string, bool) {
	if len(files) == 0 {
		return "", false
	}
	if value, ok := files[name]; ok {
		return value, true
	}
	optionalName := optionalCopyOut(name)
	value, ok := files[optionalName]
	return value, ok
}

func fileErrorMessage(errors []FileError) string {
	if len(errors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(errors))
	for _, item := range errors {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			message = item.Type
		}
		if item.Name != "" {
			message = item.Name + ": " + message
		}
		parts = append(parts, message)
	}
	return strings.Join(parts, "; ")
}

func nanosToMillis(value int64) int {
	if value <= 0 {
		return 0
	}
	return int((time.Duration(value) + time.Millisecond - 1) / time.Millisecond)
}

func ValidateResponse(results []Result) error {
	if len(results) == 0 {
		return fmt.Errorf("go-judge returned no command results")
	}
	if len(results) > 1 {
		return fmt.Errorf("go-judge returned %d command results for a single command", len(results))
	}
	return nil
}

// ValidateExecutionBatch проверяет, что go-judge вернул ровно N результатов исполнения кейсов.
func ValidateExecutionBatch(results []Result, expected int) error {
	if len(results) != expected {
		return fmt.Errorf("go-judge returned %d results, expected %d", len(results), expected)
	}
	return nil
}

// ExecutionResultFromResponseBatch отображает результаты исполнения всех входных кейсов
// в execution-режиме: вывод и мета-данные каждого кейса без сравнения с эталоном.
func ExecutionResultFromResponseBatch(command execution.ExecutionCommand, results []Result) execution.ExecutionResult {
	cases := make([]execution.ExecutionCaseResult, 0, len(results))
	var memoryPeak int64
	status := contracts.StatusOK
	for i, response := range results {
		run := caseExecutionResult(response, i)
		if run.Status != contracts.StatusOK && severity(run.Status) > severity(status) {
			status = run.Status
		}
		if run.MemoryPeakBytes > memoryPeak {
			memoryPeak = run.MemoryPeakBytes
		}
		cases = append(cases, run)
	}
	result := execution.ExecutionResult{
		Status:          status,
		MemoryPeakBytes: memoryPeak,
		RunMS:           nanosToMillis(results[0].RunTime),
		Cases:           cases,
	}
	if status != contracts.StatusOK {
		result.Error = &contracts.RunError{Code: caseErrorCode(status), Message: string(status), Retryable: false}
	}
	return result
}

// caseExecutionResult отображает один go-judge результат в доменный результат кейса.
func caseExecutionResult(response Result, index int) execution.ExecutionCaseResult {
	exit := response.ExitStatus
	run := execution.ExecutionCaseResult{
		Stdout:          response.Files[stdoutName],
		Stderr:          response.Files[stderrName],
		Truncated:       response.Status == StatusOutputLimitExceeded,
		ExitCode:        &exit,
		CPUms:           nanosToMillis(response.Time),
		MemoryPeakBytes: response.Memory,
		DurationMS:      nanosToMillis(response.RunTime),
		Status:          caseResultStatus(response),
	}
	if response.Status == StatusSignalled && response.Error != "" {
		signal := response.Error
		run.Signal = &signal
	}
	return run
}

// caseResultStatus сводит go-judge статус к доменному статусу кейса.
func caseResultStatus(response Result) contracts.ExecutionStatus {
	switch response.Status {
	case StatusAccepted:
		return contracts.StatusOK
	case StatusTimeLimitExceeded:
		return contracts.StatusTimeLimitExceeded
	case StatusMemoryLimitExceeded:
		return contracts.StatusMemoryLimitExceeded
	case StatusOutputLimitExceeded:
		return contracts.StatusOutputLimitExceeded
	case StatusFileError, StatusInternalError:
		return contracts.StatusInternalError
	default: // NonzeroExitStatus, Signalled, DangerousSyscall
		return contracts.StatusRuntimeError
	}
}

// severity задаёт значимость статуса для агрегации: выше — серьёзнее.
func severity(status contracts.ExecutionStatus) int {
	switch status {
	case contracts.StatusTimeLimitExceeded:
		return 5
	case contracts.StatusMemoryLimitExceeded:
		return 4
	case contracts.StatusOutputLimitExceeded:
		return 3
	case contracts.StatusInternalError:
		return 2
	case contracts.StatusRuntimeError:
		return 1
	default:
		return 0
	}
}

// caseErrorCode возвращает машинный код ошибки агрегированного статуса.
func caseErrorCode(status contracts.ExecutionStatus) string {
	switch status {
	case contracts.StatusTimeLimitExceeded:
		return "tle"
	case contracts.StatusMemoryLimitExceeded:
		return "mle"
	case contracts.StatusOutputLimitExceeded:
		return "ole"
	case contracts.StatusInternalError:
		return "internal_error"
	default:
		return "runtime_error"
	}
}
