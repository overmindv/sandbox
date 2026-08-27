package testsources

import (
	"context"

	"overmindv/sandbox/internal/contracts"
)

type InlineProvider struct{}

func (InlineProvider) Resolve(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error) {
	if err := ctx.Err(); err != nil {
		return contracts.RunRequest{}, err
	}
	return req, nil
}
