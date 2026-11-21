# JavaScript в песочнице

Worker принимает `language.id=javascript`, `language.version=24` и
`execution.mode=execution`. Точка входа - файл `.js`, `.mjs` или `.cjs`
из переданного workspace. Если имя не указано, используется `solution.js`.
Worker запускает Node напрямую, без shell и установки npm-пакетов.
На каждый вход выполняется отдельный запуск с обычными лимитами Sandbox.

JavaScript по умолчанию выключен. Для отдельного воркера собери образ
с Node на базе штатного go-judge:

```sh
docker build -f deployments/docker/Dockerfile.gojudge-runtime -t overmindv/sandbox-gojudge:local .
docker build -f deployments/docker/Dockerfile.gojudge-javascript -t overmindv/sandbox-gojudge-javascript:24.22.3 .
```

На таком воркере выставь `enabled: true` только для записи `javascript`
в используемом языковом манифесте. Не направляй запросы JavaScript на
обычный go-judge: в штатном образе Node нет. Публичный API пока принимает
только Python и Go.

Интеграционный тест использует отдельные локальные Kafka и go-judge:

```sh
go test -tags integration ./internal/app -run '^TestKafkaWorkerExecutesJavaScript$'
```

Укажи адреса выделенных сервисов на `127.0.0.1` через
`SANDBOX_TEST_KAFKA_PLAINTEXT` и `SANDBOX_TEST_JAVASCRIPT_GOJUDGE_URL`.
