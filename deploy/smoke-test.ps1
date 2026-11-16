param(
    [Parameter(Mandatory=$true)][string]$BaseUrl,
    [Parameter(Mandatory=$true)][string]$AdminSubject,
    [Parameter(Mandatory=$true)][string]$AdminPassword,
    [Parameter(Mandatory=$true)][string]$ProjectName,
    [Parameter(Mandatory=$true)][string[]]$ComposeFiles,
    [string]$EnvFile,
    [switch]$SkipCertificateCheck
)

$ErrorActionPreference = 'Stop'
$base = $BaseUrl.TrimEnd('/')
$session = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
$webArgs = @{ WebSession=$session; TimeoutSec=15 }
if ($SkipCertificateCheck) { $webArgs.SkipCertificateCheck = $true }

function Invoke-SmokeJson([string]$Path, [object]$Body, [int[]]$ExpectedStatus = @(200,201)) {
    $args = @{} + $webArgs
    $args.Uri = "$base$Path"
    $args.Method = 'Post'
    $args.ContentType = 'application/json'
    if ($null -ne $Body) { $args.Body = $Body | ConvertTo-Json -Depth 12 -Compress }
    try { $result = Invoke-RestMethod @args -StatusCodeVariable status } catch {
        throw "Smoke request $Path failed: $($_.Exception.Message)"
    }
    if ($status -notin $ExpectedStatus) { throw "Smoke request $Path returned HTTP $status; expected $($ExpectedStatus -join ', ')" }
    return $result
}

try {
    foreach ($path in @('/healthz','/')) {
        $response = Invoke-WebRequest @webArgs -Uri "$base$path" -Method Get
        if ($response.StatusCode -lt 200 -or $response.StatusCode -ge 300) { throw "HTTPS route $path returned HTTP $($response.StatusCode)" }
    }

    $login = Invoke-SmokeJson '/api/v1/auth/login' @{ subject=$AdminSubject; password=$AdminPassword } @(200)
    if (-not $login.data.permissions -or $login.data.permissions.Count -eq 0) { throw 'Login did not return an authenticated identity with permissions.' }
    $null = Invoke-RestMethod @webArgs -Uri "$base/api/v1/admin/users" -Method Get
    $null = Invoke-RestMethod @webArgs -Uri "$base/api/v1/admin/roles" -Method Get

    $workspaceName = 'release-smoke-' + [guid]::NewGuid().ToString('N')
    $composeArgs = @('compose','-p',$ProjectName)
    if ($EnvFile) { $composeArgs += @('--env-file',$EnvFile) }
    foreach ($file in $ComposeFiles) { $composeArgs += @('-f',$file) }
    $composeArgs += @('config','--format','json')
    $composeJson = & docker @composeArgs
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect Compose workspace mount for smoke cleanup.' }
    $composeConfig = ($composeJson -join "`n") | ConvertFrom-Json
    $workspaceMount = @($composeConfig.services.runner.volumes | Where-Object { $_.target -eq '/workspace' }) | Select-Object -First 1
    if (-not $workspaceMount -or $workspaceMount.type -ne 'bind' -or -not $workspaceMount.source) { throw 'Runner /workspace must be a host bind mount for smoke-test cleanup.' }
    $workspaceDirectory = Join-Path $workspaceMount.source $workspaceName
    New-Item -ItemType Directory -Path $workspaceDirectory -ErrorAction Stop | Out-Null
    $workspace = Invoke-SmokeJson '/api/v1/workspaces' @{ root=$workspaceName } @(201)
    if (-not $workspace.data.id) { throw 'Workspace registration did not return an ID.' }
    $task = Invoke-SmokeJson '/api/v1/tasks' @{ workspace_id=$workspace.data.id; requirement='Release smoke approval guard'; acceptance=@('approval is required') } @(201)
    $executeArgs = @{} + $webArgs
    $executeArgs.Uri = "$base/api/v1/tasks/$($task.data.id)/execute"
    $executeArgs.Method = 'Post'
    try {
        Invoke-RestMethod @executeArgs | Out-Null
        throw 'Unapproved execution unexpectedly succeeded.'
    } catch {
        $status = [int]$_.Exception.Response.StatusCode
        if ($status -ne 403) { throw "Unapproved execution returned HTTP $status; expected 403." }
    }

    $metricsArgs = @('compose','-p',$ProjectName)
    if ($EnvFile) { $metricsArgs += @('--env-file',$EnvFile) }
    foreach ($file in $ComposeFiles) { $metricsArgs += @('-f',$file) }
    $metricsArgs += @('exec','-T','council','/healthcheck','http://127.0.0.1:8080/metrics')
    & docker @metricsArgs
    if ($LASTEXITCODE -ne 0) { throw 'Internal Council /metrics endpoint failed its HTTP health check.' }

    Write-Output 'Production smoke checks passed: HTTPS, login, administrator routes, internal metrics, and approval enforcement.'
} finally {
    try { Invoke-RestMethod @webArgs -Uri "$base/api/v1/auth/logout" -Method Post | Out-Null } catch { }
    if ($workspaceDirectory -and (Test-Path -LiteralPath $workspaceDirectory -PathType Container)) {
        Remove-Item -LiteralPath $workspaceDirectory -Recurse -Force
    }
}
