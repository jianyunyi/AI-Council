[CmdletBinding()]
param()

function Assert-Match {
    param(
        [Parameter(Mandatory)]
        [string]$Content,
        [Parameter(Mandatory)]
        [string]$Pattern,
        [Parameter(Mandatory)]
        [string]$Description
    )

    if ($Content -notmatch $Pattern) {
        throw "$Description did not match required pattern: $Pattern"
    }
}

$goProxyArgument = '(?m)^ARG GOPROXY=https://goproxy\.cn,direct\r?$'
$goProxyEnvironment = '(?m)^ENV GOPROXY=\$GOPROXY\r?$'

foreach ($dockerfile in 'Dockerfile.council', 'Dockerfile.runner') {
    $dockerfilePath = Join-Path $PSScriptRoot $dockerfile
    $content = Get-Content -Raw -LiteralPath $dockerfilePath

    Assert-Match -Content $content -Pattern $goProxyArgument -Description "$dockerfile must define the default GOPROXY build argument"
    Assert-Match -Content $content -Pattern $goProxyEnvironment -Description "$dockerfile must export GOPROXY from the build argument"
}

$compose = Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'docker-compose.yml')
foreach ($service in 'council', 'runner') {
    $buildArgument = '(?ms)^  {0}:\r?\n    build:.*?^      args:\r?\n^        GOPROXY: \$\{{GOPROXY:-https://goproxy\.cn,direct\}}\r?$' -f $service
    Assert-Match -Content $compose -Pattern $buildArgument -Description "$service must pass GOPROXY to its image build"
}

$validatorPath = Join-Path $PSScriptRoot 'validate-compose.ps1'
$validator = Get-Content -Raw -LiteralPath $validatorPath
Assert-Match -Content $validator -Pattern '(?m)config --quiet\r?$' -Description 'validate-compose.ps1 must run docker compose config --quiet'

& $validatorPath
if ($LASTEXITCODE -ne 0) {
    throw "validate-compose.ps1 failed with exit code $LASTEXITCODE"
}
