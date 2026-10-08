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
    $started = @(Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'VPNMonitor' } -MaxEvents 20 |
            Where-Object Message -Like '*iniciado*')
    Assert-That ($started.Count -gt 0) 'Event Log: "VPN Monitor … iniciado" (origem registrada pelo MSI)'
    # Sem a DLL de mensagens certa, o Windows mostra "The description for Event
    # ID … cannot be found" com o texto como inserção.
    $broken = @($started | Where-Object Message -Like '*cannot be found*')
    Assert-That ($broken.Count -eq 0) "Event Log: mensagem resolvida pela EventCreate.exe ($($started[0].Message))"
    $run = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name VPNMonitorTray
    Assert-That ($run.VPNMonitorTray -eq "`"$InstallDir\vpnmon-tray.exe`"") 'bandeja no HKLM Run'
    Assert-That (Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\VPN Monitor.lnk") 'atalho no menu Iniciar'

    Write-Step '4. Reconectando → erro transitório → backoff crescente; segunda VPN (link)'
    $delays = @{}
    $sawReconnecting = $false
    $sawTransient = $false
    Wait-Until -TimeoutSeconds 300 -IntervalMs 500 -Message 'três tentativas com erro transitório' -Condition {
        $m = Get-VpnView -Name 'Matriz'
        if ($m.state -eq 'Reconectando') { $script:sawReconnecting = $true }
        $next = Get-NextAttemptUnix -View $m
        if ($m.attempt -gt 0 -and $next -gt 0 -and -not $delays.ContainsKey([int]$m.attempt)) {
            $delays[[int]$m.attempt] = $next - [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
            Write-Host "tentativa $($m.attempt): próxima em $($delays[[int]$m.attempt]) s ($($m.state))"
        }
        $err = if ($m.PSObject.Properties['lastError']) { $m.lastError } else { $null }
        if ($err) {
            if ($err.class -ne 'transitorio') {
                throw "erro não transitório: classe $($err.class), código $($err.code): $($err.message)"
            }
            $script:sawTransient = $true
        }
        $delays.Count -ge 3
    }
    Assert-That $sawReconnecting 'estado Reconectando observado'
    Assert-That $sawTransient 'lastError de classe transitorio observado'
    $keys = @($delays.Keys | Sort-Object)
    $d1, $d2, $d3 = $delays[$keys[0]], $delays[$keys[1]], $delays[$keys[2]]
    $shown = ($keys | ForEach-Object { "$_=$($delays[$_])s" }) -join ', '
    # Esperas nominais ~5/10/20 s (base = intervalo de 5 s, dobrando, jitter
    # ±20 %), medidas na 1ª sondagem que as vê (até ~2 s a menos). No CI veio
    # 4/10/19 e 5/12/22: a ordem estrita tem margem. d3 − d1 ≥ 6 separa
    # backoff dobrando (pior caso d1 ≈ 7, d3 ≈ 14,5) de espera constante (~5 s).
    Assert-That ($d1 -lt $d2 -and $d2 -lt $d3) "backoff crescente d1 < d2 < d3 ($shown)"
    Assert-That ($d3 - $d1 -ge 6) "backoff cresce ao menos 6 s entre a 1ª e a 3ª espera ($shown)"
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
    # A tentativa N arma a espera (nextAttemptUnix = T > 0). Vencido T, o
    # status zera nextAttemptUnix (ToView) já antes da fila e do RasDial, com a
    # mesma tentativa e estado Reconectando, até a discagem falhar (~20 s
    # contra o TEST-NET) e a tentativa N+1 armar a próxima espera. Discagem em
    # curso = esse quadro há ≥ 3 s depois de T E a linha "discando" da Matriz
    # no log do serviço com horário ≥ T.
    $serviceLog = Join-Path $DataDir 'logs\vpnmon.log'
    $script:armedAttempt = -1
    $script:armedAt = [long]0
    Wait-Until -TimeoutSeconds 240 -IntervalMs 500 -Message 'discagem em curso' -Condition {
        $m = Get-VpnView -Name 'Matriz'
        $next = Get-NextAttemptUnix -View $m
        if ($next -gt 0) {
            if ($m.attempt -ne $script:armedAttempt -or $next -ne $script:armedAt) {
                $script:armedAttempt = $m.attempt
                $script:armedAt = $next
                Write-Host "tentativa $($m.attempt) arma a próxima discagem para $([DateTimeOffset]::FromUnixTimeSeconds($next).ToString('HH:mm:ss')) UTC ($($m.state))"
            }
            return $false
        }
        $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
        if (-not ($script:armedAt -gt 0 -and $m.attempt -eq $script:armedAttempt -and
                $m.state -eq 'Reconectando' -and $now -ge $script:armedAt + 3)) {
            return $false
        }
        # slog em texto: time=2026-10-08T23:44:40.123Z level=INFO msg=discando vpn=Matriz …
        $dialLine = Get-Content $serviceLog -Tail 200 -ErrorAction SilentlyContinue |
            Where-Object { $_ -match '^time=(\S+) .*\bmsg=discando\b.*\bvpn=Matriz(\s|$)' -and
                [DateTimeOffset]::Parse($Matches[1]).ToUnixTimeSeconds() -ge $script:armedAt } |
            Select-Object -Last 1
        if (-not $dialLine) { return $false }
        Write-Host "discagem em curso: $($now - $script:armedAt) s após T, tentativa $($m.attempt), estado $($m.state), nextAttemptUnix 0"
        Write-Host "log do serviço: $dialLine"
        $true
    }
    $sw = [Diagnostics.Stopwatch]::StartNew()
    & sc.exe stop VPNMonitor | Out-Null
    Wait-Until -TimeoutSeconds 30 -IntervalMs 100 -Message 'serviço parado' -Condition {
        (Get-Service VPNMonitor).Status -eq 'Stopped'
    }
    $sw.Stop()
    Assert-That ($sw.Elapsed.TotalSeconds -le 10) "parada em $([math]::Round($sw.Elapsed.TotalSeconds, 1)) s"
    # A parada cancela a discagem: a entrada não pode ficar conectando.
    Wait-Until -TimeoutSeconds 10 -Message "entrada $Entry desconectada após a parada" -Condition {
        (Get-VpnConnection -Name $Entry -AllUserConnection).ConnectionStatus -eq 'Disconnected'
    }
    $rasdial = & rasdial.exe 2>&1 | Out-String
    Assert-That ($rasdial -notmatch [regex]::Escape($Entry)) "rasdial não lista $Entry ($($rasdial.Trim()))"
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
    Assert-That ($before -match 'config\.json=' -and $before -match '\.bin=') "hash cobre config.json e o cofre ($before)"
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
    # Trecho ASCII do DowngradeErrorMessage do Product.wxs (o log do msiexec
    # pode vir em ANSI e estragar os acentos).
    $downgradeLog = Get-Content (Join-Path $LogDir 'downgrade-0.0.199.log') -Raw
    Assert-That ($downgradeLog -match 'Desinstale-a antes de instalar esta') 'log do msiexec mostra a mensagem de downgrade do MSI'
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
