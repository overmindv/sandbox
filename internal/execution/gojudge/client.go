package gojudge

import (
	"bytes"
	"context"
	"crypto/tls"
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
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
	"overmindv/sandbox/internal/languages/jvm"
	"overmindv/sandbox/internal/languages/native"
)

var (
	ErrProtocol       = errors.New("go-judge protocol corruption")
	ErrInfrastructure = errors.New("go-judge infrastructure error")
	ErrRequestFailed  = errors.New("go-judge request failed")
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	transport  *http.Transport
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
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"))
	if err != nil {
		return nil, errors.New("invalid go-judge base_url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("go-judge base_url must be an absolute HTTP URL")
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("go-judge base_url requires a host and cannot contain credentials, query or fragment")
	}
	var tlsConfig *tls.Config
	if cfg.TLS.Enabled() {
		if parsed.Scheme != "https" {
			return nil, errors.New("go-judge mTLS requires https")
		}
		tlsConfig, err = loadTLS(cfg.TLS)
		if err != nil {
			return nil, err
		}
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
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        maxIdle,
		MaxIdleConnsPerHost: maxIdle,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Client{
		baseURL:   parsed,
		transport: transport,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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

	return c.runOnce(ctx, body)
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
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 64*1024*1024+1))
	if err := decoder.Decode(&results); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response", ErrProtocol)
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrProtocol
	}
	return results, nil
}

func (c *Client) endpoint(path string) string {
	next := *c.baseURL
	next.Path = strings.TrimRight(next.Path, "/") + path
	return next.String()
}

type Executor struct {
	client   *Client
	compiler *Client
}

func NewExecutor(cfg config.GoJudgeClient) (*Executor, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	executor := &Executor{client: client}
	if strings.TrimSpace(cfg.CompilerURL) != "" {
		compiler, err := NewClient(config.GoJudgeClient{
			BaseURL: cfg.CompilerURL, RequestTimeoutMS: int(goCompileBudget / time.Millisecond),
			MaxIdleConnections: cfg.MaxIdleConnections, TLS: cfg.TLS,
		})
		if err != nil {
			return nil, fmt.Errorf("configure compiler: %w", err)
		}
		if compiler.baseURL.String() == client.baseURL.String() {
			return nil, errors.New("compiler and runner must use separate endpoints")
		}
		executor.compiler = compiler
	}
	return executor, nil
}

func (e *Executor) Execute(ctx context.Context, command execution.ExecutionCommand) (execution.ExecutionResult, error) {
	if e == nil || e.client == nil {
		return execution.ExecutionResult{}, fmt.Errorf("%w: executor is nil", ErrInfrastructure)
	}
	request, err := RequestFromExecutionCommand(command)
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	if adapter, ok := jvm.Lookup(command.Adapter); ok {
		if e.compiler == nil {
			return execution.ExecutionResult{}, fmt.Errorf("%w: %s compiler is not configured", ErrInfrastructure, adapter.Label())
		}
		return e.executeCompiledJVM(ctx, adapter, command, request)
	}
	if adapter, ok := native.Lookup(command.Adapter); ok {
		if e.compiler == nil {
			return execution.ExecutionResult{}, fmt.Errorf("%w: %s compiler is not configured", ErrInfrastructure, adapter.Label())
		}
		return e.executeCompiledNative(ctx, adapter, command, request)
	}
	if e.BuildAllowance(command) != 0 {
		if !execution.IsExecutionMode(command) {
			return e.executeCompiledGoTests(ctx, command, request)
		}
		return e.executeCompiledGo(ctx, command, request)
	}
	return executeRequest(ctx, e.client, command, request)
}

func executeRequest(ctx context.Context, client *Client, command execution.ExecutionCommand, request Request) (execution.ExecutionResult, error) {
	results, err := client.Run(ctx, request)
	if errors.Is(err, ErrProtocol) {
		return protocolErrorResult(), nil
	}
	if err != nil {
		return execution.ExecutionResult{}, err
	}
	if execution.IsExecutionMode(command) {
		if err := ValidateExecutionBatch(results, len(command.Inputs)); err != nil {
			return protocolErrorResult(), nil
		}
		return ExecutionResultFromResponseBatch(command, results), nil
	}
	if err := ValidateResponse(results); err != nil {
		return protocolErrorResult(), nil
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

// Protocol corruption не превращается в пользовательский verdict.
func protocolErrorResult() execution.ExecutionResult {
	return execution.ExecutionResult{Status: contracts.StatusInvalidRequest, Error: &contracts.RunError{Code: "checker_error", Message: "invalid execution protocol", Retryable: false}}
}
