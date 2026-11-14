# План задач: sandbox

Дата анализа: **31 октября 2026**. Локальный HEAD: `6d0c805`; анализ также учитывает рабочее дерево на 2026-10-31.

## Текущее состояние

Репозиторий фактически присутствует локально вопреки корневой карте. Есть go-judge, Python/Go adapters, Kafka retry/DLQ, object-storage suites, Kubernetes manifests и threat model. Здесь планируются работы владельца sandbox; исполняемый код и deployment сейчас не меняются.

Проверенные опорные файлы: `README.md`, `docs/kafka-contracts.md`, `docs/security-hardening.md`, `internal/kafka/processor.go`, `deployments/k8s`, `configs/languages.yaml`.

## Граница приватного результата

Для пилота предпочтителен stdin/stdout grader: expected output остаётся у tasks; sandbox возвращает наблюдаемый результат по внутреннему защищённому контракту. Если tasks нужен stdout hidden case для сравнения, он доступен только этому внутреннему потребителю; публичные diagnostics и обычные логи его не содержат. Redaction не должна уничтожить данные до доверенного сравнения. Пользовательское решение не получает expected outputs или доступ к управляющим файлам grader.

## Правила выполнения

- Оценка по договорённости: **1 SP ≈ 1 рабочий день; 2 SP ≈ 2 рабочих дня одного разработчика**, включая проверку и review. Это предварительная оценка, не гарантия календарного срока.
- Сначала реализовать весь описанный в карточке функциональный срез, затем написать/обновить тесты последним шагом. Без TDD; общий e2e — после интеграции. Уже существующие проверки не выключать.
- Статус всех карточек: **запланировано**, код по этому backlog ещё не изменён. Наличие исходников и тестов не означает подтверждённую работоспособность.
- Обязательные зависимости указаны явно; рабочая очередь и расчёт срока — в [общем плане](../tasks.md). F0–F4 — технические этапы, а PILOT/PROD/LATER — границы выпуска.
- При смене контракта: согласовать производителя и потребителей, сгенерировать код, обеспечить совместимый rollout. Миграции только append-only; секреты/hidden tests/исходники решений в обычные логи не писать.
- Задачу больше двух дней после уточнения делить на отдельно принимаемые части, сохраняя ID родителя как ссылку. Не скрывать дополнительный объём внутри пункта «и всё остальное».

Всего: **9 задач / 16 SP**; обязательный срез: **7 задач / 13 SP**.

<a id="sbx-01"></a>

## [sandbox] Implement the agreed execution envelope

- **ID:** SBX-01
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P1 — обязательная функция.
- **Зависит от:** [TSK-01](../tasks/tasks.md#tsk-01).
- **Где менять:** `internal/kafka/codec.go`, `api/schemas`, `docs/kafka-contracts.md`.
- **Готово, когда:** Golden request от tasks исполняется и результат декодируется обеими сторонами.

**Шаги:**

1. Сопоставить существующие modes/adapters с согласованным контрактом tasks.
2. Поддержать совместимый переход схем, submission key и task version/suite fingerprint.
3. Возвращать полный результат с однозначными case IDs.
4. **Проверка после готовности функционала:** contract fixtures Python/Go, invalid schema и unknown language.

<a id="sbx-02"></a>

## [sandbox] Fence duplicate execution attempts

- **ID:** SBX-02
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P1 — обязательная функция.
- **Зависит от:** [SBX-01](#sbx-01).
- **Где менять:** `internal/kafka/processor.go`, `docs/storage-model.md`.
- **Готово, когда:** Один attempt не исполняется параллельно двумя workers.

**Шаги:**

1. Проверить существующее durable idempotency хранилище и закрепить claim/fencing для attempt.
2. Повтор после рестарта отдаёт сохранённый terminal result без повторного выполнения.
3. Публикация результата предшествует commit входного offset, crash восстанавливается.
4. **Проверка после готовности функционала:** crash matrix claim/run/publish/commit на реальном storage.

<a id="sbx-03"></a>

## [sandbox] Enforce immutable private suite loading

- **ID:** SBX-03
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P1 — обязательная функция.
- **Зависит от:** [SBX-01](#sbx-01), [TSK-03](../tasks/tasks.md#tsk-03).
- **Где менять:** `internal/objectstore`, `docs/test-source-provider.md`.
- **Готово, когда:** Runner загружает именно закреплённый suite, чужие объекты недоступны.

**Шаги:**

1. Использовать существующий object-storage provider с обязательными version/checksum, без latest для graded.
2. Разделить credentials чтения suites и записи artifacts, запретить произвольные bucket/path.
3. Ограничить total bytes/files, traversal и decompression.
4. **Проверка после готовности функционала:** checksum mismatch, path traversal и oversized manifests.

<a id="sbx-04"></a>

## [sandbox] Separate hidden results from public diagnostics

- **ID:** SBX-04
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P1 — обязательная функция.
- **Зависит от:** [SBX-03](#sbx-03).
- **Где менять:** `internal/execution/sanitizer.go`, `internal/kafka/producer.go`.
- **Готово, когда:** Hidden данные не попадают в доступный пользователю artifact или operator log.

**Шаги:**

1. Маркировать visibility кейса и применять redaction до публикации пользовательских diagnostics.
2. Не прикладывать private suite, stdout скрытых тестов или секреты в обычный DLQ.
3. Подробные артефакты хранить с отдельным ACL и TTL.
4. **Проверка после готовности функционала:** echo-input, compile path, malicious error и DLQ leak сценарии.

<a id="sbx-05"></a>

## [sandbox] Validate production runtime isolation

- **ID:** SBX-05
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P0 — блокер корректности/безопасности.
- **Зависит от:** [INF-01](../infra/tasks.md#inf-01).
- **Где менять:** `deployments/k8s`, `docs/threat-model.md`, `docs/security-hardening.md`.
- **Готово, когда:** Есть проверяемый профиль изоляции для выбранного окружения, не просто privileged dev Compose.

**Шаги:**

1. На отдельном worker node настроить выбранную go-judge isolation boundary и resource limits.
2. Проверить network deny, metadata/host access, pids/filesystem/output quotas и cleanup.
3. Зафиксировать допустимые привилегии runtime и исключить обычный application node.
4. **Проверка после готовности функционала:** существующий security smoke плюс fork/output/network/host-access cases.

<a id="sbx-06"></a>

## [sandbox] Bound retries and dead-letter recovery

- **ID:** SBX-06
- **Оценка:** 1 SP (1 рабочий день)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P1 — обязательная функция.
- **Зависит от:** [SBX-02](#sbx-02), [TSK-05](../tasks/tasks.md#tsk-05).
- **Где менять:** `internal/kafka/retries.go`, `internal/kafka/dlq.go`.
- **Готово, когда:** DLQ не оставляет пользовательское решение бесконечно queued.

**Шаги:**

1. Согласовать retry budget внутри attempt с tasks deadline.
2. Разделить deterministic errors и временную инфраструктуру, exhausted возвращает terminal infra result.
3. Добавить ACL-защищённый replay с аудитом и новым attempt по команде tasks.
4. **Проверка после готовности функционала:** broker failure, exhausted retry и безопасный replay.

<a id="sbx-07"></a>

## [sandbox] Pin language runtimes and drain workers

- **ID:** SBX-07
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F3 — выпуск и эксплуатация; **контур:** PROD — дополнение к полному продакшену.
- **Приоритет:** P2 — последующее улучшение.
- **Зависит от:** [SBX-05](#sbx-05).
- **Где менять:** `configs/languages.yaml`, `deployments/docker`, `deployments/k8s`.
- **Готово, когда:** Обновление runtime не меняет уже запущенную проверку и не теряет jobs.

**Шаги:**

1. Закрепить image digest и фактическую версию Python/Go в language manifest.
2. При rollout перестать принимать jobs, дождаться bounded execution или безопасно вернуть lease.
3. Вывести capacity/queue metrics для существующего HPA, задать лимиты.
4. **Проверка после готовности функционала:** live execution во время drain и resource saturation.

<a id="sbx-08"></a>

## [sandbox] Expire execution artifacts safely

- **ID:** SBX-08
- **Оценка:** 1 SP (1 рабочий день)
- **Этап:** F3 — выпуск и эксплуатация; **контур:** PROD — дополнение к полному продакшену.
- **Приоритет:** P2 — последующее улучшение.
- **Зависит от:** [SBX-04](#sbx-04), [DOC-05](../docs/tasks.md#doc-05).
- **Где менять:** `internal/objectstore`, `docs/operations.md`.
- **Готово, когда:** Нет бесконечного роста временных файлов и восстановления удалённых данных.

**Шаги:**

1. Добавить lifecycle/GC для workspace и artifacts с учётом активных attempts.
2. Удалять пользовательские артефакты по deletion event.
3. Защитить referenced private suites от раннего удаления.
4. **Проверка после готовности функционала:** crash cleanup и retention boundary тесты.

<a id="sbx-09"></a>

## [sandbox] Protect grading from untrusted solution code

- **ID:** SBX-09
- **Оценка:** 2 SP (2 рабочих дня)
- **Этап:** F1 — надёжная основа; **контур:** PILOT — обязательный срез.
- **Приоритет:** P0 — блокер корректности/безопасности.
- **Зависит от:** [SBX-03](#sbx-03).
- **Где менять:** `internal/languages/python`, `internal/languages/go`, `internal/execution`.
- **Готово, когда:** Решение не может получить accepted отключением тестов или подделкой отчёта.

**Шаги:**

1. Проверить границу между solution и harness: запрещён доступ к expected data, управляющим файлам и подмена exit/report.
2. Для пилота предпочесть stdin/stdout execution и доверенное сравнение в tasks; custom test harness включать только после проверки изоляции.
3. При protocol corruption возвращать checker_error, не passed.
4. **Проверка после готовности функционала:** malicious solution fixtures: overwrite harness, fake report, exit zero и read expected.
