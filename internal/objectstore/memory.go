package objectstore

import (
	"context"
	"fmt"
	"sync"
)

type MemoryStore struct {
	mu      sync.RWMutex
	objects map[string]Object
	bucket  string
}

func NewMemoryStore(bucket string) *MemoryStore {
	return &MemoryStore{
		objects: make(map[string]Object),
		bucket:  normalizeBucket(bucket),
	}
}

func (s *MemoryStore) Provider() string {
	return "memory"
}

func (s *MemoryStore) Put(ctx context.Context, object Object) (StoredObject, error) {
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
	contentType := NormalizeContentType(object.ContentType)

	cloned := Object{
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		Content:     append([]byte(nil), object.Content...),
		Metadata:    cloneMetadata(object.Metadata),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[objectMapKey(bucket, key)] = cloned
	return StoredObject{
		Provider:    s.Provider(),
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		SHA256:      Digest(cloned.Content),
		SizeBytes:   int64(len(cloned.Content)),
	}, nil
}

func (s *MemoryStore) Get(ctx context.Context, bucket string, key string) ([]byte, error) {
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

	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[objectMapKey(normalizedBucket, normalizedKey)]
	if !ok {
		return nil, fmt.Errorf("object %q not found", normalizedKey)
	}
	return append([]byte(nil), object.Content...), nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func objectMapKey(bucket string, key string) string {
	return bucket + "\x00" + key
}

func cloneMetadata(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
