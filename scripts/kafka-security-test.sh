#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
state=$(mktemp -d)
project="sandbox-kafka-$(basename "$state" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]')"
export SANDBOX_TEST_KAFKA_SECRET_DIR="$state/client"
export SANDBOX_TEST_KAFKA_BROKER_PKI="$state/broker"

ports=$(python3 -c '
from socket import socket
sockets = [socket() for _ in range(3)]
for sock in sockets:
    sock.bind(("127.0.0.1", 0))
print(*(sock.getsockname()[1] for sock in sockets))
')
set -- $ports
export SANDBOX_TEST_PLAIN_PORT="$1" SANDBOX_TEST_MTLS_PORT="$2" SANDBOX_TEST_SCRAM_PORT="$3"
export SANDBOX_TEST_KAFKA_PLAINTEXT="127.0.0.1:$1" SANDBOX_TEST_KAFKA_MTLS="127.0.0.1:$2"
export SANDBOX_TEST_KAFKA_SCRAM="127.0.0.1:$3" SANDBOX_TEST_KAFKA_WRONG_HOST="localhost:$2"

compose() {
    docker compose --env-file /dev/null -p "$project" -f tests/kafka-security-compose.yml "$@"
}

cleanup() {
    compose down >/dev/null 2>&1 || :
    rm -rf -- "$state"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

umask 077
mkdir "$state/client" "$state/broker"
chmod 0755 "$state/broker"

make_ca() {
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
        -subj "/CN=$1 fixture CA" -addext 'basicConstraints=critical,CA:TRUE' \
        -keyout "$state/$1.key" -out "$state/$1.pem" 2>/dev/null
}

sign() {
    name=$1 ca=$2 purpose=$3
    openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$name" \
        -keyout "$state/$name.key" -out "$state/$name.csr" 2>/dev/null
    printf 'extendedKeyUsage=%s\nsubjectAltName=IP:127.0.0.1\n' "$purpose" > "$state/ext.cnf"
    openssl x509 -req -in "$state/$name.csr" -CA "$state/$ca.pem" -CAkey "$state/$ca.key" \
        -CAcreateserial -days 1 -extfile "$state/ext.cnf" -out "$state/$name.pem" 2>/dev/null
}

make_ca ca
make_ca wrong-ca
sign broker ca serverAuth
sign sandbox ca clientAuth
sign foreign-client wrong-ca clientAuth
cp "$state/ca.pem" "$state/wrong-ca.pem" "$state/sandbox.pem" "$state/sandbox.key" \
    "$state/foreign-client.pem" "$state/foreign-client.key" "$state/client/"
cat "$state/broker.key" "$state/broker.pem" "$state/ca.pem" > "$state/broker/broker.pem"
cp "$state/ca.pem" "$state/broker/ca.pem"
chmod 0644 "$state/broker/broker.pem" "$state/broker/ca.pem"
fixture=$(openssl rand -hex 24)
printf '%s\n' sandbox > "$state/client/username"
printf '%s\n' "$fixture" > "$state/client/password"
openssl rand -hex 24 > "$state/client/wrong-password"

compose up -d broker
admin() {
    tool=$1
    shift
    compose exec -T broker "/opt/kafka/bin/$tool.sh" --bootstrap-server 127.0.0.1:9092 "$@"
}

tries=0
until compose exec -T broker timeout 8 /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server 127.0.0.1:9092 --list >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -ge 10 ]; then
        printf 'Kafka security fixture did not become ready\n' >&2
        exit 1
    fi
    sleep 1
done

admin kafka-configs --alter --entity-type users --entity-name sandbox \
    --add-config "SCRAM-SHA-256=[iterations=4096,password=$fixture]" >/dev/null

for mode in mtls scram; do
    for kind in requests retry results events deadletter; do
        admin kafka-topics --create --topic "$mode.$kind.v1" --partitions 1 --replication-factor 1 >/dev/null
    done
    case "$mode" in
        mtls) principal='User:CN=sandbox' ;;
        scram) principal=User:sandbox ;;
    esac
    admin kafka-acls --add --allow-principal "$principal" --operation Read \
        --topic "$mode.requests.v1" --topic "$mode.retry.v1" --group "$mode-worker-v1" >/dev/null
    admin kafka-acls --add --allow-principal "$principal" --operation Write \
        --topic "$mode.results.v1" --topic "$mode.events.v1" --topic "$mode.retry.v1" \
        --topic "$mode.deadletter.v1" --topic bench.v1 >/dev/null
done
admin kafka-topics --create --topic bench.v1 --partitions 1 --replication-factor 1 >/dev/null

go test -tags=integration ./internal/app -run '^TestKafkaSecurity' -count=1 -v
go test -tags=integration ./internal/app -run '^$' -bench '^BenchmarkKafkaPublishSecurity$' -benchmem -benchtime=300x
