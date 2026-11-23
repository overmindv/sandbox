#!/bin/sh
set -eu

cd "$(dirname -- "$0")/.."
work=$(mktemp -d)
repo_dir=$PWD
output_dir=$work
if [ -n "${MSYSTEM:-}" ]; then
    export MSYS_NO_PATHCONV=1
    repo_dir=$(pwd -W)
    output_dir=$(cd "$work" && pwd -W)
fi
network="sandbox-cpp-$(basename "$work" | tr '[:upper:]' '[:lower:]' | tr -cd '[:alnum:]')"
runner_image=${SANDBOX_CPP_RUNNER_IMAGE:-overmindv/sandbox-gojudge:local}
compiler_image=${SANDBOX_CPP_COMPILER_IMAGE:-overmindv/sandbox-gojudge-cpp:14.2.0}
runner=
compiler=
network_created=false
cleanup() {
    if [ -n "$compiler" ]; then docker stop "$compiler" >/dev/null 2>&1 || :; fi
    if [ -n "$runner" ]; then docker stop "$runner" >/dev/null 2>&1 || :; fi
    if [ "$network_created" = true ]; then docker network rm "$network" >/dev/null 2>&1 || :; fi
    rm -f -- "$work/gojudge.test" "$work/worker.test"
    rmdir -- "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ -n "${SANDBOX_CPP_TEST_BINARY:-}" ]; then
    binary=$SANDBOX_CPP_TEST_BINARY
else
    binary="$output_dir/gojudge.test"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$binary" ./internal/execution/gojudge
fi
if [ -n "${SANDBOX_CPP_WORKER_TEST_BINARY:-}" ]; then
    worker_binary=$SANDBOX_CPP_WORKER_TEST_BINARY
else
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

docker exec "$runner" test ! -x /usr/bin/g++-14
docker exec "$runner" test ! -x /usr/bin/gcc-14
[ "$(docker exec "$compiler" /usr/bin/g++-14 -dumpfullversion)" = "14.2.0" ]
[ "$(docker exec "$compiler" /usr/bin/gcc-14 -dumpfullversion)" = "14.2.0" ]

run_tests() {
    suite=$1
    shift
    docker run --rm --pull never --network "$network" --read-only --cap-drop ALL \
        --security-opt no-new-privileges --tmpfs /tmp:size=32m \
        --mount "type=bind,src=$suite,dst=/fixture/test,readonly" \
        --mount "type=bind,src=$repo_dir/configs,dst=/repo/configs,readonly" \
        --workdir /repo --env SANDBOX_GOJUDGE_CPP_INTEGRATION=1 \
        --env SANDBOX_GOJUDGE_BASE_URL=http://runner:5050 \
        --env SANDBOX_GOJUDGE_COMPILER_URL=http://compiler:5050 \
        --entrypoint /fixture/test "$runner_image" -test.v "$@"
}

run_tests "$binary" -test.run '^TestLiveGoJudge(CPP|C17)$' -test.count "${SANDBOX_CPP_TEST_COUNT:-1}"
run_tests "$worker_binary" -test.run '^TestLiveWorkerExecutes(CPP|C17)$'
