package kafka

import "context"

type IdempotencyStore interface {
	Seen(ctx context.Context, attemptID string) (bool, error)
	MarkSeen(ctx context.Context, attemptID string) error
}
