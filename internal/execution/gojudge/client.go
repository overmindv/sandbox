package gojudge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/execution"
)

var (
	ErrInfrastructure = errors.New("go-judge infrastructure error")
	ErrRequestFailed  = errors.New("go-judge request failed")
)

type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	maxAttempts int
	backoff     time.Duration
}

type HTTPError struct {
	StatusCode int
	Body       string
	Retryable  bool
}

func (e HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("go-judge http status %d", e.StatusCode)
	}
	return fmt.Sprintf("go-judge http status %d: %s", e.StatusCode, e.Body)
}

func NewClient(cfg config.GoJudgeClient) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("go-judge base_url is required")
	}
	parsed, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse go-judge base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("go-judge base_url must use http or https")
	}

	timeout := time.Duration(cfg.RequestTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	maxIdle := cfg.MaxIdleConnections
	if maxIdle <= 0 {
		maxIdle = 8
	}

	transport := &http.Transport{
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdle,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Client{
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		maxAttempts: 2,
		backoff:     50 * time.Millisecond,
	}, nil
}

func (c *Client) Run(ctx context.Context, request Request) ([]Result, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: client is nil", ErrInfrastructure)
	}
	if len(request.Cmd) == 0 {
		return nil, fmt.Errorf("%w: at least one command is required", ErrRequestFailed)
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrRequestFailed, err)
	}

	var lastErr error
	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		results, err := c.runOnce(ctx, body)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if ctx.Err() != nil || attempt == attempts || !IsRetryableInfrastructureError(err) {
			break
		}
		if err := sleepContext(ctx, c.backoff*time.Duration(attempt)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) Healthy(ctx context.Context) bool {
	if c == nil {
		return false
	}
	endpoint := c.endpoint("/version")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

func (c *Client) runOnce(ctx context.Context, body []byte) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/run"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build run request: %v", ErrRequestFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrInfrastructure, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		httpErr := HTTPError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			Retryable:  resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
		}
		if httpErr.Retryable {
			return nil, fmt.Errorf("%w: %w", ErrInfrastructure, httpErr)
		}
		return nil, fmt.Errorf("%w: %w", ErrRequestFailed, httpErr)
	}

	var results []Result
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&results); err != nil {
		return nil, fmt.Errorf("%w: decode run response: %v", ErrInfrastructure, err)
	}
	return results, nil
}

func (c *Client) endpoint(path string) string {
	next := *c.baseURL
	next.Path = strings.TrimRight(next.Path, "/") + path
	return next.String()
}

type Executor struct {
	client *Client
}

func NewExecutor(cfg config.GoJudgeClient) (*Executor, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Executor{client: client}, nil
}

func (e *Executor) Execute(ctx context.Context, command execution.ExecutionCommand) (execution.ExecutionResult, error) {
	if e == nil || e.client == nil {
		return execution.ExecutionResult{}, fmt.Errorf("%w: executor is nil", ErrInfrastructure)
	}
	request, err := RequestFromExecutionCommand(command)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	results, err := e.client.Run(ctx, request)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	if err := ValidateResponse(results); err != nil {
		return execution.ExecutionResult{}, fmt.Errorf("%w: %v", ErrInfrastructure, err)
	}
	return ExecutionResultFromResponse(command, results[0]), nil
}

func (e *Executor) Healthy() bool {
	if e == nil || e.client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return e.client.Healthy(ctx)
}

func IsRetryableInfrastructureError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRequestFailed) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrInfrastructure) {
		return true
	}

	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ execution.Executor = (*Executor)(nil)
