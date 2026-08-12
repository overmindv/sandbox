package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

type HealthState struct {
	workerID    string
	environment string
	startedAt   time.Time
	ready       atomic.Bool
}

func NewHealthState(cfg config.Config) *HealthState {
	return &HealthState{
		workerID:    cfg.App.WorkerID,
		environment: cfg.App.Environment,
		startedAt:   time.Now().UTC(),
	}
}

func (s *HealthState) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *HealthState) Ready() bool {
	return s.ready.Load()
}

type HealthServer struct {
	addr     string
	state    *HealthState
	logger   *observability.Logger
	server   *http.Server
	listener net.Listener
	errCh    chan error
	mu       sync.RWMutex
}

func NewHealthServer(addr string, state *HealthState, logger *observability.Logger) *HealthServer {
	return &HealthServer{
		addr:   addr,
		state:  state,
		logger: logger,
		errCh:  make(chan error, 1),
	}
}

func (s *HealthServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/livez", s.withTrace(http.HandlerFunc(s.handleLive)))
	mux.Handle("/healthz", s.withTrace(http.HandlerFunc(s.handleHealth)))
	mux.Handle("/readyz", s.withTrace(http.HandlerFunc(s.handleReady)))

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	s.mu.Lock()
	s.listener = listener
	s.server = server
	s.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errCh <- err
			return
		}
		s.errCh <- nil
	}()

	s.logger.Info(ctx, "health server started", "addr", listener.Addr().String())
	return nil
}

func (s *HealthServer) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	server := s.server
	s.mu.RUnlock()
	if server == nil {
		return nil
	}

	err := server.Shutdown(ctx)
	select {
	case serveErr := <-s.errCh:
		if err == nil {
			err = serveErr
		}
	default:
	}
	return err
}

func (s *HealthServer) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *HealthServer) withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := strings.TrimSpace(r.Header.Get(observability.TraceIDHeader))
		ctx := observability.WithTraceID(r.Context(), traceID)
		w.Header().Set(observability.TraceIDHeader, observability.TraceIDFromContext(ctx))
		s.logger.Debug(ctx, "health request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *HealthServer) handleLive(w http.ResponseWriter, r *http.Request) {
	s.writeStatus(w, r, http.StatusOK, "ok")
}

func (s *HealthServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeStatus(w, r, http.StatusOK, "ok")
}

func (s *HealthServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.state.Ready() {
		s.writeStatus(w, r, http.StatusServiceUnavailable, "not_ready")
		return
	}
	s.writeStatus(w, r, http.StatusOK, "ready")
}

func (s *HealthServer) writeStatus(w http.ResponseWriter, r *http.Request, statusCode int, status string) {
	traceID := observability.TraceIDFromContext(r.Context())
	response := map[string]any{
		"status":      status,
		"ready":       s.state.Ready(),
		"worker_id":   s.state.workerID,
		"environment": s.state.environment,
		"uptime_ms":   time.Since(s.state.startedAt).Milliseconds(),
		"trace_id":    traceID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error(r.Context(), "write health response failed", "error", err)
	}
}
