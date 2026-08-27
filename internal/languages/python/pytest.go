package python

import (
	"encoding/json"
	"fmt"
	"strings"

	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

const PytestReportSchemaVersion = "sandbox.pytest.report.v1"

type PytestResultInput struct {
	Raw execution.RawExecutionResult
}

type pytestReport struct {
	SchemaVersion string       `json:"schema_version"`
	ExitStatus    int          `json:"exitstatus"`
	GeneratedAt   int64        `json:"generated_at"`
	Tests         []pytestCase `json:"tests"`
}

type pytestCase struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int    `json:"duration_ms"`
	Message    string `json:"message"`
}

func PytestArgv(base []string, testEntrypoint string) []string {
	if testEntrypoint == "" {
		testEntrypoint = DefaultTestEntrypoint
	}

	argv := append([]string(nil), base...)
	if len(argv) == 0 {
		argv = []string{"python3", "-m", "pytest", "-q", "--disable-warnings", "--tb=short", "-p", "_sandbox_pytest_plugin"}
	}
	return append(argv, testEntrypoint)
}

func ParsePytestResult(input PytestResultInput) execution.ExecutionResult {
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
		return withError(result, contracts.StatusTimeLimitExceeded, "tle", "python execution exceeded the time limit", false)
	}
	if raw.MemoryExceeded {
		return withError(result, contracts.StatusMemoryLimitExceeded, "mle", "python execution exceeded the memory limit", false)
	}
	if raw.OutputExceeded {
		return withError(result, contracts.StatusOutputLimitExceeded, "ole", "python execution exceeded the output limit", false)
	}

	report, hasReport, reportErr := decodePytestReport(raw.ReportFiles)
	reportExitStatus := raw.ExitCode
	if hasReport && reportErr == nil {
		reportExitStatus = report.ExitStatus
		applyPytestReport(&result, report)
	} else if !hasReport {
		applyPytestSummary(&result, raw.Stdout+"\n"+raw.Stderr)
	}

	output := result.Stdout + "\n" + result.Stderr + "\n" + testcaseMessages(result.TestCases)
	if looksLikeSyntaxError(output) {
		return withError(result, contracts.StatusCompileError, "syntax_error", "python source or tests failed to compile", false)
	}
	if reportErr != nil {
		return withError(result, contracts.StatusRuntimeError, "pytest_report_error", reportErr.Error(), false)
	}

	if hasErroredTests(result.TestCases) {
		return withError(result, contracts.StatusRuntimeError, "pytest_error", "pytest reported test collection or runtime errors", false)
	}
	if hasFailedTests(result.TestCases) || result.TestsFailed > 0 {
		result.Status = contracts.StatusFailed
		return result
	}
	if reportExitStatus == 0 {
		result.Status = contracts.StatusPassed
		return result
	}

	return withError(result, contracts.StatusRuntimeError, "runtime_error", "python tests exited with non-zero status", false)
}

func decodePytestReport(files map[string][]byte) (pytestReport, bool, error) {
	if len(files) == 0 {
		return pytestReport{}, false, nil
	}
	data, ok := files[ReportPath]
	if !ok {
		return pytestReport{}, false, nil
	}

	var report pytestReport
	if err := json.Unmarshal(data, &report); err != nil {
		return pytestReport{}, true, fmt.Errorf("parse pytest report %q: %w", ReportPath, err)
	}
	if report.SchemaVersion != "" && report.SchemaVersion != PytestReportSchemaVersion {
		return pytestReport{}, true, fmt.Errorf("pytest report schema_version must be %q", PytestReportSchemaVersion)
	}
	return report, true, nil
}

func applyPytestReport(result *execution.ExecutionResult, report pytestReport) {
	result.TestCases = make([]contracts.TestCaseResult, 0, len(report.Tests))
	for _, item := range report.Tests {
		status := normalizeCaseStatus(item.Status)
		testcase := contracts.TestCaseResult{
			Name:       item.Name,
			Status:     status,
			DurationMS: item.DurationMS,
			Message:    item.Message,
		}
		result.TestCases = append(result.TestCases, testcase)
		result.TestsTotal++
		if status == "passed" {
			result.TestsPassed++
			continue
		}
		if status == "failed" || status == "error" {
			result.TestsFailed++
			if result.FirstFailedTest == "" {
				result.FirstFailedTest = testcase.Name
			}
		}
	}
}

func applyPytestSummary(result *execution.ExecutionResult, output string) {
	passed := summaryCount(output, "passed")
	failed := summaryCount(output, "failed")
	errors := summaryCount(output, "error") + summaryCount(output, "errors")

	result.TestsPassed = passed
	result.TestsFailed = failed + errors
	result.TestsTotal = result.TestsPassed + result.TestsFailed
	if failed > 0 {
		result.TestCases = append(result.TestCases, contracts.TestCaseResult{Name: "pytest::summary", Status: "failed"})
		result.FirstFailedTest = "pytest::summary"
	}
	if errors > 0 {
		result.TestCases = append(result.TestCases, contracts.TestCaseResult{Name: "pytest::summary", Status: "error"})
		if result.FirstFailedTest == "" {
			result.FirstFailedTest = "pytest::summary"
		}
	}
}

func summaryCount(output string, label string) int {
	fields := strings.Fields(output)
	for i := 1; i < len(fields); i++ {
		current := strings.Trim(strings.ToLower(fields[i]), ",.;:")
		if current != label {
			continue
		}

		var count int
		if _, err := fmt.Sscanf(fields[i-1], "%d", &count); err == nil {
			return count
		}
	}
	return 0
}

func normalizeCaseStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed", "failed", "skipped", "error":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "error"
	}
}

func testcaseMessages(testcases []contracts.TestCaseResult) string {
	var builder strings.Builder
	for _, testcase := range testcases {
		if testcase.Message == "" {
			continue
		}
		builder.WriteString(testcase.Message)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func looksLikeSyntaxError(output string) bool {
	output = strings.ToLower(output)
	markers := []string{
		"syntaxerror",
		"indentationerror",
		"taberror",
	}
	for _, marker := range markers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func hasFailedTests(testcases []contracts.TestCaseResult) bool {
	for _, testcase := range testcases {
		if testcase.Status == "failed" {
			return true
		}
	}
	return false
}

func hasErroredTests(testcases []contracts.TestCaseResult) bool {
	for _, testcase := range testcases {
		if testcase.Status == "error" {
			return true
		}
	}
	return false
}

func withError(result execution.ExecutionResult, status contracts.ExecutionStatus, code string, message string, retryable bool) execution.ExecutionResult {
	result.Status = status
	result.Error = &contracts.RunError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
	return result
}
