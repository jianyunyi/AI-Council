[CmdletBinding()]
param(
    [string]$EnvFile = (Join-Path $PSScriptRoot '.env'),
    [ValidateRange(0, 3600)]
    [int]$TimeoutSeconds = 180
)

$ErrorActionPreference = 'Stop'
$compose = Join-Path $PSScriptRoot 'docker-compose.yml'
$settings = @{}
foreach ($line in Get-Content -LiteralPath $EnvFile) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') {
        $settings[$Matches[1]] = $Matches[2].Trim().Trim('"').Trim("'")
    }
}

$baseUrl = $settings['PUBLIC_BASE_URL']
if (Test-Path Env:PUBLIC_BASE_URL) {
    $baseUrl = (Get-Item Env:PUBLIC_BASE_URL).Value
}
if ([string]::IsNullOrWhiteSpace($baseUrl)) {
    throw 'PUBLIC_BASE_URL is required for the deployment health check.'
}
$baseUri = [uri]$baseUrl.TrimEnd('/')
$publicUris = @(
    [uri]::new($baseUri, '/healthz').AbsoluteUri,
    [uri]::new($baseUri, '/').AbsoluteUri
)

$deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
do {
    $containersHealthy = $true
    foreach ($service in @('runner', 'council', 'web')) {
        $ids = @(& docker compose --env-file $EnvFile -f $compose ps -q $service)
        if ($LASTEXITCODE -ne 0 -or $ids.Count -ne 1) {
            $containersHealthy = $false
            continue
        }

        $health = & docker inspect --format '{{.State.Health.Status}}' $ids[0] 2>$null
        if ($LASTEXITCODE -ne 0 -or $health -ne 'healthy') {
            $containersHealthy = $false
        }
    }

    $publicEndpointsHealthy = $true
    foreach ($uri in $publicUris) {
        try {
            $response = Invoke-WebRequest -Uri $uri -TimeoutSec 5
            if ($response.StatusCode -lt 200 -or $response.StatusCode -ge 300) {
                $publicEndpointsHealthy = $false
            }
        } catch {
            $publicEndpointsHealthy = $false
        }
    }

    if ($containersHealthy -and $publicEndpointsHealthy) {
        return $true
    }

    if ([DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Seconds 3
    }
} while ([DateTime]::UtcNow -lt $deadline)

throw 'Runner, Council, Web, or public HTTPS health check timed out.'
