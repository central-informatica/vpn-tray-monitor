<#
.SYNOPSIS
  Exemplo de implantação silenciosa do VPN Monitor com credencial.

.DESCRIPTION
  Instala o MSI sem interface, com o seed da primeira VPN, espera o serviço
  subir e grava a credencial pelo stdin (a senha nunca vai para a linha de
  comando nem para o MSI). Roda no Windows PowerShell 5.1 e no PowerShell 7,
  num prompt de administrador; serve de base para um script de Intune (app
  Win32) ou de inicialização de GPO.

  Contexto não interativo (Intune roda como SYSTEM): com -User e sem
  -Password o script cai no Read-Host, que não tem console; nesse caso
  adapte o script para obter a senha de um meio protegido. Nunca a deixe em
  texto no script, no SYSVOL ou na linha de comando.

  A entrada RAS precisa existir para todos os usuários antes:
    Add-VpnConnection -Name "VPN Matriz" -ServerAddress vpn.exemplo -AllUserConnection

.EXAMPLE
  .\deploy-exemplo.ps1 -Msi .\VPNMonitor-2.1.0-x64.msi -Entry "VPN Matriz" -Name Matriz `
      -CheckHost 10.254.1.172 -User "DOMINIO\svc-vpn"
  (pede a senha sem eco)

.EXAMPLE
  $senha = Read-Host -AsSecureString
  .\deploy-exemplo.ps1 -Msi .\VPNMonitor-2.1.0-x64.msi -Entry "VPN Matriz" -User "DOMINIO\svc-vpn" -Password $senha
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Msi,
    # Entrada RAS (VPN_ENTRY).
    [Parameter(Mandatory)][ValidateScript({ $_ -notmatch '"' })][string]$Entry,
    # Nome da VPN no VPN Monitor (VPN_NAME); padrão: igual à entrada.
    [ValidateScript({ $_ -notmatch '"' })][string]$Name,
    [ValidateSet('', 'ping', 'tcp', 'link')][string]$CheckKind = '',
    [ValidateScript({ $_ -notmatch '"' })][string]$CheckHost,
    [string]$CheckPort,
    [string]$Interval,
    # Usuário da VPN; sem ele, nenhuma credencial é gravada (VPN por
    # certificado ou credencial salva no Windows).
    [string]$User,
    [securestring]$Password,
    [string]$LogFile = (Join-Path $env:TEMP 'VPNMonitor-install.log')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# O Intune roda o PowerShell de 32 bits, onde ProgramFiles aponta para
# "Program Files (x86)"; ProgramW6432 é sempre o de 64 bits.
$programFiles = if ($env:ProgramW6432) { $env:ProgramW6432 } else { $env:ProgramFiles }
$svcExe = Join-Path $programFiles 'VPN Monitor\vpnmon-svc.exe'
$installExit = 0

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'rode num prompt de administrador'
}
if (-not $Name) { $Name = $Entry }

# Propriedades do seed (§5.3); valores com espaço vão entre aspas.
$props = @("VPN_ENTRY=`"$Entry`"", "VPN_NAME=`"$Name`"")
if ($CheckKind) { $props += "CHECK_KIND=$CheckKind" }
if ($CheckHost) { $props += "CHECK_HOST=`"$CheckHost`"" }
if ($CheckPort) { $props += "CHECK_PORT=$CheckPort" }
if ($Interval) { $props += "INTERVAL=$Interval" }

$msiArgs = @('/i', "`"$((Resolve-Path $Msi).Path)`"", '/qn', '/norestart', '/l*v', "`"$LogFile`"") + $props
$p = Start-Process -FilePath 'msiexec.exe' -ArgumentList $msiArgs -Wait -PassThru
switch ($p.ExitCode) {
    0 { Write-Host 'VPN Monitor instalado' }
    3010 { $installExit = 3010; Write-Host 'VPN Monitor instalado; a troca de arquivos em uso termina na próxima reinicialização' }
    default { throw "msiexec saiu com $($p.ExitCode); veja $LogFile" }
}

$deadline = (Get-Date).AddSeconds(30)
while ($true) {
    $service = Get-Service VPNMonitor -ErrorAction SilentlyContinue
    if ($service -and $service.Status -eq 'Running') { break }
    if ((Get-Date) -gt $deadline) { throw 'o serviço VPNMonitor não entrou em execução em 30 s' }
    Start-Sleep -Milliseconds 500
}

if ($User) {
    if (-not $Password) { $Password = Read-Host -AsSecureString "Senha de $User" }
    # UTF-8 no stdin do vpnmon-svc: o Windows PowerShell 5.1 mandaria ASCII
    # e trocaria acentos por "?".
    $OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
    try {
        [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) |
            & $svcExe credential set $Name --user $User --password-stdin
        if ($LASTEXITCODE -ne 0) { throw "credential set saiu com $LASTEXITCODE" }
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

& $svcExe status
if ($LASTEXITCODE -ne 0) { throw "status saiu com $LASTEXITCODE" }
exit $installExit
