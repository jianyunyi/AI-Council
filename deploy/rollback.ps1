[CmdletBinding()]
param([Parameter(Mandatory=$true)][ValidatePattern('^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$')][string]$ReleaseTag,[string]$EnvFile=(Join-Path $PSScriptRoot '.env'),[switch]$DuringDeploy)
$ErrorActionPreference='Stop'
$compose=Join-Path $PSScriptRoot 'docker-compose.yml'
$state=Join-Path (Split-Path $PSScriptRoot -Parent) '.data/deployments/current.json'
if (-not (Test-Path $state)) { throw 'No deployment state exists for rollback.' }
$known=(Get-Content $state -Raw | ConvertFrom-Json).releaseTag
if (-not $DuringDeploy -and -not $known) { throw 'No deployment state exists for rollback.' }
$env:AICOUNCIL_RELEASE_TAG=$ReleaseTag
foreach($service in @('council','web','runner')) { & docker image inspect "aicouncil-${service}:$ReleaseTag" *> $null; if ($LASTEXITCODE -ne 0) { throw "Rollback $service image is not present locally." } }
& docker compose --env-file $EnvFile -f $compose up -d --no-build --remove-orphans
if ($LASTEXITCODE -ne 0) { throw 'Rollback Compose activation failed.' }
$null = & (Join-Path $PSScriptRoot 'wait-health.ps1') -EnvFile $EnvFile
if (-not $DuringDeploy) { $doc=[ordered]@{releaseTag=$ReleaseTag;deployedAt=[DateTime]::UtcNow.ToString('o');previousReleaseTag=$known;rollback=$true}; $doc | ConvertTo-Json | Set-Content -Encoding utf8 $state }
