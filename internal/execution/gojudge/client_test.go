package gojudge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"overmindv/sandbox/internal/config"
)

func TestClientDoesNotRepeatAmbiguousExecution(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"status":"Accepted","exitStatus":0,"files":{"stdout":"","stderr":""}}]`))
	}))
	defer server.Close()

	client, err := NewClient(config.GoJudgeClient{BaseURL: server.URL, RequestTimeoutMS: 1000})
	if err != nil {
		t.Errorf("new client: %v", err)
		return
	}

	_, err = client.Run(context.Background(), Request{Cmd: []Command{{Args: []string{"python3"}}}})
	if !errors.Is(err, ErrInfrastructure) || attempts != 1 {
		t.Fatalf("ambiguous execution repeated: calls=%d err=%v", attempts, err)
	}

}

func TestClientDoesNotRetryRequestErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client, err := NewClient(config.GoJudgeClient{BaseURL: server.URL, RequestTimeoutMS: 1000})
	if err != nil {
		t.Errorf("new client: %v", err)
		return
	}

	_, err = client.Run(context.Background(), Request{Cmd: []Command{{Args: []string{"python3"}}}})
	if !errors.Is(err, ErrRequestFailed) {
		t.Errorf("expected request failed error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected one attempt, got %d", attempts)
	}
}

func TestClientHealthyUsesVersionEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Errorf("expected /version path, got %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"version":"test"}`))
	}))
	defer server.Close()

	client, err := NewClient(config.GoJudgeClient{BaseURL: server.URL, RequestTimeoutMS: 1000})
	if err != nil {
		t.Errorf("new client: %v", err)
		return
	}
	if !client.Healthy(context.Background()) {
		t.Error("expected healthy client")
	}
}
