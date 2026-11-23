$ErrorActionPreference='Stop'
$deploy=Split-Path $PSScriptRoot -Parent
$tmp=Join-Path ([IO.Path]::GetTempPath()) ('aicouncil-contract-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force $tmp | Out-Null
function docker { param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Args)
  if($Args -contains 'config') { $global:LASTEXITCODE=0; return $global:ComposeJson }
  $global:LASTEXITCODE=0
}
try {
 $certs=Join-Path $tmp 'certs'; New-Item -ItemType Directory $certs | Out-Null; Set-Content (Join-Path $certs 'tls.crt') 'dummy'; Set-Content (Join-Path $certs 'tls.key') 'dummy'
 $gateway=@{ports=@(@{published='80'},@{published='443'})}
 $global:ComposeJson=@{services=@{caddy=$gateway;runner=@{};council=@{volumes=@(@{target='/app/.data';type='volume';source='proj_council-data'})};web=@{};prometheus=@{}};volumes=@{'council-data'=@{name='proj_council-data'}}} | ConvertTo-Json -Depth 8
 $envFile=Join-Path $tmp 'valid.env'
 $valid=@{COUNCIL_BOOTSTRAP_PASSWORD=('GoodPass-'+('x'*20));RUNNER_TOKEN=('r'*40);AICOUNCIL_BACKUP_KEY=('a'*64);AICOUNCIL_APPROVAL_SIGNING_KEY=('b'*64);PUBLIC_BASE_URL='https://council.example.org';SMTP_HOST='smtp.example.org';SMTP_PORT='587';SMTP_FROM='robot@example.org';TLS_CERT='/certs/tls.crt';TLS_KEY='/certs/tls.key';TLS_CERT_DIRECTORY=$certs}
 function writeEnv($v){$v.GetEnumerator()|ForEach-Object{"$($_.Key)=$($_.Value)"}|Set-Content $envFile}
 writeEnv $valid
 & (Join-Path $deploy 'preflight.ps1') -EnvFile $envFile
 foreach($case in @(@{COUNCIL_BOOTSTRAP_PASSWORD='bad'},@{RUNNER_TOKEN='short'},@{AICOUNCIL_BACKUP_KEY='secret-not-hex'},@{PUBLIC_BASE_URL='https://localhost'},@{PUBLIC_BASE_URL='https://council.example.org/nested'},@{SMTP_PORT='70000'},@{TLS_CERT='not-a-container-path'})) {
   $bad=@{}+$valid; foreach($k in $case.Keys){$bad[$k]=$case[$k]}; writeEnv $bad
   try { & (Join-Path $deploy 'preflight.ps1') -EnvFile $envFile; throw 'Expected invalid environment rejection.' } catch { if($_.Exception.Message -notmatch 'Preflight failed'){throw}; if($_.Exception.Message -match 'secret-not-hex|GoodPass|rrrr'){throw 'Secret leaked in preflight error.'} }
 }
 $priorBaseUrl=if(Test-Path Env:PUBLIC_BASE_URL){(Get-Item Env:PUBLIC_BASE_URL).Value}else{$null}
 try {
   $env:PUBLIC_BASE_URL='http://override.example.org'
   $failed=$false
   try { & (Join-Path $deploy 'preflight.ps1') -EnvFile $envFile } catch { $failed=$_.Exception.Message -match 'PUBLIC_BASE_URL' }
   if(-not $failed){throw 'Preflight ignored an unsafe process environment override.'}
 } finally {
   if($null -eq $priorBaseUrl){Remove-Item Env:PUBLIC_BASE_URL -ErrorAction SilentlyContinue}else{$env:PUBLIC_BASE_URL=$priorBaseUrl}
 }
 writeEnv $valid
 $global:ComposeJson=@{services=@{caddy=$gateway;runner=@{ports=@(@{published='9091'})};council=@{volumes=@(@{target='/app/.data';type='bind';source='/tmp'})};web=@{};prometheus=@{}};volumes=@{'council-data'=@{}}} | ConvertTo-Json -Depth 8
 try { & (Join-Path $deploy 'preflight.ps1') -EnvFile $envFile; throw 'Expected exposed-port/wrong-volume rejection.' } catch { if($_.Exception.Message -notmatch 'runner ports|council-data named volume'){throw} }
 $global:ComposeJson=@{services=@{caddy=$gateway;runner=@{};council=@{volumes=@(@{target='/app/.data';type='volume';source='proj_other-data'})};web=@{};prometheus=@{}};volumes=@{'council-data'=@{name='proj_council-data'};other_data=@{}}} | ConvertTo-Json -Depth 8
 try { & (Join-Path $deploy 'preflight.ps1') -EnvFile $envFile; throw 'Expected wrong named-volume rejection.' } catch { if($_.Exception.Message -notmatch 'council-data named volume'){throw} }
 Write-Output 'Preflight contract cases passed.'
} finally { Remove-Item -LiteralPath $tmp -Recurse -Force }



