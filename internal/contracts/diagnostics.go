package contracts

// DiagnosticsVisibilityForRequest не доверяет публичности закрытого suite.
func DiagnosticsVisibilityForRequest(req RunRequest) string {
	if !req.PrivateInputs && req.Execution != nil && req.Execution.Suite == nil && req.Metadata["run_mode"] == "samples" {
		return "public"
	}
	return "private"
}

// RedactDiagnostics сохраняет stdout кейсов только в доверенном checker envelope.
// Публичный API Tasks отдельно исключает private cases по сохранённому run_mode.
func RedactDiagnostics(result RunResult) RunResult {
	if result.Error != nil {
		copy := *result.Error
		copy.Message = "execution failed"
		copy.Details = nil
		result.Error = &copy
	}
	private := result.DiagnosticsVisibility != "public"
	if private {
		result.DiagnosticsVisibility = "private"
		result.Logs = ResultLogs{}
		result.Tests = nil
		result.Summary.FirstFailedTest = ""
	}
	result.Cases = append([]CaseExecutionResult(nil), result.Cases...)
	for i := range result.Cases {
		result.Cases[i].DiagnosticsVisibility = result.DiagnosticsVisibility
		if private {
			result.Cases[i].Stderr = ""
		}
	}
	return result
}
