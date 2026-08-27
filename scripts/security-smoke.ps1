param(
    [int]$TimeoutSeconds = 90,
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

function New-SmokeLimits {
    param(
        [int]$CPUms = 1000,
        [int]$WallMS = 2000,
        [int]$MemoryMB = 64,
        [int]$PIDs = 16,
        [int]$StdoutBytes = 4096,
        [int]$StderrBytes = 4096,
        [int]$WorkspaceBytes = 1048576
    )

    return [ordered]@{
        cpu_ms = $CPUms
        wall_ms = $WallMS
        memory_mb = $MemoryMB
        pids = $PIDs
        stdout_bytes = $StdoutBytes
        stderr_bytes = $StderrBytes
        workspace_bytes = $WorkspaceBytes
    }
}

function New-SmokeCase {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Solution,
        [Parameter(Mandatory = $true)][string]$Tests,
        [string]$SourcePath = "solution.py",
        [Parameter(Mandatory = $true)]$Limits,
        [Parameter(Mandatory = $true)][string[]]$AllowedStatuses
    )

    $submissionID = [guid]::NewGuid().ToString()
    $attemptID = [guid]::NewGuid().ToString()
    $payload = [ordered]@{
        schema_version = "sandbox.run.request.v1"
        submission_id = $submissionID
        attempt_id = $attemptID
        user_id = [guid]::NewGuid().ToString()
        task_id = [guid]::NewGuid().ToString()
        language = [ordered]@{
            id = "python"
            version = "3.12"
        }
        code = [ordered]@{
            entrypoint = $SourcePath
            files = @(
                [ordered]@{
                    path = $SourcePath
                    content_b64 = ConvertTo-Base64Utf8 $Solution
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
                    content_b64 = ConvertTo-Base64Utf8 $Tests
                }
            )
        }
        limits = $Limits
        metadata = [ordered]@{
            source = "scripts/security-smoke.ps1"
            security_smoke_case = $Name
        }
        trace_id = "security-smoke-$Name-$attemptID"
        created_at = (Get-Date).ToUniversalTime().ToString("o")
    } | ConvertTo-Json -Depth 20 -Compress

    return [pscustomobject]@{
        Name = $Name
        SubmissionID = $submissionID
        AttemptID = $attemptID
        Payload = $payload
        AllowedStatuses = $AllowedStatuses
    }
}

function Invoke-KafkaProducer {
    param(
        [Parameter(Mandatory = $true)][string]$InputFile,
        [Parameter(Mandatory = $true)][string]$Topic
    )

    $composeArgs = @(Get-SandboxComposeArgs -Root $repoRoot)
    $producerArgs = $composeArgs + @(
        "exec",
        "-T",
        "kafka",
        "/opt/kafka/bin/kafka-console-producer.sh",
        "--bootstrap-server",
        "localhost:9092",
        "--topic",
        $Topic,
        "--property",
        "parse.key=true",
        "--property",
        "key.separator=|"
    )

    Get-Content -Path $InputFile -Encoding UTF8 | docker @producerArgs
}

function Read-KafkaResults {
    param([Parameter(Mandatory = $true)][string]$Topic)

    $composeArgs = @(Get-SandboxComposeArgs -Root $repoRoot)
    $consumerArgs = $composeArgs + @(
        "exec",
        "-T",
        "kafka",
        "/opt/kafka/bin/kafka-console-consumer.sh",
        "--bootstrap-server",
        "localhost:9092",
        "--topic",
        $Topic,
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
        return & docker @consumerArgs 2>&1
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
}

function Test-AllowedStatus {
    param(
        [Parameter(Mandatory = $true)][string]$Actual,
        [Parameter(Mandatory = $true)][string[]]$Allowed
    )

    foreach ($status in $Allowed) {
        if ($Actual -eq $status) {
            return $true
        }
    }
    return $false
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

    $cases = @()
    $cases += New-SmokeCase `
        -Name "path_traversal" `
        -SourcePath "../solution.py" `
        -Solution "def attack():`n    return 'should not run'`n" `
        -Tests "from solution import attack`n`ndef test_path_traversal():`n    assert attack() == 'should not run'`n" `
        -Limits (New-SmokeLimits) `
        -AllowedStatuses @("invalid_request")

    $cases += New-SmokeCase `
        -Name "sleep_bomb" `
        -Solution "import time`n`ndef attack():`n    time.sleep(30)`n" `
        -Tests "from solution import attack`n`ndef test_sleep_bomb():`n    attack()`n    assert False, 'sleep bomb returned'`n" `
        -Limits (New-SmokeLimits -CPUms 1000 -WallMS 1500 -MemoryMB 64 -PIDs 16) `
        -AllowedStatuses @("tle")

    $cases += New-SmokeCase `
        -Name "memory_bomb" `
        -Solution "def attack():`n    chunks = []`n    while True:`n        chunks.append(bytearray(1024 * 1024))`n" `
        -Tests "from solution import attack`n`ndef test_memory_bomb():`n    attack()`n    assert False, 'memory bomb returned'`n" `
        -Limits (New-SmokeLimits -CPUms 2000 -WallMS 4000 -MemoryMB 32 -PIDs 16) `
        -AllowedStatuses @("mle", "runtime_error", "failed", "tle")

    $cases += New-SmokeCase `
        -Name "output_bomb" `
        -Solution "import os`n`ndef attack():`n    chunk = b'x' * 4096`n    while True:`n        os.write(1, chunk)`n" `
        -Tests "from solution import attack`n`ndef test_output_bomb():`n    attack()`n    assert False, 'output bomb returned'`n" `
        -Limits (New-SmokeLimits -CPUms 1000 -WallMS 3000 -MemoryMB 64 -PIDs 16 -StdoutBytes 4096 -StderrBytes 4096) `
        -AllowedStatuses @("ole", "tle", "mle", "runtime_error", "failed")

    $cases += New-SmokeCase `
        -Name "fork_bomb" `
        -Solution "import os`nimport time`n`ndef attack():`n    children = []`n    while True:`n        try:`n            pid = os.fork()`n            if pid == 0:`n                time.sleep(60)`n            children.append(pid)`n        except (AttributeError, OSError):`n            time.sleep(0.01)`n            return`n" `
        -Tests "from solution import attack`n`ndef test_fork_bomb():`n    attack()`n    assert False, 'fork bomb returned'`n" `
        -Limits (New-SmokeLimits -CPUms 1000 -WallMS 3000 -MemoryMB 64 -PIDs 8) `
        -AllowedStatuses @("failed", "runtime_error", "tle", "mle")

    $cases += New-SmokeCase `
        -Name "metadata_endpoint" `
        -Solution "import urllib.request`n`ndef attack():`n    return urllib.request.urlopen('http://169.254.169.254/latest/meta-data/', timeout=1).read()`n" `
        -Tests "import pytest`nfrom solution import attack`n`ndef test_metadata_endpoint_blocked():`n    with pytest.raises(Exception):`n        attack()`n" `
        -Limits (New-SmokeLimits -CPUms 1000 -WallMS 3000 -MemoryMB 64 -PIDs 16) `
        -AllowedStatuses @("passed")

    New-Item -ItemType Directory -Force -Path ".tmp" | Out-Null
    $requestFile = Join-Path ".tmp" "security-smoke-requests.txt"
    $requestLines = @()
    foreach ($case in $cases) {
        $requestLines += "$($case.SubmissionID)|$($case.Payload)"
    }
    Set-Content -Path $requestFile -Value $requestLines -Encoding UTF8

    Write-Host "Sending $($cases.Count) security smoke jobs to $RequestTopic"
    Invoke-KafkaProducer -InputFile $requestFile -Topic $RequestTopic
    if ($LASTEXITCODE -ne 0) {
        throw "kafka-console-producer failed with exit code $LASTEXITCODE"
    }

    $remaining = @{}
    foreach ($case in $cases) {
        $remaining[$case.SubmissionID] = $case
    }

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline -and $remaining.Count -gt 0) {
        $lines = Read-KafkaResults -Topic $ResultTopic
        foreach ($line in $lines) {
            $text = [string]$line
            $separator = $text.IndexOf("|")
            if ($separator -le 0) {
                continue
            }

            $submissionID = $text.Substring(0, $separator)
            if (-not $remaining.ContainsKey($submissionID)) {
                continue
            }

            $resultJson = $text.Substring($separator + 1)
            try {
                $result = $resultJson | ConvertFrom-Json
            } catch {
                continue
            }

            $case = $remaining[$submissionID]
            $status = [string]$result.status
            if (-not (Test-AllowedStatus -Actual $status -Allowed $case.AllowedStatuses)) {
                Write-Error "Security smoke $($case.Name) got unexpected status '$status'. Allowed: $($case.AllowedStatuses -join ', ')"
                exit 2
            }

            Write-Host "Security smoke $($case.Name): $status"
            $remaining.Remove($submissionID)
        }

        if ($remaining.Count -gt 0) {
            Start-Sleep -Seconds 1
        }
    }

    if ($remaining.Count -gt 0) {
        $missing = ($remaining.Values | ForEach-Object { $_.Name }) -join ", "
        throw "Timed out waiting for security smoke results: $missing"
    }

    Write-Host "Security smoke passed."
} finally {
    Pop-Location
}
