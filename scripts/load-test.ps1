param(
    [int]$TimeoutSeconds = 60,
    [string]$RequestTopic = "",
    [string]$ResultTopic = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
. (Join-Path $PSScriptRoot "env.ps1")

function ConvertTo-Base64Utf8 {
    param([Parameter(Mandatory = $true)][string]$Text)

    return [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Text))
}

function Invoke-KafkaProducer {
    param(
        [Parameter(Mandatory = $true)][string]$InputFile,
        [Parameter(Mandatory = $true)][string]$Topic
    )

    if (Test-Path ".env.local") {
        Get-Content -Path $InputFile -Encoding UTF8 | docker compose --env-file .env.local exec -T kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic $Topic --property parse.key=true --property "key.separator=|"
    } elseif (Test-Path ".env") {
        Get-Content -Path $InputFile -Encoding UTF8 | docker compose --env-file .env exec -T kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic $Topic --property parse.key=true --property "key.separator=|"
    } else {
        Get-Content -Path $InputFile -Encoding UTF8 | docker compose exec -T kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:9092 --topic $Topic --property parse.key=true --property "key.separator=|"
    }
}

Push-Location $repoRoot
try {
    Import-SandboxEnvFile -Root $repoRoot | Out-Null

    if ([string]::IsNullOrWhiteSpace($RequestTopic)) {
        $RequestTopic = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_REQUESTS" "code-execution.requests.v1"
    }
    if ([string]::IsNullOrWhiteSpace($ResultTopic)) {
        $ResultTopic = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_RESULTS" "code-execution.results.v1"
    }

    $submissionID = [guid]::NewGuid().ToString()
    $attemptID = [guid]::NewGuid().ToString()
    $userID = [guid]::NewGuid().ToString()
    $taskID = [guid]::NewGuid().ToString()
    $traceID = "local-e2e-$attemptID"

    $solution = @'
def add(a, b):
    return a + b
'@

    $tests = @'
from solution import add


def test_add():
    assert add(2, 3) == 5
'@

    $payload = [ordered]@{
        schema_version = "sandbox.run.request.v1"
        submission_id = $submissionID
        attempt_id = $attemptID
        user_id = $userID
        task_id = $taskID
        language = [ordered]@{
            id = "python"
            version = "3.12"
        }
        code = [ordered]@{
            entrypoint = "solution.py"
            files = @(
                [ordered]@{
                    path = "solution.py"
                    content_b64 = ConvertTo-Base64Utf8 $solution
                }
            )
        }
        tests = [ordered]@{
            mode = "pytest"
            source = "inline"
            entrypoint = "test_solution.py"
            files = @(
                [ordered]@{
                    path = "test_solution.py"
                    content_b64 = ConvertTo-Base64Utf8 $tests
                }
            )
        }
        limits = [ordered]@{
            cpu_ms = 2000
            wall_ms = 5000
            memory_mb = 128
            pids = 32
            stdout_bytes = 32768
            stderr_bytes = 32768
            workspace_bytes = 1048576
        }
        metadata = [ordered]@{
            source = "scripts/load-test.ps1"
        }
        trace_id = $traceID
        created_at = (Get-Date).ToUniversalTime().ToString("o")
    } | ConvertTo-Json -Depth 20 -Compress

    New-Item -ItemType Directory -Force -Path ".tmp" | Out-Null
    $requestFile = Join-Path ".tmp" "local-e2e-request.txt"
    Set-Content -Path $requestFile -Value "$submissionID|$payload" -Encoding UTF8

    Write-Host "Sending job:"
    Write-Host "  submission_id: $submissionID"
    Write-Host "  attempt_id:    $attemptID"
    Write-Host "  request_topic: $RequestTopic"

    Invoke-KafkaProducer -InputFile $requestFile -Topic $RequestTopic
    if ($LASTEXITCODE -ne 0) {
        throw "kafka-console-producer failed with exit code $LASTEXITCODE"
    }

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $composeArgs = @(Get-SandboxComposeArgs -Root $repoRoot)
        $consumerArgs = $composeArgs + @(
            "exec",
            "-T",
            "kafka",
            "/opt/kafka/bin/kafka-console-consumer.sh",
            "--bootstrap-server",
            "localhost:9092",
            "--topic",
            $ResultTopic,
            "--from-beginning",
            "--timeout-ms",
            "5000",
            "--property",
            "print.key=true",
            "--property",
            "key.separator=|"
        )

        $previousErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        try {
            $lines = & docker @consumerArgs 2>&1
        } finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        foreach ($line in $lines) {
            $text = [string]$line
            if (-not $text.StartsWith("$submissionID|")) {
                continue
            }

            $resultJson = $text.Substring($submissionID.Length + 1)
            $result = $resultJson | ConvertFrom-Json
            Write-Host ""
            Write-Host "Received result from ${ResultTopic}:"
            $result | ConvertTo-Json -Depth 20

            if ($result.status -ne "passed") {
                exit 2
            }
            exit 0
        }

        Start-Sleep -Seconds 1
    }

    throw "Timed out waiting for result with submission_id=$submissionID in topic $ResultTopic"
} finally {
    Pop-Location
}
