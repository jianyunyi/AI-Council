[CmdletBinding()]
param()

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
    Get-Command docker -ErrorAction Stop | Out-Null
    & docker compose --env-file (Join-Path $PSScriptRoot '.env.example') -f (Join-Path $PSScriptRoot 'docker-compose.yml') config
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
