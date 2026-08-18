package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeKeyRejectsUnsafePaths(t *testing.T) {
	cases := []string{
		"",
		"/absolute/path",
		"../escape",
		"runs/../../escape",
		"C:/windows/path",
		"runs/\x00/stdout.txt",
	}

	for _, value := range cases {
		if _, err := NormalizeKey(value); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("expected invalid key for %q, got %v", value, err)
		}
	}
}

func TestFilesystemStorePutAndGet(t *testing.T) {
	root := t.TempDir()
	store, err := NewFilesystemStore(FilesystemConfig{RootDir: root, Bucket: "local"})
	if err != nil {
		t.Errorf("create filesystem store: %v", err)
		return
	}

	stored, err := store.Put(context.Background(), Object{
		Key:         "runs/run-1/attempts/attempt-1/stdout.txt",
		ContentType: "text/plain; charset=utf-8",
		Content:     []byte("hello from artifact\n"),
	})
	if err != nil {
		t.Errorf("put object: %v", err)
		return
	}
	if stored.Provider != "filesystem" {
		t.Errorf("expected filesystem provider, got %q", stored.Provider)
	}
	if stored.Bucket != "local" {
		t.Errorf("expected default bucket, got %q", stored.Bucket)
	}
	if stored.SHA256 != Digest([]byte("hello from artifact\n")) {
		t.Errorf("expected digest to match content, got %q", stored.SHA256)
	}

	content, err := store.Get(context.Background(), stored.Bucket, stored.Key)
	if err != nil {
		t.Errorf("get object: %v", err)
		return
	}
	if string(content) != "hello from artifact\n" {
		t.Errorf("expected stored content, got %q", string(content))
	}

	expectedPath := filepath.Join(root, "local", "runs", "run-1", "attempts", "attempt-1", "stdout.txt")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("expected object file at %s: %v", expectedPath, err)
	}
}

func TestS3StorePutUsesSignedPathStyleRequest(t *testing.T) {
	var seenPath string
	var seenAuth string
	var seenHash string
	var seenBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		seenHash = r.Header.Get("X-Amz-Content-Sha256")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		seenBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3Store(S3Config{
		Bucket:           "artifacts",
		Endpoint:         server.URL,
		Region:           "us-east-1",
		AccessKeyID:      "access-key",
		SecretAccessKey:  "secret-key",
		RequestTimeoutMS: 1000,
		ForcePathStyle:   true,
	})
	if err != nil {
		t.Errorf("create s3 store: %v", err)
		return
	}
	store.now = func() time.Time {
		return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	}

	payload := []byte("artifact body")
	stored, err := store.Put(context.Background(), Object{
		Key:         "runs/run-1/attempts/attempt-1/stdout.txt",
		ContentType: "text/plain",
		Content:     payload,
	})
	if err != nil {
		t.Errorf("put s3 object: %v", err)
		return
	}

	if seenPath != "/artifacts/runs/run-1/attempts/attempt-1/stdout.txt" {
		t.Errorf("expected path-style s3 path, got %q", seenPath)
	}
	if !strings.HasPrefix(seenAuth, "AWS4-HMAC-SHA256 Credential=access-key/20260819/us-east-1/s3/aws4_request") {
		t.Errorf("expected sigv4 authorization header, got %q", seenAuth)
	}
	if seenHash != Digest(payload) {
		t.Errorf("expected payload hash header, got %q", seenHash)
	}
	if !bytes.Equal(seenBody, payload) {
		t.Errorf("expected request body %q, got %q", string(payload), string(seenBody))
	}
	if stored.Provider != "s3" || stored.Bucket != "artifacts" || stored.Key == "" {
		t.Errorf("unexpected stored object metadata: %#v", stored)
	}
}
