package gojudge

import (
	"encoding/json"
	"fmt"
)

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

// Для Accepted exitStatus обязателен: нулевое значение отсутствующего поля не является доказательством успеха.
func (r *Result) UnmarshalJSON(data []byte) error {
	type wire Result
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Status == StatusAccepted {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		exit, ok := fields["exitStatus"]
		if !ok || string(exit) == "null" {
			return fmt.Errorf("accepted response lacks exit status")
		}
		var files map[string]json.RawMessage
		if err := json.Unmarshal(fields["files"], &files); err != nil {
			return fmt.Errorf("accepted response lacks output")
		}
		stdout, ok := files["stdout"]
		if !ok || string(stdout) == "null" {
			return fmt.Errorf("accepted response lacks stdout")
		}

	}
	*r = Result(value)
	return nil
}
