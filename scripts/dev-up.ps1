param(
    [switch]$WithUI,
    [switch]$WithPrometheus,
    [switch]$WithObjectStorage,
    [switch]$WithGoJudgeHost,
    [switch]$NoBuild
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
. (Join-Path $PSScriptRoot "env.ps1")

Push-Location $repoRoot
try {
    Import-SandboxEnvFile -Root $repoRoot | Out-Null
    $composeArgs = @(Get-SandboxComposeArgs -Root $repoRoot)
    if ($WithGoJudgeHost) {
        $composeArgs += @("-f", "docker-compose.yml", "-f", "docker-compose.gojudge-host.yml")
    }
    if ($WithUI) {
        $composeArgs += @("--profile", "ui")
    }
    if ($WithPrometheus) {
        $composeArgs += @("--profile", "observability")
    }
    if ($WithObjectStorage) {
        $composeArgs += @("--profile", "object-storage")
    }

    $upArgs = $composeArgs + @("up", "-d")
    if (-not $NoBuild) {
        $upArgs += "--build"
    }

    & docker @upArgs
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose up failed with exit code $LASTEXITCODE"
    }

    $workerPort = Get-SandboxEnvOrDefault "SANDBOX_WORKER_HEALTH_PORT" "8080"

    Write-Host ""
    Write-Host "Sandbox local stack is starting."
    Write-Host "Worker health:   http://localhost:$workerPort/healthz"
    Write-Host "Worker readiness:http://localhost:$workerPort/readyz"
    if ($WithGoJudgeHost) {
        $goJudgePort = Get-SandboxEnvOrDefault "SANDBOX_GOJUDGE_HOST_PORT" "5050"
        $compilerPort = Get-SandboxEnvOrDefault "SANDBOX_GOJUDGE_COMPILER_HOST_PORT" "5051"
        Write-Host "go-judge:        http://127.0.0.1:$goJudgePort/version"
        Write-Host "Go compiler:     http://127.0.0.1:$compilerPort/version"
    }
    if ($WithUI) {
        $uiPort = Get-SandboxEnvOrDefault "SANDBOX_REDPANDA_CONSOLE_PORT" "8088"
        Write-Host "Kafka UI:        http://localhost:$uiPort"
    }
    if ($WithPrometheus) {
        $promPort = Get-SandboxEnvOrDefault "SANDBOX_PROMETHEUS_PORT" "9091"
        Write-Host "Prometheus:      http://localhost:$promPort"
    }
    if ($WithObjectStorage) {
        $minioPort = Get-SandboxEnvOrDefault "SANDBOX_MINIO_API_PORT" "9000"
        $minioConsolePort = Get-SandboxEnvOrDefault "SANDBOX_MINIO_CONSOLE_PORT" "9001"
        Write-Host "MinIO API:       http://localhost:$minioPort"
        Write-Host "MinIO Console:   http://localhost:$minioConsolePort"
    }
} finally {
    Pop-Location
}
