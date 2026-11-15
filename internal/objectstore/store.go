package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"

	"overmindv/sandbox/internal/config"
)

var (
	ErrDisabled   = errors.New("object store is disabled")
	ErrInvalidKey = errors.New("invalid object key")
)

type Store interface {
	Provider() string
	Put(ctx context.Context, object Object) (StoredObject, error)
	Get(ctx context.Context, bucket string, key string) ([]byte, error)
	Close() error
}

type Object struct {
	Bucket      string
	Key         string
	ContentType string
	Content     []byte
	Metadata    map[string]string
}

type StoredObject struct {
	Provider    string
	Bucket      string
	Key         string
	ContentType string
	SHA256      string
	SizeBytes   int64
}

func New(cfg config.ArtifactStorageConfig) (Store, error) {
	if !cfg.Enabled || cfg.Provider == "" || cfg.Provider == "disabled" {
		return DisabledStore{}, nil
	}

	switch cfg.Provider {
	case "filesystem":
		return NewFilesystemStore(FilesystemConfig{
			RootDir: cfg.RootDir,
			Bucket:  cfg.Bucket,
		})
	case "s3":
		return NewS3Store(S3Config{
			MaxBytes:         cfg.MaxBytes,
			Bucket:           cfg.Bucket,
			Endpoint:         cfg.Endpoint,
			Region:           cfg.Region,
			AccessKeyID:      cfg.AccessKeyID,
			SecretAccessKey:  cfg.SecretAccessKey,
			RequestTimeoutMS: cfg.RequestTimeoutMS,
			ForcePathStyle:   cfg.ForcePathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported object store provider %q", cfg.Provider)
	}
}

func NormalizeKey(key string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	if normalized == "" {
		return "", fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("%w: absolute key %q", ErrInvalidKey, key)
	}
	if strings.Contains(normalized, ":") {
		return "", fmt.Errorf("%w: key must not contain volume or scheme separator", ErrInvalidKey)
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: parent segment in %q", ErrInvalidKey, key)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: unsafe segment in %q", ErrInvalidKey, key)
		}
		for _, r := range segment {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("%w: control character in key", ErrInvalidKey)
			}
		}
	}
	return cleaned, nil
}

func NormalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		return "application/octet-stream"
	}
	return contentType
}

func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func normalizeBucket(bucket string) string {
	return strings.TrimSpace(bucket)
}
