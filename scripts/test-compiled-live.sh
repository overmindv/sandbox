#!/bin/sh
set -eu

cd "$(dirname -- "$0")/.."
case "${1:-}" in
    cpp)
        profile=cpp
        runner_image=${SANDBOX_CPP_RUNNER_IMAGE:-overmindv/sandbox-gojudge:local}
        compiler_image=${SANDBOX_CPP_COMPILER_IMAGE:-overmindv/sandbox-gojudge-cpp:14.2.0}
        binary=${SANDBOX_CPP_TEST_BINARY:-}
        worker_binary=${SANDBOX_CPP_WORKER_TEST_BINARY:-}
        count=${SANDBOX_CPP_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_CPP_INTEGRATION
        executor_tests='^TestLiveGoJudge(CPP|C17|SQLite)$'
        worker_tests='^TestLiveWorkerExecutes(CPP|C17)$'
        ;;
    java)
        profile=java
        runner_image=${SANDBOX_JAVA_RUNNER_IMAGE:-overmindv/sandbox-gojudge-java:21.0.13.1}
        compiler_image=${SANDBOX_JAVA_COMPILER_IMAGE:-overmindv/sandbox-gojudge-java-compiler:21.0.13.1}
        binary=${SANDBOX_JAVA_TEST_BINARY:-}
        worker_binary=${SANDBOX_JAVA_WORKER_TEST_BINARY:-}
        count=${SANDBOX_JAVA_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_JAVA_INTEGRATION
        executor_tests='^TestLiveGoJudgeJava$'
        worker_tests='^TestLiveWorkerExecutesJava$'
        ;;
    kotlin)
        profile=kotlin
        runner_image=${SANDBOX_KOTLIN_RUNNER_IMAGE:-overmindv/sandbox-gojudge-kotlin:2.4.21}
        compiler_image=${SANDBOX_KOTLIN_COMPILER_IMAGE:-overmindv/sandbox-gojudge-kotlin-compiler:2.4.21}
        binary=${SANDBOX_KOTLIN_TEST_BINARY:-}
        worker_binary=${SANDBOX_KOTLIN_WORKER_TEST_BINARY:-}
        count=${SANDBOX_KOTLIN_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_KOTLIN_INTEGRATION
        executor_tests='^TestLiveGoJudgeKotlin$'
        worker_tests='^TestLiveWorkerExecutesKotlin$'
        ;;
    typescript)
        profile=typescript
        runner_image=${SANDBOX_TYPESCRIPT_RUNNER_IMAGE:-overmindv/sandbox-gojudge-typescript:7.0.2}
        compiler_image=${SANDBOX_TYPESCRIPT_COMPILER_IMAGE:-overmindv/sandbox-gojudge-typescript-compiler:7.0.2}
        binary=${SANDBOX_TYPESCRIPT_TEST_BINARY:-}
        worker_binary=${SANDBOX_TYPESCRIPT_WORKER_TEST_BINARY:-}
        count=${SANDBOX_TYPESCRIPT_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_TYPESCRIPT_INTEGRATION
        executor_tests='^TestLiveGoJudgeTypeScript$'
        worker_tests='^TestLiveWorkerExecutesTypeScript$'
        ;;
    csharp)
        profile=csharp
        runner_image=${SANDBOX_CSHARP_RUNNER_IMAGE:-overmindv/sandbox-gojudge-csharp:10.0.15}
        compiler_image=${SANDBOX_CSHARP_COMPILER_IMAGE:-overmindv/sandbox-gojudge-csharp-compiler:10.0.404}
        binary=${SANDBOX_CSHARP_TEST_BINARY:-}
        worker_binary=${SANDBOX_CSHARP_WORKER_TEST_BINARY:-}
        count=${SANDBOX_CSHARP_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_CSHARP_INTEGRATION
        executor_tests='^TestLiveGoJudgeCSharp$'
        worker_tests='^TestLiveWorkerExecutesCSharp$'
        ;;
    swift)
        profile=swift
        runner_image=${SANDBOX_SWIFT_RUNNER_IMAGE:-overmindv/sandbox-gojudge-swift:6.4.3}
        compiler_image=${SANDBOX_SWIFT_COMPILER_IMAGE:-overmindv/sandbox-gojudge-swift-compiler:6.4.3}
        binary=${SANDBOX_SWIFT_TEST_BINARY:-}
        worker_binary=${SANDBOX_SWIFT_WORKER_TEST_BINARY:-}
        count=${SANDBOX_SWIFT_TEST_COUNT:-1}
        integration=SANDBOX_GOJUDGE_SWIFT_INTEGRATION
        executor_tests='^TestLiveGoJudgeSwift$'
        worker_tests='^TestLiveWorkerExecutesSwift$'
        ;;
    *)
        printf 'Usage: %s cpp|java|kotlin|typescript|csharp|swift\n' "$0" >&2
        exit 2
        ;;
esac

work=$(mktemp -d)
repo_dir=$PWD
output_dir=$work
if [ -n "${MSYSTEM:-}" ]; then
    export MSYS_NO_PATHCONV=1
    repo_dir=$(pwd -W)
    output_dir=$(cd "$work" && pwd -W)
fi
network="sandbox-$profile-$(basename "$work" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]')"
runner=
compiler=
broker=
broker_addr=
network_created=false
cleanup() {
    if [ -n "$broker" ]; then docker stop "$broker" >/dev/null 2>&1 || :; fi
    if [ -n "$compiler" ]; then docker stop "$compiler" >/dev/null 2>&1 || :; fi
    if [ -n "$runner" ]; then docker stop "$runner" >/dev/null 2>&1 || :; fi
    if [ "$network_created" = true ]; then docker network rm "$network" >/dev/null 2>&1 || :; fi
    rm -f -- "$work/gojudge.test" "$work/worker.test"
    rmdir -- "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ -z "$binary" ]; then
    binary="$output_dir/gojudge.test"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$binary" ./internal/execution/gojudge
fi
if [ -z "$worker_binary" ]; then
    worker_binary="$output_dir/worker.test"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$worker_binary" ./internal/app
fi

docker network create --internal "$network" >/dev/null
network_created=true
start_judge() {
    alias=$1
    image=$2
    docker run -d --rm --pull never --network "$network" --network-alias "$alias" \
        --privileged --read-only --cpus 2 --memory 1g --shm-size 256m \
        --tmpfs /tmp:size=256m --tmpfs /w:size=128m "$image" \
        -http-addr 0.0.0.0:5050 -parallelism 2 -container-cred-start 10000 \
        -tmp-fs-param size=128m,nr_inodes=4096 -output-limit 33554432 \
        -copy-out-limit 1048576 -file-timeout 2m -no-fallback
}
runner=$(start_judge runner "$runner_image")
compiler=$(start_judge compiler "$compiler_image")
for judge in "$runner" "$compiler"; do
    tries=0
    until docker exec "$judge" curl -fsS http://127.0.0.1:5050/version >/dev/null 2>&1; do
        tries=$((tries + 1))
        if [ "$tries" -eq 20 ]; then
            printf '%s\n' 'go-judge did not become ready' >&2
            exit 1
        fi
        sleep 1
    done
done

if [ "$profile" = swift ]; then
    docker exec "$runner" test ! -e /usr/bin/swiftc
    docker exec "$runner" test ! -e /usr/bin/swift
    docker exec "$runner" test -f /usr/lib/swift/linux/libswiftCore.so
    docker exec "$compiler" /usr/bin/swiftc --version | grep -F 'Swift version 6.4.3'
    broker_addr=kafka:9092
    broker=$(docker run -d --rm --network "$network" --network-alias kafka \
        --cpus 1 --memory 768m --tmpfs /swift-kafka:size=128m,uid=1000,gid=1000 \
        --env CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk \
        --env KAFKA_NODE_ID=1 --env KAFKA_PROCESS_ROLES=broker,controller \
        --env KAFKA_CONTROLLER_QUORUM_VOTERS=1@kafka:9093 \
        --env KAFKA_LISTENERS=PLAINTEXT://:9092,CONTROLLER://:9093 \
        --env KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://kafka:9092 \
        --env KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
        --env KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER --env KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT \
        --env KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 --env KAFKA_OFFSETS_TOPIC_NUM_PARTITIONS=1 \
        --env KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
        --env KAFKA_AUTO_CREATE_TOPICS_ENABLE=false --env KAFKA_HEAP_OPTS="-Xms128m -Xmx256m" \
        --env KAFKA_LOG_DIRS=/swift-kafka apache/kafka:3.9.1)
    tries=0
    until docker exec "$broker" /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server "$broker_addr" >/dev/null 2>&1; do
        tries=$((tries + 1))
        if [ "$tries" -eq 10 ]; then
            docker logs --tail 50 "$broker" >&2
            exit 1
        fi
        sleep 1
    done
elif [ "$profile" = csharp ]; then
    docker exec "$runner" test ! -d /usr/local/share/dotnet/sdk
    docker exec "$runner" test ! -x /usr/local/bin/sandbox-csharp-compile
    dotnet=/usr/local/share/dotnet/dotnet
    runtimes=$(docker exec "$runner" "$dotnet" --list-runtimes)
    printf '%s\n' "$runtimes" | grep -q '^Microsoft\.NETCore\.App 10\.0\.15 '
    docker exec "$compiler" "$dotnet" exec /usr/local/share/dotnet/sdk/10.0.404/Roslyn/bincore/csc.dll -version
elif [ "$profile" = typescript ]; then
    docker exec "$runner" test ! -e /usr/local/share/sandbox-ts/compiler
    docker exec "$runner" test ! -x /usr/local/bin/sandbox-ts-compile
    [ "$(docker exec "$runner" /usr/local/bin/node --version)" = "v24.22.3" ]
    [ "$(docker exec "$compiler" /usr/local/share/sandbox-ts/compiler/lib/tsc --version)" = "Version 7.0.2" ]
elif [ "$profile" = kotlin ]; then
    docker exec "$runner" test ! -x /usr/bin/javac
    docker exec "$runner" test ! -x /usr/local/bin/sandbox-kotlin-compile
    docker exec "$runner" test ! -e /usr/local/share/sandbox/kotlin-compiler.jar
    docker exec "$runner" test -f /usr/local/share/sandbox/kotlin-stdlib.jar
    version=$(docker exec "$compiler" /usr/bin/java -jar /usr/local/share/sandbox/kotlin-compiler.jar -version 2>&1)
    printf '%s\n' "$version" | grep -q 'kotlinc-jvm 2\.4\.21 '
elif [ "$profile" = java ]; then
    docker exec "$runner" test ! -x /usr/bin/javac
    docker exec "$runner" test ! -x /usr/local/bin/sandbox-java-compile
    [ "$(docker exec "$compiler" /usr/bin/javac --version)" = "javac 21.0.13.1" ]
else
    docker exec "$runner" test ! -x /usr/bin/g++-14
    docker exec "$runner" test ! -x /usr/bin/gcc-14
    [ "$(docker exec "$compiler" /usr/bin/g++-14 -dumpfullversion)" = "14.2.0" ]
    [ "$(docker exec "$compiler" /usr/bin/gcc-14 -dumpfullversion)" = "14.2.0" ]
fi

run_tests() {
    suite=$1
    shift
    docker run --rm --pull never --network "$network" --read-only --cap-drop ALL \
        --security-opt no-new-privileges --tmpfs /tmp:size=32m \
        --mount "type=bind,src=$suite,dst=/fixture/test,readonly" \
        --mount "type=bind,src=$repo_dir/configs,dst=/repo/configs,readonly" \
        --workdir /repo --env "$integration=1" \
        --env "SANDBOX_GOJUDGE_INTEGRATION=${SANDBOX_GOJUDGE_INTEGRATION:-0}" \
        --env SANDBOX_GOJUDGE_BASE_URL=http://runner:5050 \
        --env SANDBOX_GOJUDGE_COMPILER_URL=http://compiler:5050 \
        --env SANDBOX_SWIFT_TEST_KAFKA_BROKER="$broker_addr" \
        --entrypoint /fixture/test "$runner_image" -test.v "$@"
}

if [ "$profile" = cpp ]; then
    SANDBOX_GOJUDGE_INTEGRATION=1 run_tests "$binary" -test.run "$executor_tests" -test.count "$count"
else
    run_tests "$binary" -test.run "$executor_tests" -test.count "$count"
fi
run_tests "$worker_binary" -test.run "$worker_tests"
if [ "$profile" = swift ]; then
    SANDBOX_GOJUDGE_INTEGRATION=1 run_tests "$worker_binary" -test.run '^TestLiveWorkerBuildsGoOutsideRunTimeout$'
fi
