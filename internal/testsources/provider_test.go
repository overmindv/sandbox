package testsources

import (
	"context"
	"errors"
	"testing"

	"overmindv/sandbox/internal/contracts"
)

var (
	_ TestSourceProvider = InlineProvider{}
	_ TestSourceProvider = (*ObjectStorageProvider)(nil)
	_ TestSourceProvider = Resolver{}
)

func TestResolverRejectsUnsupportedTestSource(t *testing.T) {
	provider := NewResolver(nil)
	req := validObjectStorageRunRequest()
	req.Tests.Source = contracts.TestSource("database")

	_, err := provider.Resolve(context.Background(), req)
	var sourceErr SourceError
	if !errors.As(err, &sourceErr) {
		t.Errorf("expected SourceError, got %v", err)
		return
	}
	if sourceErr.Reason != "unsupported_test_source" {
		t.Errorf("expected unsupported test source reason, got %q", sourceErr.Reason)
	}
	if sourceErr.Retryable {
		t.Error("expected unsupported test source to be non-retryable")
	}
	if !errors.Is(err, ErrUnsupportedTestSource) {
		t.Errorf("expected error to wrap ErrUnsupportedTestSource, got %v", err)
	}
}
