package execution

import (
	"context"
	"errors"
)

var (
	ErrUnsupportedFakeStatus = errors.New("unsupported fake execution status")
	ErrInvalidCommandPayload = errors.New("invalid execution command payload")
)

type Executor interface {
	Execute(ctx context.Context, command ExecutionCommand) (ExecutionResult, error)
	Healthy() bool
}
