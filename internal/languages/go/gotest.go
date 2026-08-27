package golang

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

type GoTestResultInput struct {
	Raw execution.RawExecutionResult
}

type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func GoTestArgv(base []string) []string {
	if len(base) == 0 {
		return []string{"/usr/local/go/bin/go", "test", "-json", "-p", "1", "./..."}
	}
	return append([]string(nil), base...)
}

func ParseGoTestResult(input GoTestResultInput) execution.ExecutionResult {
	raw := input.Raw
	exitCode := raw.ExitCode
	result := execution.ExecutionResult{
		Stdout:          raw.Stdout,
		Stderr:          raw.Stderr,
		LogsTruncated:   raw.OutputExceeded,
		CPUms:           raw.CPUms,
		MemoryPeakBytes: raw.MemoryPeakBytes,
		ExitCode:        &exitCode,
		RunMS:           raw.RunMS,
	}

	if raw.TimedOut {
		return withGoTestError(result, contracts.StatusTimeLimitExceeded, "tle", "go test exceeded the time limit", false)
	}
	if raw.MemoryExceeded {
		return withGoTestError(result, contracts.StatusMemoryLimitExceeded, "mle", "go test exceeded the memory limit", false)
	}
	if raw.OutputExceeded {
		return withGoTestError(result, contracts.StatusOutputLimitExceeded, "ole", "go test exceeded the output limit", false)
	}

	parseErr := applyGoTestEvents(&result, raw.Stdout+"\n"+raw.Stderr)
	if parseErr != nil && raw.ExitCode == 0 {
		return withGoTestError(result, contracts.StatusRuntimeError, "go_test_report_error", parseErr.Error(), false)
	}
	if result.TestsFailed > 0 {
		result.Status = contracts.StatusFailed
		return result
	}
	if raw.ExitCode == 0 {
		result.Status = contracts.StatusPassed
		if result.TestsTotal == 0 {
			result.TestsTotal = 1
			result.TestsPassed = 1
		}
		return result
	}

	output := raw.Stdout + "\n" + raw.Stderr
	if looksLikeGoCompileError(output) {
		return withGoTestError(result, contracts.StatusCompileError, "go_compile_error", "go test failed to compile", false)
	}
	return withGoTestError(result, contracts.StatusRuntimeError, "go_test_error", "go test exited with non-zero status", false)
}

func applyGoTestEvents(result *execution.ExecutionResult, output string) error {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seen := map[string]contracts.TestCaseResult{}
	order := []string{}
	parsed := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event goTestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Test == "" {
			continue
		}
		status, ok := goTestStatus(event.Action)
		if !ok {
			continue
		}

		name := goTestName(event.Package, event.Test)
		if _, exists := seen[name]; !exists {
			order = append(order, name)
		}
		seen[name] = contracts.TestCaseResult{
			Name:       name,
			Status:     status,
			DurationMS: elapsedMS(event.Elapsed),
		}
		parsed++
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("scan go test output: %w", err)
	}
	if parsed == 0 {
		return nil
	}

	result.TestCases = make([]contracts.TestCaseResult, 0, len(order))
	for _, name := range order {
		testcase := seen[name]
		result.TestCases = append(result.TestCases, testcase)
		result.TestsTotal++
		switch testcase.Status {
		case "passed", "skipped":
			result.TestsPassed++
		case "failed", "error":
			result.TestsFailed++
			if result.FirstFailedTest == "" {
				result.FirstFailedTest = testcase.Name
			}
		}
	}
	return nil
}

func goTestStatus(action string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "pass":
		return "passed", true
	case "fail":
		return "failed", true
	case "skip":
		return "skipped", true
	default:
		return "", false
	}
}

func goTestName(pkg string, test string) string {
	pkg = strings.TrimSpace(pkg)
	test = strings.TrimSpace(test)
	if pkg == "" {
		return test
	}
	return pkg + "::" + test
}

func elapsedMS(elapsedSeconds float64) int {
	if elapsedSeconds <= 0 {
		return 0
	}
	return int(math.Ceil(elapsedSeconds * 1000))
}

func looksLikeGoCompileError(output string) bool {
	output = strings.ToLower(output)
	markers := []string{
		"[build failed]",
		"build failed",
		"undefined:",
		"syntax error",
		"expected ",
		"cannot use ",
		"imported and not used",
		"no required module",
		"found packages ",
	}
	for _, marker := range markers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func withGoTestError(result execution.ExecutionResult, status contracts.ExecutionStatus, code string, message string, retryable bool) execution.ExecutionResult {
	result.Status = status
	result.Error = &contracts.RunError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
	return result
}
