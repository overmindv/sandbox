param(
    [int]$Jobs = 1,
    [int]$TimeoutSeconds = 120,
    [string]$RequestTopic = "",
    [string]$ResultTopic = "",
    [string]$ExpectedStatus = "passed",
    [string]$RunLabel = "local-load",
    [string]$ReportPath = "",
    [string]$FakeStatus = "",
    [ValidateSet("python", "go")]
    [string]$Language = "python",
    [int]$ConsumerTimeoutMS = 1000,
    [switch]$DryRun
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
        [Parameter(Mandatory = $true)][string]$Topic,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $composeArgs = @(Get-SandboxComposeArgs -Root $Root)
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
    param(
        [Parameter(Mandatory = $true)][hashtable]$Expected,
        [Parameter(Mandatory = $true)][string]$Topic,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds,
        [Parameter(Mandatory = $true)][int]$ConsumerTimeoutMS,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $seen = @{}
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline -and $seen.Count -lt $Expected.Count) {
        $composeArgs = @(Get-SandboxComposeArgs -Root $Root)
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
            "$ConsumerTimeoutMS",
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
            $separator = $text.IndexOf("|")
            if ($separator -lt 0) {
                continue
            }

            $submissionID = $text.Substring(0, $separator)
            if (-not $Expected.ContainsKey($submissionID) -or $seen.ContainsKey($submissionID)) {
                continue
            }

            $resultJson = $text.Substring($separator + 1)
            try {
                $result = $resultJson | ConvertFrom-Json
            } catch {
                continue
            }

            $seen[$submissionID] = [ordered]@{
                received_at = (Get-Date).ToUniversalTime().ToString("o")
                result = $result
            }
        }

        if ($seen.Count -lt $Expected.Count) {
            Start-Sleep -Milliseconds 500
        }
    }

    return $seen
}

function Get-Percentile {
    param(
        [object[]]$Values = @(),
        [Parameter(Mandatory = $true)][double]$Percentile
    )

    if ($null -eq $Values -or $Values.Count -eq 0) {
        return 0
    }

    $numbers = @($Values | ForEach-Object { [double]$_ } | Sort-Object)
    if ($numbers.Count -eq 0) {
        return 0
    }

    $index = [Math]::Ceiling(($Percentile / 100.0) * $numbers.Count) - 1
    if ($index -lt 0) {
        $index = 0
    }
    if ($index -ge $numbers.Count) {
        $index = $numbers.Count - 1
    }
    return [Math]::Round([double]$numbers[$index], 2)
}

function New-LoadTestJob {
    param(
        [Parameter(Mandatory = $true)][int]$Index,
        [Parameter(Mandatory = $true)][string]$RunID,
        [Parameter(Mandatory = $true)][string]$RunLabel,
        [Parameter(Mandatory = $true)][string]$Language,
        [string]$FakeStatus = ""
    )

    $submissionID = [guid]::NewGuid().ToString()
    $attemptID = [guid]::NewGuid().ToString()
    $userID = [guid]::NewGuid().ToString()
    $taskID = [guid]::NewGuid().ToString()
    $traceID = "load-$RunID-$Index"
    $createdAt = (Get-Date).ToUniversalTime().ToString("o")
    $expected = $Index + 1

    $languageVersion = "3.12"
    $codeEntrypoint = "solution.py"
    $testMode = "pytest"
    $testEntrypoint = "test_solution.py"
    $solutionPath = "solution.py"
    $testPath = "test_solution.py"
    $solution = "def add(a, b):`n    return a + b`n"
    $tests = "from solution import add`n`n`ndef test_add_$($Index)():`n    assert add($Index, 1) == $expected`n"
    $cpuMS = 2000
    $wallMS = 5000
    $memoryMB = 128
    $pids = 32
    $workspaceBytes = 1048576
    if ($Language -eq "go") {
        $languageVersion = "1.26"
        $codeEntrypoint = "solution.go"
        $testMode = "go-test"
        $testEntrypoint = "solution_test.go"
        $solutionPath = "solution.go"
        $testPath = "solution_test.go"
        $solution = "package solution`n`nfunc Add(a, b int) int {`n`treturn a + b`n}`n"
        $tests = "package solution`n`nimport `"testing`"`n`nfunc TestAdd$($Index)(t *testing.T) {`n`tif got := Add($Index, 1); got != $expected {`n`t`tt.Errorf(`"Add() = %d`", got)`n`t}`n}`n"
        $cpuMS = 10000
        $wallMS = 30000
        $memoryMB = 512
        $pids = 64
        $workspaceBytes = 5242880
    }

    $metadata = [ordered]@{
        source = "scripts/load-test.ps1"
        load_test_run_id = $RunID
        load_test_run_label = $RunLabel
        load_test_index = $Index
        load_test_language = $Language
    }
    if (-not [string]::IsNullOrWhiteSpace($FakeStatus)) {
        $metadata.fake_status = $FakeStatus
    }

    $payload = [ordered]@{
        schema_version = "sandbox.run.request.v1"
        submission_id = $submissionID
        attempt_id = $attemptID
        user_id = $userID
        task_id = $taskID
        language = [ordered]@{
            id = $Language
            version = $languageVersion
        }
        code = [ordered]@{
            entrypoint = $codeEntrypoint
            files = @(
                [ordered]@{
                    path = $solutionPath
                    content_b64 = ConvertTo-Base64Utf8 $solution
                }
            )
        }
        tests = [ordered]@{
            mode = $testMode
            source = "inline"
            entrypoint = $testEntrypoint
            files = @(
                [ordered]@{
                    path = $testPath
                    content_b64 = ConvertTo-Base64Utf8 $tests
                }
            )
        }
        limits = [ordered]@{
            cpu_ms = $cpuMS
            wall_ms = $wallMS
            memory_mb = $memoryMB
            pids = $pids
            stdout_bytes = 32768
            stderr_bytes = 32768
            workspace_bytes = $workspaceBytes
        }
        metadata = $metadata
        trace_id = $traceID
        created_at = $createdAt
    } | ConvertTo-Json -Depth 20 -Compress

    return [ordered]@{
        submission_id = $submissionID
        attempt_id = $attemptID
        created_at = $createdAt
        payload = $payload
    }
}

function New-Report {
    param(
        [Parameter(Mandatory = $true)][string]$RunID,
        [Parameter(Mandatory = $true)][string]$RunLabel,
        [Parameter(Mandatory = $true)][string]$Language,
        [Parameter(Mandatory = $true)][int]$Jobs,
        [Parameter(Mandatory = $true)][hashtable]$Seen,
        [Parameter(Mandatory = $true)][hashtable]$Expected,
        [Parameter(Mandatory = $true)][string]$RequestTopic,
        [Parameter(Mandatory = $true)][string]$ResultTopic,
        [Parameter(Mandatory = $true)][datetime]$StartedAt,
        [Parameter(Mandatory = $true)][datetime]$SentAt,
        [Parameter(Mandatory = $true)][datetime]$CompletedAt,
        [Parameter(Mandatory = $true)][double]$WallSeconds,
        [Parameter(Mandatory = $true)][int]$ConsumerTimeoutMS,
        [Parameter(Mandatory = $true)][bool]$DryRun
    )

    $statusCounts = [ordered]@{}
    $workerTotalMS = @()
    $queueMS = @()
    $runMS = @()
    $receiveLatencyMS = @()
    $requestTimes = @()
    $resultTimes = @()

    foreach ($submissionID in $Seen.Keys) {
        $entry = $Seen[$submissionID]
        $result = $entry.result
        $status = [string]$result.status
        if (-not $statusCounts.Contains($status)) {
            $statusCounts[$status] = 0
        }
        $statusCounts[$status]++

        if ($null -ne $result.timing) {
            $workerTotalMS += [double]$result.timing.total_ms
            $queueMS += [double]$result.timing.queue_ms
            $runMS += [double]$result.timing.run_ms
        }
        $receivedAt = [datetime]::Parse([string]$entry.received_at).ToUniversalTime()
        $receiveLatencyMS += ($receivedAt - $SentAt).TotalMilliseconds

        if ($Expected.ContainsKey($submissionID)) {
            $requestTimes += [datetime]::Parse([string]$Expected[$submissionID].created_at).ToUniversalTime()
        }
        if ($null -ne $result.created_at) {
            $resultTimes += [datetime]::Parse([string]$result.created_at).ToUniversalTime()
        }
    }

    $wallThroughput = 0
    if ($WallSeconds -gt 0) {
        $wallThroughput = [Math]::Round($Seen.Count / $WallSeconds, 2)
    }

    $pipelineWindowSeconds = 0
    $pipelineThroughput = 0
    if ($requestTimes.Count -gt 0 -and $resultTimes.Count -gt 0) {
        $firstRequestAt = ($requestTimes | Sort-Object | Select-Object -First 1)
        $lastResultAt = ($resultTimes | Sort-Object | Select-Object -Last 1)
        $pipelineWindowSeconds = ($lastResultAt - $firstRequestAt).TotalSeconds
        if ($pipelineWindowSeconds -gt 0) {
            $pipelineThroughput = [Math]::Round($Seen.Count / $pipelineWindowSeconds, 2)
        }
    }

    return [ordered]@{
        schema_version = "sandbox.load_test.report.v1"
        run_id = $RunID
        run_label = $RunLabel
        dry_run = $DryRun
        request_topic = $RequestTopic
        result_topic = $ResultTopic
        language = $Language
        jobs_requested = $Jobs
        jobs_received = $Seen.Count
        jobs_missing = $Expected.Count - $Seen.Count
        started_at = $StartedAt.ToUniversalTime().ToString("o")
        sent_at = $SentAt.ToUniversalTime().ToString("o")
        completed_at = $CompletedAt.ToUniversalTime().ToString("o")
        wall_seconds = [Math]::Round($WallSeconds, 3)
        pipeline_window_seconds = [Math]::Round($pipelineWindowSeconds, 3)
        throughput_jobs_per_sec = $pipelineThroughput
        wall_throughput_jobs_per_sec = $wallThroughput
        consumer_timeout_ms = $ConsumerTimeoutMS
        status_counts = $statusCounts
        latency_ms = [ordered]@{
            receive_p50 = Get-Percentile $receiveLatencyMS 50
            receive_p95 = Get-Percentile $receiveLatencyMS 95
            receive_p99 = Get-Percentile $receiveLatencyMS 99
            worker_total_p50 = Get-Percentile $workerTotalMS 50
            worker_total_p95 = Get-Percentile $workerTotalMS 95
            worker_total_p99 = Get-Percentile $workerTotalMS 99
            queue_p95 = Get-Percentile $queueMS 95
            run_p95 = Get-Percentile $runMS 95
        }
    }
}

function Write-JsonReport {
    param(
        [Parameter(Mandatory = $true)][object]$Report,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $parent = Split-Path -Parent $Path
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        New-Item -ItemType Directory -Force -Path $parent | Out-Null
    }
    $Report | ConvertTo-Json -Depth 20 | Set-Content -Path $Path -Encoding UTF8
}

Push-Location $repoRoot
try {
    Import-SandboxEnvFile -Root $repoRoot | Out-Null

    if ($Jobs -lt 1) {
        throw "Jobs must be >= 1"
    }
    if ($TimeoutSeconds -lt 1) {
        throw "TimeoutSeconds must be >= 1"
    }
    if ($ConsumerTimeoutMS -lt 100) {
        throw "ConsumerTimeoutMS must be >= 100"
    }
    if ([string]::IsNullOrWhiteSpace($RequestTopic)) {
        $RequestTopic = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_REQUESTS" "code-execution.requests.v1"
    }
    if ([string]::IsNullOrWhiteSpace($ResultTopic)) {
        $ResultTopic = Get-SandboxEnvOrDefault "SANDBOX_KAFKA_TOPIC_RESULTS" "code-execution.results.v1"
    }

    $runID = [guid]::NewGuid().ToString()
    New-Item -ItemType Directory -Force -Path ".tmp" | Out-Null
    $requestFile = Join-Path ".tmp" "load-test-$runID-requests.txt"
    if ([string]::IsNullOrWhiteSpace($ReportPath)) {
        $ReportPath = Join-Path ".tmp" "load-test-$runID-report.json"
    }

    $expected = @{}
    $lines = New-Object System.Collections.Generic.List[string]
    for ($i = 0; $i -lt $Jobs; $i++) {
        $job = New-LoadTestJob -Index $i -RunID $runID -RunLabel $RunLabel -Language $Language -FakeStatus $FakeStatus
        $expected[$job.submission_id] = $job
        $lines.Add("$($job.submission_id)|$($job.payload)")
    }
    Set-Content -Path $requestFile -Value $lines -Encoding UTF8

    Write-Host "Load test:"
    Write-Host "  run_id:        $runID"
    Write-Host "  jobs:          $Jobs"
    Write-Host "  language:      $Language"
    Write-Host "  request_topic: $RequestTopic"
    Write-Host "  result_topic:  $ResultTopic"
    Write-Host "  request_file:  $requestFile"
    Write-Host "  report_file:   $ReportPath"

    $startedAt = (Get-Date).ToUniversalTime()
    if ($DryRun) {
        $report = New-Report `
            -RunID $runID `
            -RunLabel $RunLabel `
            -Language $Language `
            -Jobs $Jobs `
            -Seen @{} `
            -Expected $expected `
            -RequestTopic $RequestTopic `
            -ResultTopic $ResultTopic `
            -StartedAt $startedAt `
            -SentAt $startedAt `
            -CompletedAt (Get-Date).ToUniversalTime() `
            -WallSeconds 0 `
            -ConsumerTimeoutMS $ConsumerTimeoutMS `
            -DryRun $true
        Write-JsonReport -Report $report -Path $ReportPath
        Write-Host "Dry run complete. Kafka was not used."
        exit 0
    }

    $wall = [Diagnostics.Stopwatch]::StartNew()
    Invoke-KafkaProducer -InputFile $requestFile -Topic $RequestTopic -Root $repoRoot
    if ($LASTEXITCODE -ne 0) {
        throw "kafka-console-producer failed with exit code $LASTEXITCODE"
    }
    $sentAt = (Get-Date).ToUniversalTime()

    $seen = Read-KafkaResults -Expected $expected -Topic $ResultTopic -TimeoutSeconds $TimeoutSeconds -ConsumerTimeoutMS $ConsumerTimeoutMS -Root $repoRoot
    $completedAt = (Get-Date).ToUniversalTime()
    $wall.Stop()

    $report = New-Report `
        -RunID $runID `
        -RunLabel $RunLabel `
        -Language $Language `
        -Jobs $Jobs `
        -Seen $seen `
        -Expected $expected `
        -RequestTopic $RequestTopic `
        -ResultTopic $ResultTopic `
        -StartedAt $startedAt `
        -SentAt $sentAt `
        -CompletedAt $completedAt `
        -WallSeconds $wall.Elapsed.TotalSeconds `
        -ConsumerTimeoutMS $ConsumerTimeoutMS `
        -DryRun $false
    Write-JsonReport -Report $report -Path $ReportPath

    Write-Host ""
    Write-Host "Load test result:"
    Write-Host "  received:   $($report.jobs_received)/$($report.jobs_requested)"
    Write-Host "  throughput: $($report.throughput_jobs_per_sec) jobs/s"
    Write-Host "  p95 total:  $($report.latency_ms.worker_total_p95) ms"
    Write-Host "  p95 queue:  $($report.latency_ms.queue_p95) ms"
    Write-Host "  report:     $ReportPath"

    if ($seen.Count -lt $expected.Count) {
        exit 3
    }

    $badStatuses = @()
    foreach ($entry in $seen.Values) {
        if ([string]$entry.result.status -ne $ExpectedStatus) {
            $badStatuses += "$($entry.result.submission_id):$($entry.result.status)"
        }
    }
    if ($badStatuses.Count -gt 0) {
        Write-Error "Unexpected statuses: $($badStatuses -join ', ')"
        exit 2
    }

    exit 0
} finally {
    Pop-Location
}
