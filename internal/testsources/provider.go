package testsources

import (
	"context"
	"errors"
	"fmt"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/objectstore"
)

var (
	ErrObjectStorageTestsDisabled = errors.New("object storage tests are disabled")
	ErrUnsupportedTestSource      = errors.New("unsupported test source")
	ErrInvalidSuite               = errors.New("invalid test suite")
	ErrChecksumMismatch           = errors.New("checksum mismatch")
)

type SourceError struct {
	Reason    string
	Retryable bool
	Err       error
}

func (e SourceError) Error() string {
	if e.Err == nil {
		return e.Reason
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Err)
}

func (e SourceError) Unwrap() error {
	return e.Err
}

type Provider interface {
	Resolve(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error)
}

type Resolver struct {
	inline        InlineProvider
	objectStorage *ObjectStorageProvider
}

func NewResolver(objectStorage *ObjectStorageProvider) Provider {
	return Resolver{
		inline:        InlineProvider{},
		objectStorage: objectStorage,
	}
}

func NewProviderFromConfig(cfg config.TestsConfig) (Provider, error) {
	var objectStorage *ObjectStorageProvider
	if cfg.ObjectStorage.Enabled {
		store, err := objectstore.New(config.ArtifactStorageConfig{
			Enabled:          true,
			Provider:         cfg.ObjectStorage.Provider,
			Bucket:           cfg.ObjectStorage.Bucket,
			Endpoint:         cfg.ObjectStorage.Endpoint,
			Region:           cfg.ObjectStorage.Region,
			AccessKeyID:      cfg.ObjectStorage.AccessKeyID,
			SecretAccessKey:  cfg.ObjectStorage.SecretAccessKey,
			KeyPrefix:        cfg.ObjectStorage.KeyPrefix,
			MaxBytes:         cfg.ObjectStorage.MaxBytes,
			RequestTimeoutMS: cfg.ObjectStorage.RequestTimeoutMS,
			ForcePathStyle:   cfg.ObjectStorage.ForcePathStyle,
		})
		if err != nil {
			return nil, err
		}
		objectStorage = NewObjectStorageProvider(cfg.ObjectStorage, store)
	}

	return NewResolver(objectStorage), nil
}

func (r Resolver) Resolve(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error) {
	switch req.Tests.Source {
	case contracts.TestSourceInline:
		return r.inline.Resolve(ctx, req)
	case contracts.TestSourceObjectStorage:
		if r.objectStorage == nil {
			return contracts.RunRequest{}, SourceError{
				Reason:    "object_storage_tests_disabled",
				Retryable: false,
				Err:       ErrObjectStorageTestsDisabled,
			}
		}
		return r.objectStorage.Resolve(ctx, req)
	default:
		return contracts.RunRequest{}, SourceError{
			Reason:    "unsupported_test_source",
			Retryable: false,
			Err:       fmt.Errorf("%w: %s", ErrUnsupportedTestSource, req.Tests.Source),
		}
	}
}
