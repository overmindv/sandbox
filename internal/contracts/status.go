package contracts

type ExecutionStatus string

const (
	StatusQueued              ExecutionStatus = "queued"
	StatusRunning             ExecutionStatus = "running"
	StatusPassed              ExecutionStatus = "passed"
	StatusOK                  ExecutionStatus = "ok"
	StatusFailed              ExecutionStatus = "failed"
	StatusCompileError        ExecutionStatus = "compile_error"
	StatusRuntimeError        ExecutionStatus = "runtime_error"
	StatusTimeLimitExceeded   ExecutionStatus = "tle"
	StatusMemoryLimitExceeded ExecutionStatus = "mle"
	StatusOutputLimitExceeded ExecutionStatus = "ole"
	StatusInternalError       ExecutionStatus = "internal_error"
	StatusInvalidRequest      ExecutionStatus = "invalid_request"
	StatusUnsupportedLanguage ExecutionStatus = "unsupported_language"
)

func (s ExecutionStatus) Valid() bool {
	switch s {
	case StatusQueued,
		StatusRunning,
		StatusPassed,
		StatusOK,
		StatusFailed,
		StatusCompileError,
		StatusRuntimeError,
		StatusTimeLimitExceeded,
		StatusMemoryLimitExceeded,
		StatusOutputLimitExceeded,
		StatusInternalError,
		StatusInvalidRequest,
		StatusUnsupportedLanguage:
		return true
	default:
		return false
	}
}

func (s ExecutionStatus) Terminal() bool {
	return s.Valid() && s != StatusQueued && s != StatusRunning
}
