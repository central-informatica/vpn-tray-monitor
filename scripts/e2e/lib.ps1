<#
.SYNOPSIS
  Funções do roteiro e2e do MSI (spec §10.3). Carregado por run.ps1.
#>

Set-StrictMode -Version Latest

$script:InstallDir = Join-Path $env:ProgramFiles 'VPN Monitor'
$script:Svc = Join-Path $script:InstallDir 'vpnmon-svc.exe'
# = config.DataDirName dentro da ProgramData (o MSI grava o caminho em
# HKLM\SOFTWARE\VPNMonitor\DataDir, lido como VPNMON_DATADIR no PURGE=1).
$script:DataDir = Join-Path $env:ProgramData 'VPNMonitor'
$script:ServiceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\VPNMonitor'
$script:EventSourceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\EventLog\Application\VPNMonitor'

function Write-Step {
    param([Parameter(Mandatory)][string]$Text)
    Write-Host ''
    Write-Host "=== $Text ==="
}

function Assert-That {
    param(
        [Parameter(Mandatory)][bool]$Condition,
        [Parameter(Mandatory)][string]$Message
    )
    if (-not $Condition) { throw "FALHOU: $Message" }
    Write-Host "ok: $Message"
}

# Wait-Until repete o bloco até ele devolver verdadeiro ou o prazo vencer.
function Wait-Until {
    param(
        [Parameter(Mandatory)][scriptblock]$Condition,
        [Parameter(Mandatory)][int]$TimeoutSeconds,
        [Parameter(Mandatory)][string]$Message,
        [int]$IntervalMs = 500
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) {
            Write-Host "ok: $Message"
            return
        }
        Start-Sleep -Milliseconds $IntervalMs
    }
    throw "FALHOU (após $TimeoutSeconds s): $Message"
}

# Invoke-Msiexec roda o msiexec em silêncio com log detalhado e devolve o
# código de saída (0 e 3010 = sucesso).
function Invoke-Msiexec {
    param(
        [Parameter(Mandatory)][ValidateSet('/i', '/x')][string]$Mode,
        [Parameter(Mandatory)][string]$Msi,
        [Parameter(Mandatory)][string]$LogFile,
        [string[]]$Properties = @()
    )
    $msiArgs = @($Mode, "`"$Msi`"", '/qn', '/norestart', '/l*v', "`"$LogFile`"") + $Properties
    Write-Host "msiexec $($msiArgs -join ' ')"
    $p = Start-Process -FilePath 'msiexec.exe' -ArgumentList $msiArgs -Wait -PassThru
    return $p.ExitCode
}

# Invoke-Svc roda o vpnmon-svc instalado; os argumentos vão num array
# (um "--json" solto seria lido como parâmetro da função).
function Invoke-Svc {
    param([Parameter(Mandatory)][string[]]$Arguments)
    $out = & $script:Svc @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "vpnmon-svc $($Arguments -join ' ') saiu com $LASTEXITCODE`: $out" }
    return $out
}

# Get-VpnStatus lê o snapshot do `status --json` (contrato em
# cmd/vpnmon-svc/pipecmds_test.go: vpns[].name/state/attempt sempre presentes;
# nextAttemptUnix e lastError só quando há).
function Get-VpnStatus {
    return (Invoke-Svc @('status', '--json') | ConvertFrom-Json)
}

function Get-InstalledVersion {
    $keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
    return @(Get-ItemProperty $keys -ErrorAction SilentlyContinue |
            Where-Object { $_.PSObject.Properties['DisplayName'] -and $_.DisplayName -eq 'VPN Monitor' } |
            ForEach-Object { $_.DisplayVersion })
}

# Get-DataHash resume config.json e o cofre (para conferir que o upgrade não
# mexeu neles).
function Get-DataHash {
    $files = @(Join-Path $script:DataDir 'config.json') +
    @(Get-ChildItem (Join-Path $script:DataDir 'credentials') -Filter *.bin -File | ForEach-Object FullName)
    return ($files | Sort-Object | ForEach-Object { "$(Split-Path -Leaf $_)=$((Get-FileHash $_ -Algorithm SHA256).Hash)" }) -join ';'
}

# Test-RasAvailable (passo 0): RasMan sobe e Add-VpnConnection funciona.
function Test-RasAvailable {
    try {
        Set-Service RasMan -StartupType Manual -ErrorAction Stop
        Start-Service RasMan -ErrorAction Stop
        Add-VpnConnection -Name 'vpnmon-sonda' -ServerAddress 192.0.2.1 -TunnelType Sstp `
            -AllUserConnection -Force -ErrorAction Stop
        Remove-VpnConnection -Name 'vpnmon-sonda' -AllUserConnection -Force -ErrorAction Stop
    }
    catch {
        throw "runner sem RAS (RasMan ou Add-VpnConnection indisponível): $($_.Exception.Message)"
    }
    Write-Host 'ok: RasMan em execução e Add-VpnConnection disponível'
}

# Invoke-Sc roda o sc.exe e devolve a saída (o texto também vai ao console,
# para o diagnóstico). Saída em inglês: a imagem do runner é en-US.
function Invoke-Sc {
    param([Parameter(Mandatory)][string[]]$Arguments)
    $out = & sc.exe @Arguments 2>&1 | Out-String
    Write-Host "sc.exe $($Arguments -join ' '):$([Environment]::NewLine)$($out.Trim())"
    if ($LASTEXITCODE -ne 0) { throw "sc.exe $($Arguments -join ' ') saiu com $LASTEXITCODE" }
    return $out
}

# Assert-ServicePolicy confere a política que o serviço aplica na partida
# (svc.EnsurePolicy): recuperação 5/30/60 s zerando em 1 dia, também em
# falha sem crash, e preshutdown de 15 s. Duas fontes: o registro do serviço
# e o próprio SCM pelo sc.exe (qfailure, qfailureflag; o preshutdown só pelo
# registro).
function Assert-ServicePolicy {
    Wait-Until -TimeoutSeconds 30 -Message 'política do SCM gravada pelo serviço' -Condition {
        $k = Get-ItemProperty $script:ServiceKey -ErrorAction SilentlyContinue
        $k -and $k.PSObject.Properties['FailureActions'] -and $k.PSObject.Properties['PreshutdownTimeout']
    }
    $k = Get-ItemProperty $script:ServiceKey
    $fa = [byte[]]$k.FailureActions
    # SERVICE_FAILURE_ACTIONS no registro: dwResetPeriod, 3 campos, cActions,
    # depois cActions pares (tipo, espera em ms) a partir do byte 20.
    $reset = [BitConverter]::ToUInt32($fa, 0)
    $count = [BitConverter]::ToUInt32($fa, 12)
    $actions = for ($i = 0; $i -lt $count; $i++) {
        '{0}:{1}' -f [BitConverter]::ToUInt32($fa, 20 + 8 * $i), [BitConverter]::ToUInt32($fa, 24 + 8 * $i)
    }
    Assert-That ($reset -eq 86400) "registro: recuperação zera em 1 dia (veio $reset s)"
    Assert-That (($actions -join ',') -eq '1:5000,1:30000,1:60000') "registro: reinicia após 5/30/60 s (veio $($actions -join ','))"
    Assert-That ($k.FailureActionsOnNonCrashFailures -eq 1) 'registro: recuperação também em falha sem crash'
    Assert-That ($k.PreshutdownTimeout -eq 15000) "registro: preshutdown de 15 s (veio $($k.PreshutdownTimeout) ms)"

    $q = Invoke-Sc @('qfailure', 'VPNMonitor')
    $scDelays = @([regex]::Matches($q, 'RESTART -- Delay = (\d+)') | ForEach-Object { $_.Groups[1].Value })
    Assert-That ($q -match 'RESET_PERIOD[^:\r\n]*:\s*86400\b') 'sc qfailure: zera em 86400 s'
    Assert-That (($scDelays -join ',') -eq '5000,30000,60000') "sc qfailure: RESTART 5000/30000/60000 ms (veio $($scDelays -join ','))"
    $flag = Invoke-Sc @('qfailureflag', 'VPNMonitor')
    Assert-That ($flag -match 'NONCRASH_FAILURES\s*:\s*TRUE') 'sc qfailureflag: TRUE'
    # O sc.exe não tem consulta de preshutdown (qpreshutdown sai com 1639,
    # comando inexistente): o prazo fica conferido só pelo registro, acima.
}

# Assert-DataAcl confere a pasta de dados como o MSI (PermissionEx) e o
# serviço (acl.DirSDDL) a deixam: dono Administradores (BA), DACL protegida
# (P) com só SYSTEM e Administradores em controle total, e nada posto em
# quarentena (acl: <pasta>.naoconfiavel-<data>).
function Assert-DataAcl {
    $sddl = (Get-Acl $script:DataDir).GetSecurityDescriptorSddlForm('Owner, Access')
    $ok = $sddl -match '^O:BAD:PA?I?(\(A;OICI;FA;;;SY\)\(A;OICI;FA;;;BA\)|\(A;OICI;FA;;;BA\)\(A;OICI;FA;;;SY\))$'
    Assert-That $ok "ACL da pasta de dados ($sddl)"
    $quarantined = @(Get-ChildItem $env:ProgramData -Directory -Filter 'VPNMonitor.naoconfiavel-*' -ErrorAction SilentlyContinue)
    Assert-That ($quarantined.Count -eq 0) 'o serviço não pôs a pasta de dados em quarentena'
}

# Assert-EventSource confere a origem do Event Log registrada pelo MSI
# (util:EventSource): EventMessageFile como REG_EXPAND_SZ com
# %SystemRoot%\System32\EventCreate.exe (a mesma DLL do `vpnmon-svc install`).
function Assert-EventSource {
    $key = Get-Item $script:EventSourceKey
    $kind = $key.GetValueKind('EventMessageFile')
    $raw = $key.GetValue('EventMessageFile', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    Assert-That ($kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString) "EventMessageFile é REG_EXPAND_SZ (veio $kind)"
    Assert-That ($raw -eq '%SystemRoot%\System32\EventCreate.exe') "EventMessageFile = $raw"
}

function Save-DiagnosticLog {
    param([Parameter(Mandatory)][string]$LogDir)
    $logs = Join-Path $script:DataDir 'logs'
    if (Test-Path $logs) { Copy-Item "$logs\*" $LogDir -Force -ErrorAction SilentlyContinue }
    Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'VPNMonitor' } -MaxEvents 50 -ErrorAction SilentlyContinue |
        Format-List TimeCreated, LevelDisplayName, Message | Out-File (Join-Path $LogDir 'eventlog.txt')
    Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Service Control Manager' } -MaxEvents 50 -ErrorAction SilentlyContinue |
        Format-List TimeCreated, Message | Out-File (Join-Path $LogDir 'scm.txt')
}
