.PHONY: dev-up dev-up-ui dev-up-observability dev-up-object-storage dev-down topics e2e e2e-go security-smoke perf-bench perf-load perf-load-go test

dev-up:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1

dev-up-ui:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -WithUI

dev-up-observability:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -WithPrometheus

dev-up-object-storage:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -WithObjectStorage

dev-down:
	docker compose down

topics:
	powershell -ExecutionPolicy Bypass -File scripts/kafka-create-topics.ps1

e2e:
	powershell -ExecutionPolicy Bypass -File scripts/load-test.ps1

e2e-go:
	powershell -ExecutionPolicy Bypass -File scripts/load-test.ps1 -Language go

security-smoke:
	powershell -ExecutionPolicy Bypass -File scripts/security-smoke.ps1

perf-bench:
	go test ./internal/languages/python -run ^$$ -bench Benchmark -benchmem -count=5

perf-load:
	powershell -ExecutionPolicy Bypass -File scripts/load-test.ps1 -Jobs 20 -TimeoutSeconds 180 -RunLabel local-python-20 -ReportPath .tmp/load-test-live-20-report.json

perf-load-go:
	powershell -ExecutionPolicy Bypass -File scripts/load-test.ps1 -Language go -Jobs 20 -TimeoutSeconds 180 -RunLabel local-go-20 -ReportPath .tmp/load-test-go-live-20-report.json

test:
	go test ./deployments/k8s ./internal/objectstore ./internal/artifacts ./internal/testsources ./internal/contracts ./internal/config ./internal/observability ./internal/app ./internal/kafka ./internal/scheduler ./internal/security ./internal/execution ./internal/execution/fake ./internal/execution/gojudge ./internal/execution/isolate ./internal/languages/... ./cmd/sandbox-worker
