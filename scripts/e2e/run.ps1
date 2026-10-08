<#
.SYNOPSIS
  Roteiro e2e do MSI (spec §10.3) num Windows descartável (runner do CI).

.DESCRIPTION
  Instala, opera, atualiza e remove o VPN Monitor de verdade: exige um
  prompt elevado numa máquina sem o VPN Monitor e altera o sistema (cria
  entradas RAS, serviço, pastas). Os logs (msiexec, serviço, Event Log) ficam
  em -LogDir. Nenhuma senha vai a log ou console: a do passo 5 segue só pelo
  stdin do vpnmon-svc.

.EXAMPLE
  pwsh scripts/e2e/run.ps1 -OldMsi e2e\VPNMonitor-0.0.1-x64.msi -NewMsi e2e\VPNMonitor-0.0.2-x64.msi -LogDir e2e-logs
#>
[CmdletBinding()]
param(
    # MSI de ProductVersion 0.0.199 (instalação inicial).
    [Parameter(Mandatory)][string]$OldMsi,
    # MSI de ProductVersion 0.0.299 (upgrade).
    [Parameter(Mandatory)][string]$NewMsi,
    [Parameter(Mandatory)][string]$LogDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'lib.ps1')

# A senha vai ao vpnmon-svc pelo stdin em UTF-8 (o Windows PowerShell 5.1
# usaria ASCII e trocaria "ç" por "?").
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$LogDir = (Resolve-Path $LogDir).Path
$OldMsi = (Resolve-Path $OldMsi).Path
$NewMsi = (Resolve-Path $NewMsi).Path

$Entry = 'E2E VPN'
$LinkEntry = 'E2E Link'

try {
    Write-Step '0. Sonda: RasMan e Add-VpnConnection'
    Test-RasAvailable
    Assert-That (-not (Get-Service VPNMonitor -ErrorAction SilentlyContinue)) 'máquina sem VPN Monitor instalado'

    Write-Step '1. Entradas RAS para todos os usuários, servidor inalcançável (TEST-NET)'
    foreach ($name in $Entry, $LinkEntry) {
        Add-VpnConnection -Name $name -ServerAddress 192.0.2.1 -TunnelType Sstp `
            -AuthenticationMethod MSChapv2 -EncryptionLevel Required -AllUserConnection -Force
    }

    Write-Step '2. Instala o MSI 0.0.199 com seed'
    $code = Invoke-Msiexec -Mode '/i' -Msi $OldMsi -LogFile (Join-Path $LogDir 'install-0.0.199.log') -Properties @(
        "VPN_ENTRY=`"$Entry`"", 'VPN_NAME=Matriz', 'CHECK_HOST=192.0.2.10', 'INTERVAL=5')
    Assert-That ($code -in 0, 3010) "msiexec /i 0.0.199 (código $code)"

    Write-Step '3. Serviço, política do SCM, ACL, config do seed, Event Log, bandeja'
    Wait-Until -TimeoutSeconds 30 -Message 'serviço VPNMonitor em execução' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    $service = Get-Service VPNMonitor
    Assert-That ($service.StartType -eq 'Automatic') "início automático (veio $($service.StartType))"
    Assert-That (@($service.ServicesDependedOn.Name) -contains 'RasMan') 'depende do RasMan'
    $account = (Get-CimInstance Win32_Service -Filter "Name='VPNMonitor'").StartName
    Assert-That ($account -eq 'LocalSystem') "conta LocalSystem (veio $account)"
    Assert-ServicePolicy
    Assert-DataAcl
    $cfg = Get-Content (Join-Path $DataDir 'config.json') -Raw | ConvertFrom-Json
    Assert-That (@($cfg.vpns).Count -eq 1) 'config gerada pelo seed com uma VPN'
    $v = $cfg.vpns[0]
    Assert-That ($v.name -eq 'Matriz' -and $v.rasEntry -eq $Entry) "VPN Matriz → $Entry"
    Assert-That ($v.check.kind -eq 'ping' -and $v.check.host -eq '192.0.2.10') 'verificação ping 192.0.2.10'
    Assert-That ($v.intervalSeconds -eq 5) 'intervalo 5 s'
    Assert-EventSource
    $started = Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'VPNMonitor' } -MaxEvents 20 |
        Where-Object Message -Like '*iniciado*'
    Assert-That ([bool]$started) 'Event Log: "VPN Monitor … iniciado" (origem registrada pelo MSI)'
    $run = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name VPNMonitorTray
    Assert-That ($run.VPNMonitorTray -eq "`"$InstallDir\vpnmon-tray.exe`"") 'bandeja no HKLM Run'
    Assert-That (Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\VPN Monitor.lnk") 'atalho no menu Iniciar'

    Write-Step '4. Reconectando → erro transitório → backoff crescente; segunda VPN (link)'
    $delays = @{}
    $sawReconnecting = $false
    Wait-Until -TimeoutSeconds 300 -IntervalMs 1000 -Message 'três tentativas com erro transitório' -Condition {
        $m = @((Get-VpnStatus).vpns | Where-Object name -EQ 'Matriz')[0]
        if ($m.state -eq 'Reconectando') { $script:sawReconnecting = $true }
        $next = if ($m.PSObject.Properties['nextAttemptUnix']) { $m.nextAttemptUnix } else { 0 }
        if ($m.attempt -gt 0 -and $next -gt 0 -and -not $delays.ContainsKey([int]$m.attempt)) {
            $delays[[int]$m.attempt] = $next - [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
            Write-Host "tentativa $($m.attempt): próxima em $($delays[[int]$m.attempt]) s ($($m.state))"
        }
        $err = if ($m.PSObject.Properties['lastError']) { $m.lastError } else { $null }
        if ($err -and $err.class -ne 'transitorio') {
            throw "erro não transitório: classe $($err.class), código $($err.code): $($err.message)"
        }
        $delays.Count -ge 3
    }
    Assert-That $sawReconnecting 'estado Reconectando observado'
    $keys = @($delays.Keys | Sort-Object)
    # Folga de 1 s: a espera é medida com resolução de segundo e jitter de ±20 %.
    Assert-That ($delays[$keys[-1]] -ge $delays[$keys[0]] + 1) "backoff crescente ($(($keys | ForEach-Object { "$_=$($delays[$_])s" }) -join ', '))"
    Invoke-Svc @('vpn', 'add', '--name', 'Link', '--entry', $LinkEntry, '--check', 'link') | Write-Host
    Wait-Until -TimeoutSeconds 15 -Message 'VPN Link (verificação link) aparece no status' -Condition {
        @((Get-VpnStatus).vpns | Where-Object { $_.name -eq 'Link' -and $_.checkKind -eq 'link' }).Count -eq 1
    }

    Write-Step '5. Credencial pelo stdin'
    # A senha só passa pelo pipe; a saída do comando não a contém.
    $out = 'senha-e2e-çã' | & $Svc credential set Matriz --user 'e2e\usuario' --password-stdin 2>&1 | Out-String
    Assert-That ($LASTEXITCODE -eq 0) "credential set ($($out.Trim()))"
    $list = Invoke-Svc @('credential', 'list')
    Assert-That ($list -match '(?m)^Matriz\s+cofre') "credential list mostra o cofre ($($list.Trim()))"
    Assert-DataAcl

    Write-Step '6. Parada em até 10 s, com discagem em curso; nova partida'
    $sw = [Diagnostics.Stopwatch]::StartNew()
    & sc.exe stop VPNMonitor | Out-Null
    Wait-Until -TimeoutSeconds 30 -IntervalMs 100 -Message 'serviço parado' -Condition {
        (Get-Service VPNMonitor).Status -eq 'Stopped'
    }
    $sw.Stop()
    Assert-That ($sw.Elapsed.TotalSeconds -le 10) "parada em $([math]::Round($sw.Elapsed.TotalSeconds, 1)) s"
    Start-Service VPNMonitor
    Wait-Until -TimeoutSeconds 30 -Message 'serviço de volta' -Condition { (Get-Service VPNMonitor).Status -eq 'Running' }

    Write-Step '6b. Recuperação do SCM: processo morto volta em ~5 s e, na 2ª falha, em ~30 s'
    # A política só é regravada quando diverge: se o serviço a regravasse a
    # cada partida, a contagem de falhas zeraria e a 2ª espera seria 5 s.
    foreach ($expected in @(@{ Min = 3; Max = 25 }, @{ Min = 25; Max = 75 })) {
        $oldPid = (Get-CimInstance Win32_Service -Filter "Name='VPNMonitor'").ProcessId
        Assert-That ($oldPid -ne 0) "vpnmon-svc em execução (PID $oldPid)"
        $sw = [Diagnostics.Stopwatch]::StartNew()
        Stop-Process -Id $oldPid -Force
        Wait-Until -TimeoutSeconds ($expected.Max + 15) -Message 'serviço reiniciado pelo SCM' -Condition {
            $svcNow = Get-CimInstance Win32_Service -Filter "Name='VPNMonitor'"
            $svcNow.State -eq 'Running' -and $svcNow.ProcessId -ne 0 -and $svcNow.ProcessId -ne $oldPid
        }
        $sw.Stop()
        $s = [math]::Round($sw.Elapsed.TotalSeconds, 1)
        Assert-That ($s -ge $expected.Min -and $s -le $expected.Max) "voltou em $s s (esperado entre $($expected.Min) e $($expected.Max) s)"
    }

    Write-Step '7. Upgrade para 0.0.299 preservando config e credencial; downgrade bloqueado'
    $before = Get-DataHash
    $code = Invoke-Msiexec -Mode '/i' -Msi $NewMsi -LogFile (Join-Path $LogDir 'upgrade-0.0.299.log')
    Assert-That ($code -in 0, 3010) "msiexec /i 0.0.299 (código $code)"
    $versions = Get-InstalledVersion
    Assert-That (($versions -join ',') -eq '0.0.299') "uma só instalação, 0.0.299 (veio $($versions -join ','))"
    Wait-Until -TimeoutSeconds 30 -Message 'serviço em execução após o upgrade' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    Assert-That ((Get-DataHash) -eq $before) 'config.json e cofre intactos no upgrade'
    Assert-That ((Invoke-Svc @('credential', 'list')) -match '(?m)^Matriz\s+cofre') 'credencial preservada'
    Assert-ServicePolicy
    Assert-DataAcl
    $code = Invoke-Msiexec -Mode '/i' -Msi $OldMsi -LogFile (Join-Path $LogDir 'downgrade-0.0.199.log')
    Assert-That ($code -notin 0, 3010) "downgrade para 0.0.199 recusado (código $code)"
    Assert-That (((Get-InstalledVersion) -join ',') -eq '0.0.299') 'continua 0.0.299'

    Write-Step '8. Desinstala preservando a ProgramData; reinstala; desinstala com PURGE=1'
    $code = Invoke-Msiexec -Mode '/x' -Msi $NewMsi -LogFile (Join-Path $LogDir 'uninstall.log')
    Assert-That ($code -in 0, 3010) "msiexec /x (código $code)"
    Assert-That (-not (Get-Service VPNMonitor -ErrorAction SilentlyContinue)) 'serviço removido'
    Assert-That (-not (Test-Path $InstallDir)) 'Program Files\VPN Monitor removida'
    Assert-That (-not (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name VPNMonitorTray -ErrorAction SilentlyContinue)) 'bandeja fora do Run'
    Assert-That ((Get-DataHash) -eq $before) 'ProgramData preservada (config e cofre)'

    $code = Invoke-Msiexec -Mode '/i' -Msi $NewMsi -LogFile (Join-Path $LogDir 'reinstall.log')
    Assert-That ($code -in 0, 3010) "reinstalação (código $code)"
    Wait-Until -TimeoutSeconds 30 -Message 'serviço em execução após reinstalar' -Condition {
        (Get-Service VPNMonitor -ErrorAction SilentlyContinue).Status -eq 'Running'
    }
    Assert-That ((Get-DataHash) -eq $before) 'reinstalação usa a config existente'
    Assert-DataAcl
    Save-DiagnosticLog -LogDir $LogDir
    $code = Invoke-Msiexec -Mode '/x' -Msi $NewMsi -LogFile (Join-Path $LogDir 'uninstall-purge.log') -Properties @('PURGE=1')
    Assert-That ($code -in 0, 3010) "msiexec /x PURGE=1 (código $code)"
    Assert-That (-not (Get-Service VPNMonitor -ErrorAction SilentlyContinue)) 'serviço removido'
    Assert-That (-not (Test-Path $DataDir)) 'PURGE=1 removeu a ProgramData'

    Write-Host ''
    Write-Host 'e2e do MSI: tudo certo'
}
catch {
    Save-DiagnosticLog -LogDir $LogDir
    throw
}
finally {
    foreach ($name in $Entry, $LinkEntry) {
        Remove-VpnConnection -Name $name -AllUserConnection -Force -ErrorAction SilentlyContinue
    }
}
