[CmdletBinding()]
param()

function Resolve-ComposeCommand {
    $docker = Get-Command docker -ErrorAction SilentlyContinue
    if ($docker) {
        & $docker.Source compose version 2>$null
        if ($LASTEXITCODE -eq 0) {
            return [pscustomobject]@{ FilePath = $docker.Source; Prefix = @('compose') }
        }
    }

    $standalone = Get-Command docker-compose -ErrorAction SilentlyContinue
    if ($standalone) {
        return [pscustomobject]@{ FilePath = $standalone.Source; Prefix = @() }
    }

    $dockerDesktopCompose = Join-Path $env:ProgramFiles 'Docker\Docker\resources\bin\docker-compose.exe'
    if (Test-Path -LiteralPath $dockerDesktopCompose) {
        return [pscustomobject]@{ FilePath = $dockerDesktopCompose; Prefix = @() }
    }

    throw 'Docker Compose v2 was not found. Install Docker Desktop or add its resources\\bin directory to PATH.'
}

$validationValues = [ordered]@{
    COUNCIL_BOOTSTRAP_SUBJECT  = 'compose-validation'
    COUNCIL_BOOTSTRAP_PASSWORD = 'validation-only-password'
    RUNNER_TOKEN               = 'validation-only-runner-token'
    TLS_CERT                   = '/certs/validation.crt'
    TLS_KEY                    = '/certs/validation.key'
}

$previousValues = @{}
foreach ($name in $validationValues.Keys) {
    $exists = Test-Path "Env:$name"
    $previousValues[$name] = @{ Exists = $exists; Value = if ($exists) { (Get-Item "Env:$name").Value } else { $null } }
    Set-Item "Env:$name" $validationValues[$name]
}

try {
    $compose = Resolve-ComposeCommand
    & $compose.FilePath @($compose.Prefix) --env-file (Join-Path $PSScriptRoot '.env.example') -f (Join-Path $PSScriptRoot 'docker-compose.yml') config --quiet
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose config failed with exit code $LASTEXITCODE"
    }
}
finally {
    foreach ($name in $validationValues.Keys) {
        $previous = $previousValues[$name]
        if ($previous.Exists) {
            Set-Item "Env:$name" $previous.Value
        } else {
            Remove-Item "Env:$name" -ErrorAction SilentlyContinue
        }
    }
}
