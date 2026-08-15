.PHONY: dev-up dev-up-ui dev-up-observability dev-down topics e2e test

dev-up:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1

dev-up-ui:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -WithUI

dev-up-observability:
	powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -WithPrometheus

dev-down:
	docker compose down

topics:
	powershell -ExecutionPolicy Bypass -File scripts/kafka-create-topics.ps1

e2e:
	powershell -ExecutionPolicy Bypass -File scripts/load-test.ps1

test:
	go test ./internal/contracts ./internal/config ./internal/observability ./internal/app ./internal/kafka ./internal/scheduler ./internal/security ./internal/execution ./internal/execution/fake ./internal/execution/gojudge ./internal/execution/isolate ./internal/languages/... ./cmd/sandbox-worker
