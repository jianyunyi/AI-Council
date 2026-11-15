[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Archive,
    [string]$EnvFile = (Join-Path $PSScriptRoot '.env'),
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yml')
)
$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $Archive -PathType Leaf)) { throw "Backup archive not found: $Archive" }
if (-not (Test-Path -LiteralPath $EnvFile -PathType Leaf)) { throw "Environment file not found: $EnvFile" }
$backupKey = $null
Get-Content -LiteralPath $EnvFile | ForEach-Object {
    if ($_ -match '^\s*([^#\s][^=]*)=(.*)$' -and $Matches[1].Trim() -eq 'AICOUNCIL_BACKUP_KEY') { $backupKey = $Matches[2].Trim() }
}
if ($backupKey -notmatch '^[0-9a-fA-F]{64}$') { throw 'AICOUNCIL_BACKUP_KEY must be a 64-character hex key in deploy.env' }
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { throw 'Docker CLI is required to verify a backup against the Compose service image' }
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$archiveName = [System.IO.Path]::GetFileName($Archive)
$containerArchive = "/app/.data/backups/restore-input-$stamp-$archiveName"
$destination = "/app/.data/restore-drills/$stamp"
$composeArgs = @('--env-file', $EnvFile, '-f', $ComposeFile)
& docker compose @composeArgs cp (Resolve-Path -LiteralPath $Archive).Path "council:$containerArchive"
if ($LASTEXITCODE -ne 0) { throw "Copying archive into council volume failed with exit code $LASTEXITCODE" }
& docker compose @composeArgs exec -T council /aicouncil-admin restore --in $containerArchive --dest $destination
if ($LASTEXITCODE -ne 0) { throw "Restore verification failed with exit code $LASTEXITCODE" }
Write-Output "Restore verified in isolated Compose volume directory: $destination"
