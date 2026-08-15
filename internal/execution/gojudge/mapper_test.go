package gojudge

import (
	"testing"

	"overmindv/sandbox/internal/contracts"
)

func TestExecutionResultFromResponseMapsGoJudgeStatuses(t *testing.T) {
	tests := []struct {
		name       string
		response   Result
		wantStatus contracts.ExecutionStatus
		wantCode   string
	}{
		{
			name: "accepted with pytest report",
			response: Result{
				Status:     StatusAccepted,
				ExitStatus: 0,
				Time:       1_000_000,
				RunTime:    2_000_000,
				Memory:     8 * 1024 * 1024,
				Files: map[string]string{
					stdoutName:                    "",
					stderrName:                    "",
					".sandbox/pytest-report.json": `{"schema_version":"sandbox.pytest.report.v1","exitstatus":0,"tests":[{"name":"test_solution.py::test_add","status":"passed","duration_ms":1}]}`,
				},
			},
			wantStatus: contracts.StatusPassed,
		},
		{
			name: "nonzero pytest failure",
			response: Result{
				Status:     StatusNonzeroExitStatus,
				ExitStatus: 1,
				Files: map[string]string{
					".sandbox/pytest-report.json": `{"schema_version":"sandbox.pytest.report.v1","exitstatus":1,"tests":[{"name":"test_solution.py::test_add","status":"failed","message":"assert 1 == 2"}]}`,
				},
			},
			wantStatus: contracts.StatusFailed,
		},
		{
			name:       "time limit",
			response:   Result{Status: StatusTimeLimitExceeded, ExitStatus: 9},
			wantStatus: contracts.StatusTimeLimitExceeded,
			wantCode:   "tle",
		},
		{
			name:       "memory limit",
			response:   Result{Status: StatusMemoryLimitExceeded},
			wantStatus: contracts.StatusMemoryLimitExceeded,
			wantCode:   "mle",
		},
		{
			name:       "output limit",
			response:   Result{Status: StatusOutputLimitExceeded},
			wantStatus: contracts.StatusOutputLimitExceeded,
			wantCode:   "ole",
		},
		{
			name:       "file error",
			response:   Result{Status: StatusFileError, FileError: []FileError{{Name: "solution.py", Type: "CopyInOpenFile"}}},
			wantStatus: contracts.StatusInternalError,
			wantCode:   "gojudge_file_error",
		},
		{
			name:       "nonzero without report",
			response:   Result{Status: StatusNonzeroExitStatus, ExitStatus: 2},
			wantStatus: contracts.StatusRuntimeError,
			wantCode:   "runtime_error",
		},
		{
			name:       "signalled",
			response:   Result{Status: StatusSignalled, ExitStatus: 11},
			wantStatus: contracts.StatusRuntimeError,
			wantCode:   "signalled",
		},
		{
			name:       "internal",
			response:   Result{Status: StatusInternalError, Error: "container failed"},
			wantStatus: contracts.StatusInternalError,
			wantCode:   "gojudge_internal_error",
		},
	}

	command := validGoJudgeCommand()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExecutionResultFromResponse(command, tt.response)
			if result.Status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, result.Status)
			}
			if tt.wantCode != "" {
				if result.Error == nil || result.Error.Code != tt.wantCode {
					t.Errorf("expected error code %q, got %#v", tt.wantCode, result.Error)
				}
			}
		})
	}
}

func TestValidateResponseRequiresSingleResult(t *testing.T) {
	if err := ValidateResponse(nil); err == nil {
		t.Error("expected empty response error")
	}
	if err := ValidateResponse([]Result{{}, {}}); err == nil {
		t.Error("expected multi-result response error")
	}
	if err := ValidateResponse([]Result{{Status: StatusAccepted}}); err != nil {
		t.Errorf("expected single response to pass: %v", err)
	}
}
