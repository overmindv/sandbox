package gojudge

const (
	StatusAccepted            = "Accepted"
	StatusMemoryLimitExceeded = "Memory Limit Exceeded"
	StatusTimeLimitExceeded   = "Time Limit Exceeded"
	StatusOutputLimitExceeded = "Output Limit Exceeded"
	StatusFileError           = "File Error"
	StatusNonzeroExitStatus   = "Nonzero Exit Status"
	StatusSignalled           = "Signalled"
	StatusInternalError       = "Internal Error"
	StatusDangerousSyscall    = "Dangerous Syscall"
)

type Result struct {
	Status     string            `json:"status"`
	Error      string            `json:"error,omitempty"`
	ExitStatus int               `json:"exitStatus"`
	Time       int64             `json:"time"`
	Memory     int64             `json:"memory"`
	RunTime    int64             `json:"runTime"`
	ProcPeak   int               `json:"procPeak,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	FileIDs    map[string]string `json:"fileIds,omitempty"`
	FileError  []FileError       `json:"fileError,omitempty"`
}

type FileError struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}
