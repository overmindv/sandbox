# Доступ Sandbox к Kafka

В `local`, `dev`, `development` и `test` можно использовать PLAINTEXT.
Для остальных окружений, включая `prod` и `staging`, Sandbox требует TLS и
учетную запись. Reader и writer используют одну и ту же настройку.

| Kafka protocol | Как подтвердить личность |
| --- | --- |
| `SSL` | mTLS: `SANDBOX_KAFKA_TLS_CERT_FILE` и `SANDBOX_KAFKA_TLS_KEY_FILE` |
| `SASL_SSL` | `SANDBOX_KAFKA_SASL_MECHANISM` со значением `SCRAM-SHA-256` или `SCRAM-SHA-512`, плюс `SANDBOX_KAFKA_SASL_USERNAME_FILE` и `SANDBOX_KAFKA_SASL_PASSWORD_FILE` |

Режим задается через `SANDBOX_KAFKA_SECURITY_PROTOCOL` или
`kafka.security.protocol` в YAML. Для CA укажите
`SANDBOX_KAFKA_TLS_CA_FILE` с PEM-цепочкой. Без него используются
системные CA. TLS начинается с версии 1.2. Имена bootstrap и всех брокеров
из `advertised.listeners` должны совпадать с SAN сертификатов. Отключения
проверки сертификата нет.

SCRAM читает имя и пароль из двух отдельных файлов, по одной строке на файл.
Завершающий LF или CRLF отбрасывается; остальные пробелы сохраняются.
Значения credentials не передаются через `.env` или YAML. При ротации
сначала обновите право на брокере и Secret, затем перезапустите worker:
файлы читаются при создании клиента.

## Kubernetes

Смонтируйте Secret `sandbox-kafka-client` в worker. Манифест уже монтирует
его только для чтения по адресу `/run/secrets/sandbox-kafka`. Secret
опционален для локального профиля; production с недоступными файлами не
запустится. В конфигурацию передавайте только имена файлов.

Пример переменных для mTLS:

```dotenv
SANDBOX_KAFKA_SECURITY_PROTOCOL=SSL
SANDBOX_KAFKA_TLS_CA_FILE=/run/secrets/sandbox-kafka/ca.pem
SANDBOX_KAFKA_TLS_CERT_FILE=/run/secrets/sandbox-kafka/client.pem
SANDBOX_KAFKA_TLS_KEY_FILE=/run/secrets/sandbox-kafka/client.key
```

В `SANDBOX_KAFKA_BROKERS` укажите TLS listener, доступный worker.
Разрешите этот порт в egress worker, не открывая доступ из go-judge.
Файлы Kafka, CA и клиентские ключи не монтируются в runtime и не входят
в запрос к go-judge. Sandbox читает requests/retry и пишет результаты,
события, повторы и DLQ. В OVM-164 эти ACL выдаются `User:sandbox`. Для mTLS настройте
на брокере сопоставление DN сертификата с principal `sandbox`; для SCRAM
используйте имя `sandbox`. Учетная запись не должна быть администратором
брокера.

## Проверка

`sh scripts/kafka-security-test.sh` поднимает временную Kafka на loopback
и создает одноразовые сертификаты. Нужны Docker Compose, OpenSSL, Python 3
и Go 1.26. Проверка охватывает mTLS, SCRAM, сохранение offset после
перезапуска worker, отклонение неверных credentials и замер публикации.
Рабочие ключи и брокеры для теста не нужны.
