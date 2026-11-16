[CmdletBinding()]
param([string]$EnvFile = (Join-Path $PSScriptRoot '.env'))
$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $EnvFile)) { throw 'Preflight failed: EnvFile is missing.' }
$values = @{}
foreach ($line in Get-Content -LiteralPath $EnvFile) { if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') { $values[$Matches[1]] = $Matches[2].Trim().Trim('"').Trim("'") } }
$composeOverrides = @(
    'COUNCIL_BOOTSTRAP_PASSWORD', 'RUNNER_TOKEN', 'AICOUNCIL_BACKUP_KEY',
    'PUBLIC_BASE_URL', 'SMTP_HOST', 'SMTP_PORT', 'SMTP_FROM', 'SMTP_USERNAME',
    'SMTP_PASSWORD', 'GITHUB_OAUTH_CLIENT_ID', 'GITHUB_OAUTH_CLIENT_SECRET',
    'GOOGLE_OAUTH_CLIENT_ID', 'GOOGLE_OAUTH_CLIENT_SECRET', 'TLS_CERT', 'TLS_KEY',
    'TLS_CERT_DIRECTORY'
)
foreach ($name in $composeOverrides) {
    if (Test-Path "Env:$name") {
        $values[$name] = (Get-Item "Env:$name").Value
    }
}
$errors = [Collections.Generic.List[string]]::new()
if (-not $values.COUNCIL_BOOTSTRAP_PASSWORD -or $values.COUNCIL_BOOTSTRAP_PASSWORD.Length -lt 16 -or $values.COUNCIL_BOOTSTRAP_PASSWORD -match '^(password|changeme|admin|validation-only-password)$') { $errors.Add('COUNCIL_BOOTSTRAP_PASSWORD') }
if (-not $values.RUNNER_TOKEN -or $values.RUNNER_TOKEN.Length -lt 32) { $errors.Add('RUNNER_TOKEN') }
if ($values.AICOUNCIL_BACKUP_KEY -notmatch '^[a-fA-F0-9]{64}$') { $errors.Add('AICOUNCIL_BACKUP_KEY') }
try {
    $uri = [uri]$values.PUBLIC_BASE_URL
    if ($uri.Scheme -ne 'https' -or -not $uri.Host -or $uri.IsLoopback -or $uri.Host -notmatch '\.' -or $uri.AbsolutePath -ne '/' -or $uri.Query -or $uri.Fragment -or $uri.UserInfo) { throw 'bad' }
} catch { $errors.Add('PUBLIC_BASE_URL') }
foreach ($k in @('SMTP_HOST','SMTP_PORT','SMTP_FROM')) { if (-not $values[$k]) { $errors.Add($k) } }
if ($values.SMTP_PORT) {
    $smtpPort = 0
    $validSMTPPort = [int]::TryParse([string]$values.SMTP_PORT, [ref]$smtpPort)
    if (-not $validSMTPPort -or $smtpPort -lt 1 -or $smtpPort -gt 65535) { $errors.Add('SMTP_PORT') }
}
if (($values.SMTP_USERNAME -and -not $values.SMTP_PASSWORD) -or ($values.SMTP_PASSWORD -and -not $values.SMTP_USERNAME)) { $errors.Add('SMTP_USERNAME/SMTP_PASSWORD') }
foreach ($provider in @('GITHUB','GOOGLE')) { $id=$values["${provider}_OAUTH_CLIENT_ID"]; $secret=$values["${provider}_OAUTH_CLIENT_SECRET"]; if (($id -and -not $secret) -or ($secret -and -not $id)) { $errors.Add("${provider}_OAUTH_CLIENT_ID/SECRET") } }
foreach ($key in @('TLS_CERT','TLS_KEY')) { if (-not $values[$key] -or $values[$key] -notmatch '^/certs/[^/]+$') { $errors.Add($key); continue }; $certDir=$values.TLS_CERT_DIRECTORY; if(-not $certDir){$certDir='./certs'}; if(-not [IO.Path]::IsPathRooted($certDir)){$certDir=Join-Path $PSScriptRoot $certDir}; $hostCert=Join-Path $certDir (Split-Path $values[$key] -Leaf); if (-not (Test-Path -LiteralPath $hostCert)) { $errors.Add("${key} file") } }
if ($values.TLS_CERT -and $values.TLS_KEY -and $values.TLS_CERT -eq $values.TLS_KEY) { $errors.Add('TLS_CERT/TLS_KEY must be different files') }
$certDirCheck=$values.TLS_CERT_DIRECTORY; if(-not $certDirCheck){$certDirCheck='./certs'}; if(-not [IO.Path]::IsPathRooted($certDirCheck)){$certDirCheck=Join-Path $PSScriptRoot $certDirCheck}; if(-not (Test-Path -LiteralPath $certDirCheck -PathType Container)){$errors.Add('TLS_CERT_DIRECTORY')}
if ($errors.Count) { throw ('Preflight failed: ' + (($errors | Select-Object -Unique) -join ', ')) }
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { throw 'Docker Compose v2 is required.' }
$compose = (Resolve-Path (Join-Path $PSScriptRoot 'docker-compose.yml')).Path
$json = & docker compose --env-file $EnvFile -f $compose config --format json 2>$null
if ($LASTEXITCODE -ne 0) { throw 'docker compose config failed; inspect configuration without sharing secret values.' }
$cfg = ($json -join "`n") | ConvertFrom-Json
foreach ($service in @('runner', 'council', 'web', 'prometheus')) {
    if ($cfg.services.$service.ports) { $errors.Add("$service ports") }
}
$gatewayPorts = @($cfg.services.caddy.ports | ForEach-Object { [string]$_.published } | Sort-Object -Unique)
if (($gatewayPorts -join ',') -ne '443,80') { $errors.Add('caddy public ports 80 and 443') }
$mount = $cfg.services.council.volumes | Where-Object { $_.target -eq '/app/.data' }
$named = $cfg.volumes.PSObject.Properties | Where-Object { $_.Name -match '(^|_)council-data$' }
if (-not $mount -or $mount.type -ne 'volume' -or $mount.source -notmatch '(^|[-_])council-data$' -or -not $named) { $errors.Add('council-data named volume at /app/.data') }
if ($errors.Count) { throw ('Preflight failed: ' + ($errors -join ', ')) }
