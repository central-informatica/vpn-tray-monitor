<#
.SYNOPSIS
  Analisa os scripts PowerShell do repositório com o PSScriptAnalyzer
  (erros e avisos reprovam). Roda no Linux (pwsh) e no Windows.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Version = '1.24.0'
if (-not (Get-Module -ListAvailable PSScriptAnalyzer | Where-Object { $_.Version -eq [version]$Version })) {
    Install-Module PSScriptAnalyzer -RequiredVersion $Version -Scope CurrentUser -Force -Repository PSGallery
}
Import-Module PSScriptAnalyzer -RequiredVersion $Version

$problems = Invoke-ScriptAnalyzer -Path $PSScriptRoot -Recurse -Severity Error, Warning `
    -Settings (Join-Path $PSScriptRoot 'PSScriptAnalyzerSettings.psd1')
$problems | Format-Table -AutoSize ScriptName, Line, Severity, RuleName, Message | Out-String -Width 200 | Write-Host
if ($problems) {
    Write-Error "$(@($problems).Count) problema(s) do PSScriptAnalyzer"
}
Write-Host 'scripts PowerShell sem problemas'
