param(
    [string]$BootstrapServer = "localhost:9092"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
. (Join-Path $PSScriptRoot "env.ps1")

Push-Location $repoRoot
try {
    Import-SandboxEnvFile -Root $repoRoot | Out-Null

    $topics = @(
        @{ Name = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_REQUESTS" "code-execution.requests.v1"; Partitions = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_REQUEST_PARTITIONS" "6" },
        @{ Name = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_RESULTS" "code-execution.results.v1"; Partitions = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_RESULT_PARTITIONS" "6" },
        @{ Name = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_EVENTS" "code-execution.events.v1"; Partitions = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_EVENT_PARTITIONS" "6" },
        @{ Name = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_DEADLETTER" "code-execution.deadletter.v1"; Partitions = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_DLQ_PARTITIONS" "3" }
    )

    foreach ($topic in $topics) {
        Write-Host "Creating topic $($topic.Name) with $($topic.Partitions) partitions"
        $composeArgs = @(Get-SandboxComposeArgs -Root $repoRoot)
        $dockerArgs = $composeArgs + @(
            "exec",
            "-T",
            "kafka",
            "/opt/kafka/bin/kafka-topics.sh",
            "--bootstrap-server",
            $BootstrapServer,
            "--create",
            "--if-not-exists",
            "--topic",
            $topic.Name,
            "--partitions",
            $topic.Partitions,
            "--replication-factor",
            "1"
        )
        & docker @dockerArgs
        if ($LASTEXITCODE -ne 0) {
            throw "docker topic creation failed for $($topic.Name) with exit code $LASTEXITCODE"
        }
    }
} finally {
    Pop-Location
}
