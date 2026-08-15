function Import-SandboxEnvFile {
    param([Parameter(Mandatory = $true)][string]$Root)

    $candidates = @(
        (Join-Path -Path $Root -ChildPath ".env.local"),
        (Join-Path -Path $Root -ChildPath ".env")
    )

    foreach ($candidate in $candidates) {
        if (-not (Test-Path $candidate)) {
            continue
        }

        foreach ($line in Get-Content -Path $candidate -Encoding UTF8) {
            $trimmed = $line.Trim()
            if ($trimmed -eq "" -or $trimmed.StartsWith("#")) {
                continue
            }

            $separator = $trimmed.IndexOf("=")
            if ($separator -le 0) {
                continue
            }

            $name = $trimmed.Substring(0, $separator).Trim()
            $value = $trimmed.Substring($separator + 1).Trim()
            if ($value.Length -ge 2) {
                $first = $value.Substring(0, 1)
                $last = $value.Substring($value.Length - 1, 1)
                if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                    $value = $value.Substring(1, $value.Length - 2)
                }
            }

            if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) {
                [Environment]::SetEnvironmentVariable($name, $value, "Process")
            }
        }

        return $candidate
    }

    return ""
}

function Get-SandboxComposeArgs {
    param([Parameter(Mandatory = $true)][string]$Root)

    $compose = @("compose")
    if (Test-Path (Join-Path -Path $Root -ChildPath ".env.local")) {
        $compose += @("--env-file", ".env.local")
    } elseif (Test-Path (Join-Path -Path $Root -ChildPath ".env")) {
        $compose += @("--env-file", ".env")
    }
    return $compose
}

function Get-SandboxEnvOrDefault {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Default
    )

    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value
}
