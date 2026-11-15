param([string]$GoBinary = 'go')
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if (Test-Path -LiteralPath '.env') {
    foreach ($taskLine in Get-Content -LiteralPath '.env') {
        if ($taskLine -match '^\s*([A-Z][A-Z0-9_]*)=(.*)$') {
            $taskKey = $Matches[1]
            $taskValue = $Matches[2].Trim()
            if (($taskValue.StartsWith('"') -and $taskValue.EndsWith('"')) -or ($taskValue.StartsWith("'") -and $taskValue.EndsWith("'"))) {
                $taskValue = $taskValue.Substring(1, $taskValue.Length - 2)
            }
            [Environment]::SetEnvironmentVariable($taskKey, $taskValue, 'Process')
        }
    }
}
& $GoBinary run ./cmd/server
exit $LASTEXITCODE
