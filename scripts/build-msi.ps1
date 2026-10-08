<#
.SYNOPSIS
  Gera o MSI do VPN Monitor (WiX v5) a partir dos dois exes já compilados.

.DESCRIPTION
  Usado pelo CI (job msi), pela release e à mão no Windows. Instala o WiX
  5.0.2 como ferramenta global do dotnet se ainda não houver (o WiX v5 é
  .NET 6; DOTNET_ROLL_FORWARD=Major o roda nos runtimes mais novos).

.EXAMPLE
  pwsh scripts/build-msi.ps1 -Semver 2.1.0 -ProductVersion 2.1.99 -BinDir build -OutDir dist
#>
[CmdletBinding()]
param(
    # Versão completa, só para o nome do arquivo (ex.: 2.1.0-rc.1).
    [Parameter(Mandatory)][string]$Semver,
    # ProductVersion do MSI, X.Y.Z (tools/msiversion).
    [Parameter(Mandatory)][ValidatePattern('^\d{1,3}\.\d{1,3}\.\d{1,5}$')][string]$ProductVersion,
    # Pasta com vpnmon-svc.exe e vpnmon-tray.exe.
    [Parameter(Mandatory)][string]$BinDir,
    [Parameter(Mandatory)][string]$OutDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$WixVersion = '5.0.2'
$env:DOTNET_ROLL_FORWARD = 'Major'

function Install-WixIfMissing {
    $wix = Get-Command wix -ErrorAction SilentlyContinue
    $current = if ($wix) { (& wix --version 2>$null | Select-Object -Last 1) } else { '' }
    if ($current -notlike "$WixVersion*") {
        Write-Host "instalando WiX $WixVersion"
        & dotnet tool install --global wix --version $WixVersion
        if ($LASTEXITCODE -ne 0) { throw "dotnet tool install wix falhou ($LASTEXITCODE)" }
        $env:PATH = "$env:USERPROFILE\.dotnet\tools;$env:PATH"
    }
    & wix extension add -g "WixToolset.Util.wixext/$WixVersion"
    if ($LASTEXITCODE -ne 0) { throw "wix extension add falhou ($LASTEXITCODE)" }
}

$repo = Split-Path -Parent $PSScriptRoot
$bin = (Resolve-Path $BinDir).Path
foreach ($exe in 'vpnmon-svc.exe', 'vpnmon-tray.exe') {
    if (-not (Test-Path (Join-Path $bin $exe))) { throw "faltando $exe em $bin" }
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$out = Join-Path (Resolve-Path $OutDir).Path "VPNMonitor-$Semver-x64.msi"

Install-WixIfMissing
& wix build (Join-Path $repo 'installer\Product.wxs') `
    -arch x64 `
    -ext "WixToolset.Util.wixext" `
    -d "ProductVersion=$ProductVersion" `
    -d "BinDir=$bin" `
    -o $out
if ($LASTEXITCODE -ne 0) { throw "wix build falhou ($LASTEXITCODE)" }
Write-Host "MSI: $out"
