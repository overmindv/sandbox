# Sandbox Worker

Локальная песочница читает `RunRequest` из Kafka, запускает Python/pytest и Go/go test через go-judge и пишет `RunResult` обратно в Kafka.

## Local Docker Compose

По умолчанию compose поднимает:

- Kafka: `localhost:29092` с внутренним адресом `kafka:9092`.
- go-judge и Go-компилятор: доступны только внутри сети Compose.
- sandbox-worker: health `http://localhost:8080/healthz`, readiness `http://localhost:8080/readyz`, metrics `http://localhost:9090/metrics`.

Опционально:

- Redpanda Console: профиль `ui`, `http://localhost:8088`.
- Prometheus: профиль `observability`, `http://localhost:9091`.

### Настройка

Для дефолтов ничего создавать не нужно. Если надо поменять порты, образы или имена топиков, используй `.env.example` как шаблон:

```powershell
Copy-Item .env.example .env.local
```

Скрипты читают локальные env-файлы в таком порядке: текущие переменные процесса, затем `.env.local`, затем `.env`, затем встроенные defaults. `.env.local` и `.env` не попадают в git.

Сервисный конфиг worker берется из `configs/local.yaml`, но compose переопределяет ключевые значения через env:

- `SANDBOX_KAFKA_BROKERS=kafka:9092`
- `SANDBOX_EXECUTOR_TYPE=gojudge`
- `SANDBOX_GOJUDGE_BASE_URL=http://gojudge:5050`
- `SANDBOX_GOJUDGE_COMPILER_URL=http://gojudge-compiler:5050`
- `SANDBOX_FEATURE_GOJUDGE_EXECUTOR=true`
- `SANDBOX_FEATURE_FAKE_EXECUTOR=false`

### Запуск

```powershell
.\scripts\dev-up.ps1
```

Для тестов с хоста можно открыть runner и компилятор на loopback:

```powershell
.\scripts\dev-up.ps1 -WithGoJudgeHost
```

С UI:

```powershell
.\scripts\dev-up.ps1 -WithUI
```

С Prometheus:

```powershell
.\scripts\dev-up.ps1 -WithPrometheus
```

With MinIO object storage:

```powershell
.\scripts\dev-up.ps1 -WithObjectStorage
```

Через Make:

```powershell
make dev-up
make dev-up-ui
make dev-up-observability
make dev-up-object-storage
```

### Topics

Compose запускает `kafka-topics` один раз при старте. Отдельно пересоздать/проверить topics можно так:

```powershell
.\scripts\kafka-create-topics.ps1
```

Создаются:

- `code-execution.requests.v1`
- `code-execution.retry.v1`
- `code-execution.results.v1`
- `code-execution.events.v1`
- `code-execution.deadletter.v1`

### End-to-End

После старта stack:

```powershell
.\scripts\load-test.ps1
.\scripts\load-test.ps1 -Language go
```

Скрипт отправляет валидный Python или Go job в `code-execution.requests.v1`, читает `code-execution.results.v1` и завершится с кодом `0`, если результат `passed`.

### Диагностика

```powershell
docker compose ps
docker compose logs -f sandbox-worker
docker compose logs -f gojudge
docker compose logs -f kafka
```

Остановка:

```powershell
docker compose down
```

С удалением локальных данных Kafka/Prometheus:

```powershell
docker compose down -v
```
