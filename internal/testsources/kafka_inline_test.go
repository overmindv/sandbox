package testsources

import (
	"context"
	"errors"
	"testing"

	"overmindv/sandbox/internal/contracts"
)

func TestInlineProviderResolvesRequest(t *testing.T) {
	req := validObjectStorageRunRequest()
	req.Tests = contracts.TestSpec{
		Mode:   contracts.TestModePytest,
		Source: contracts.TestSourceInline,
		Files: []contracts.SourceFile{
			{
				Path:       "test_solution.py",
				ContentB64: "YXNzZXJ0IFRydWUK",
			},
		},
	}

	resolved, err := InlineProvider{}.Resolve(context.Background(), req)
	if err != nil {
		t.Errorf("resolve inline tests: %v", err)
		return
	}
	if resolved.AttemptID != req.AttemptID {
		t.Errorf("expected attempt_id %q, got %q", req.AttemptID, resolved.AttemptID)
	}
	if resolved.Tests.Source != contracts.TestSourceInline {
		t.Errorf("expected inline source, got %q", resolved.Tests.Source)
	}
}

func TestInlineProviderRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := InlineProvider{}.Resolve(ctx, validObjectStorageRunRequest())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
