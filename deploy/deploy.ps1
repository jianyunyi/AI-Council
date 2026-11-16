[CmdletBinding()]
param([string]$EnvFile=(Join-Path $PSScriptRoot '.env'),[string]$ReleaseTag)
$ErrorActionPreference='Stop'
if (-not $ReleaseTag) { $ReleaseTag=(git -C (Split-Path $PSScriptRoot -Parent) rev-parse --short=12 HEAD).Trim() }
if ($ReleaseTag -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$') { throw 'Invalid release tag.' }
& (Join-Path $PSScriptRoot 'preflight.ps1') -EnvFile $EnvFile
$root=Split-Path $PSScriptRoot -Parent; $state=Join-Path $root '.data/deployments'; $current=Join-Path $state 'current.json'; New-Item -ItemType Directory -Force -Path $state | Out-Null
$previous=$null; if (Test-Path $current) { $previous=(Get-Content $current -Raw | ConvertFrom-Json).releaseTag }
if($previous -and $ReleaseTag -eq $previous){throw 'Release tag must differ from the active tag to preserve rollback images.'}
$compose=Join-Path $PSScriptRoot 'docker-compose.yml'; $env:AICOUNCIL_RELEASE_TAG=$ReleaseTag
try {
 $backupOutcome='not-needed'; $backupPath=$null
 $running=& docker compose --env-file $EnvFile -f $compose ps --status running -q council
 if($running){ & (Join-Path $PSScriptRoot 'backup.ps1'); if($LASTEXITCODE -ne 0){throw 'Pre-upgrade backup failed.'}; $backupOutcome='created'; $backupPath=(Get-ChildItem (Join-Path $root '.data/backups') -Filter '*.aicb' | Sort-Object LastWriteTime -Descending | Select-Object -First 1 -ExpandProperty FullName) }
 & docker compose --env-file $EnvFile -f $compose pull caddy prometheus
 if($LASTEXITCODE -ne 0){throw 'External image pull failed.'}
 & docker compose --env-file $EnvFile -f $compose build
 if ($LASTEXITCODE -ne 0) { throw 'Image build failed.' }
 & docker compose --env-file $EnvFile -f $compose up -d --remove-orphans
 if ($LASTEXITCODE -ne 0) { throw 'Compose activation failed.' }
 $null = & (Join-Path $PSScriptRoot 'wait-health.ps1') -EnvFile $EnvFile
 $manifest=[ordered]@{releaseTag=$ReleaseTag; deployedAt=[DateTime]::UtcNow.ToString('o'); previousReleaseTag=$previous; gitCommit=(git -C $root rev-parse HEAD).Trim(); backupOutcome=$backupOutcome; backupPath=$backupPath}
 $manifest | ConvertTo-Json | Set-Content -Encoding utf8 $current
 $manifest | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $state ("{0}.json" -f [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')))
} catch {
 if ($previous) { & (Join-Path $PSScriptRoot 'rollback.ps1') -ReleaseTag $previous -EnvFile $EnvFile -DuringDeploy; if($LASTEXITCODE -ne 0){throw 'Rollback failed; deployment state remains unchanged.'} }
 else { & docker compose --env-file $EnvFile -f $compose stop; if ($LASTEXITCODE -ne 0) { throw 'Failed deployment and service stop failed.' } }
 throw
}
