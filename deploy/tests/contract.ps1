$ErrorActionPreference='Stop'
$root=Split-Path $PSScriptRoot -Parent
foreach ($file in @('preflight.ps1','deploy.ps1','rollback.ps1','wait-health.ps1','smoke-test.ps1')) { if (-not (Test-Path (Join-Path $root $file))) { throw "Missing script: $file" }; $null=[scriptblock]::Create((Get-Content (Join-Path $root $file) -Raw)) }
$compose=Get-Content (Join-Path $root 'docker-compose.yml') -Raw
foreach ($svc in @('web','council','runner')) { if ($compose -notmatch "(?s)${svc}:.*?image:\s+aicouncil-${svc}:\$\{AICOUNCIL_RELEASE_TAG:-local\}") { throw "Missing immutable tag image for $svc" } }
Write-Output 'Deployment script contracts passed.'
& (Join-Path $PSScriptRoot 'preflight-contract.ps1')
& (Join-Path $PSScriptRoot 'health-contract.ps1')
