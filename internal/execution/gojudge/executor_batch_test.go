package gojudge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/contracts"
)

func TestExecutorValidatesExecutionResultCardinality(t *testing.T) {
	for _, tt := range []struct {
		name      string
		execution bool
		count     int
		wantError bool
	}{
		{name: "all three input results are preserved", execution: true, count: 3},
		{name: "missing batch results", execution: true, count: 0, wantError: true},
		{name: "incomplete batch", execution: true, count: 1, wantError: true},
		{name: "extra batch result", execution: true, count: 4, wantError: true},
		{name: "legacy single command still rejects multiple results", count: 3, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request Request
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				wantCommands := 1
				if tt.execution {
					wantCommands = 3
				}
				if len(request.Cmd) != wantCommands {
					t.Errorf("sent %d commands, want %d", len(request.Cmd), wantCommands)
				}
				outputs := []string{"1\n", "2\n", "1\n", "extra\n"}
				results := make([]Result, tt.count)
				for i := range results {
					results[i] = Result{Status: StatusAccepted, Files: map[string]string{"stdout": outputs[i]}}
				}
				_ = json.NewEncoder(w).Encode(results)
			}))
			defer server.Close()
			executor, err := NewExecutor(config.GoJudgeClient{BaseURL: server.URL, RequestTimeoutMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			command := validGoJudgeCommand()
			if tt.execution {
				command.Mode = contracts.ExecutionMode
				command.Inputs = []string{"3 2", "4 4", "1 1"}
			}
			result, err := executor.Execute(context.Background(), command)
			if tt.wantError {
				if !errors.Is(err, ErrInfrastructure) {
					t.Fatalf("expected infrastructure error for mismatched cardinality, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid multi-case execution failed: %v", err)
			}
			outputs := make([]string, len(result.Cases))
			for i, item := range result.Cases {
				outputs[i] = item.Stdout
			}
			if result.Status != contracts.StatusOK || !reflect.DeepEqual(outputs, []string{"1\n", "2\n", "1\n"}) {
				t.Fatalf("batch results lost or reordered: %+v", result)
			}
		})
	}
}
