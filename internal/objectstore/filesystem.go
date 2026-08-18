package objectstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FilesystemConfig struct {
	RootDir string
	Bucket  string
}

type FilesystemStore struct {
	rootDir string
	bucket  string
}

func NewFilesystemStore(cfg FilesystemConfig) (*FilesystemStore, error) {
	root := strings.TrimSpace(cfg.RootDir)
	if root == "" {
		return nil, fmt.Errorf("filesystem object store root dir is required")
	}
	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, err
	}
	return &FilesystemStore{
		rootDir: absoluteRoot,
		bucket:  normalizeBucket(cfg.Bucket),
	}, nil
}

func (s *FilesystemStore) Provider() string {
	return "filesystem"
}

func (s *FilesystemStore) Put(ctx context.Context, object Object) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}

	key, err := NormalizeKey(object.Key)
	if err != nil {
		return StoredObject{}, err
	}
	bucket := normalizeBucket(object.Bucket)
	if bucket == "" {
		bucket = s.bucket
	}
	fullPath, err := s.fullPath(bucket, key)
	if err != nil {
		return StoredObject{}, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return StoredObject{}, err
	}

	temp, err := os.CreateTemp(filepath.Dir(fullPath), ".artifact-*")
	if err != nil {
		return StoredObject{}, err
	}
	tempName := temp.Name()
	_, writeErr := temp.Write(object.Content)
	closeErr := temp.Close()
	if writeErr != nil {
		_ = os.Remove(tempName)
		return StoredObject{}, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tempName)
		return StoredObject{}, closeErr
	}
	if err := os.Rename(tempName, fullPath); err != nil {
		_ = os.Remove(tempName)
		return StoredObject{}, err
	}

	contentType := NormalizeContentType(object.ContentType)
	return StoredObject{
		Provider:    s.Provider(),
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		SHA256:      Digest(object.Content),
		SizeBytes:   int64(len(object.Content)),
	}, nil
}

func (s *FilesystemStore) Get(ctx context.Context, bucket string, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalizedKey, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	normalizedBucket := normalizeBucket(bucket)
	if normalizedBucket == "" {
		normalizedBucket = s.bucket
	}
	fullPath, err := s.fullPath(normalizedBucket, normalizedKey)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(fullPath)
}

func (s *FilesystemStore) Close() error {
	return nil
}

func (s *FilesystemStore) fullPath(bucket string, key string) (string, error) {
	parts := []string{s.rootDir}
	if bucket != "" {
		bucketKey, err := NormalizeKey(bucket)
		if err != nil {
			return "", err
		}
		parts = append(parts, filepath.FromSlash(bucketKey))
	}
	parts = append(parts, filepath.FromSlash(key))

	fullPath, err := filepath.Abs(filepath.Clean(filepath.Join(parts...)))
	if err != nil {
		return "", err
	}
	rootWithSeparator := s.rootDir + string(os.PathSeparator)
	if fullPath != s.rootDir && !strings.HasPrefix(fullPath, rootWithSeparator) {
		return "", fmt.Errorf("%w: path escapes object store root", ErrInvalidKey)
	}
	return fullPath, nil
}
