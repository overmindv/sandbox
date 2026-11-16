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
	ErrSuiteTooLarge              = errors.New("test suite too large")
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

type TestSourceProvider interface {
	Resolve(ctx context.Context, req contracts.RunRequest) (contracts.RunRequest, error)
}

type Provider = TestSourceProvider

type Resolver struct {
	inline        InlineProvider
	objectStorage *ObjectStorageProvider
}

func NewResolver(objectStorage *ObjectStorageProvider) TestSourceProvider {
	return Resolver{
		inline:        InlineProvider{},
		objectStorage: objectStorage,
	}
}

func NewProviderFromConfig(cfg config.TestsConfig) (TestSourceProvider, error) {
	var objectStorage *ObjectStorageProvider
	if cfg.ObjectStorage.Enabled {
		if cfg.ObjectStorage.Provider != "s3" {
			return nil, errors.New("suite reader requires s3 provider")
		}
		store, err := objectstore.NewS3Store(objectstore.S3Config{
			ReadOnly:         true,
			Bucket:           cfg.ObjectStorage.Bucket,
			Endpoint:         cfg.ObjectStorage.Endpoint,
			Region:           cfg.ObjectStorage.Region,
			AccessKeyID:      cfg.ObjectStorage.AccessKeyID,
			SecretAccessKey:  cfg.ObjectStorage.SecretAccessKey,
			KeyPrefix:        cfg.ObjectStorage.KeyPrefix,
			MaxBytes:         suiteByteLimit(cfg.ObjectStorage.MaxBytes),
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
	// Execution-режим не использует тест-файлы: резолв тестовых исходников не нужен.
	if req.Execution != nil && req.Execution.Mode == contracts.ExecutionMode {
		if req.Execution.Suite != nil {
			return r.objectStorage.ResolveInputs(ctx, req)
		}
		return req, nil
	}
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
