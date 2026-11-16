$ErrorActionPreference = 'Stop'
$deployRoot = Split-Path $PSScriptRoot -Parent
$envPath = Join-Path ([IO.Path]::GetTempPath()) ('health-contract-' + [guid]::NewGuid().ToString('N') + '.env')
Set-Content -LiteralPath $envPath 'PUBLIC_BASE_URL=https://council.example.test'
$global:MissingService = ''
$global:PublicStatus = 200
$global:RequestedUris = [Collections.Generic.List[string]]::new()
function docker {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$DockerArgs)
    $global:LASTEXITCODE = 0
    if ($DockerArgs -contains 'ps') {
        $service = $DockerArgs[-1]
        if ($service -eq $global:MissingService) { return }
        return "container-$service"
    }
    if ($DockerArgs -contains 'inspect') { return 'healthy' }
}
function Invoke-WebRequest {
    param([string]$Uri, [int]$TimeoutSec)
    $global:RequestedUris.Add($Uri)
    if ($global:PublicStatus -ge 500) { throw 'mock endpoint unavailable' }
    return [pscustomobject]@{ StatusCode = $global:PublicStatus }
}
try {
    $null = & (Join-Path $deployRoot 'wait-health.ps1') -EnvFile $envPath -TimeoutSeconds 0
    if ($RequestedUris.Count -ne 2 -or $RequestedUris[0] -ne 'https://council.example.test/healthz' -or $RequestedUris[1] -ne 'https://council.example.test/') { throw 'Healthy deployment did not verify both HTTPS routes.' }

    $global:MissingService = 'runner'
    $failed = $false
    try { & (Join-Path $deployRoot 'wait-health.ps1') -EnvFile $envPath -TimeoutSeconds 0 } catch { $failed = $_.Exception.Message -match 'health check timed out' }
    if (-not $failed) { throw 'A missing Runner container was accepted as healthy.' }

    $global:MissingService = ''
    $global:PublicStatus = 503
    $failed = $false
    try { & (Join-Path $deployRoot 'wait-health.ps1') -EnvFile $envPath -TimeoutSeconds 0 } catch { $failed = $_.Exception.Message -match 'health check timed out' }
    if (-not $failed) { throw 'An unavailable public HTTPS endpoint was accepted as healthy.' }

    Write-Output 'Health contract cases passed.'
} finally {
    Remove-Item -LiteralPath $envPath -Force -ErrorAction SilentlyContinue
}
