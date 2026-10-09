<#
.SYNOPSIS
  Assina arquivos com o signtool (gancho de assinatura da release, spec §10.2).

.DESCRIPTION
  Esqueleto documentado: o pipeline chama este script só quando a variável
  SIGNING_ENABLED do repositório (ou do environment "release") é 'true',
  primeiro com os dois exes e depois com o MSI. O time de infra pluga aqui o
  HSM/serviço de assinatura escolhido; nada de chave ou senha no repositório.

  Configuração por variáveis de ambiente (secrets/vars do environment
  "release", repassadas pelo passo do workflow):

    SIGN_DLIB           DLL de assinatura do provedor (signtool /dlib), por
                        exemplo a do Azure Trusted Signing:
                        C:\tools\TrustedSigning\bin\x64\Azure.CodeSigning.Dlib.dll
    SIGN_DLIB_METADATA  arquivo de metadados lido pela DLL (signtool /dmdf),
                        por exemplo metadata.json com Endpoint, conta e perfil
    SIGN_TIMESTAMP_URL  carimbo de tempo RFC 3161 (signtool /tr)
    SIGN_EXPECTED_SHA1  opcional: thumbprint SHA-1 do certificado esperado;
                        se definida, a verificação usa /sha1 e recusa
                        assinatura feita por outro certificado

  Exemplo do comando que este script monta para cada arquivo:

    signtool sign /v /fd SHA256 /td SHA256 /tr http://timestamp.exemplo
      /dlib Azure.CodeSigning.Dlib.dll /dmdf metadata.json vpnmon-svc.exe

  Para um HSM com KSP próprio (sem /dlib), troque os argumentos por
  /csp "<nome do KSP>" /kc "<nome da chave>" /f <certificado.cer>.
  A autenticação no provedor (ex.: azure/login com OIDC) fica em passos do
  workflow antes deste. Com Azure Trusted Signing por OIDC, o job "package"
  do release.yml passa a precisar de "permissions: id-token: write" (hoje ele
  só herda contents: read).

.EXAMPLE
  pwsh scripts/sign.ps1 -Path build\vpnmon-svc.exe, build\vpnmon-tray.exe
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string[]]$Path
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-SignTool {
    $cmd = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    # Só pastas de versão do SDK (ex.: 10.0.22621.0); outras ficam de fora
    # antes de ordenar.
    $found = Get-ChildItem $kits -Recurse -Filter signtool.exe -ErrorAction SilentlyContinue |
        Where-Object {
            $v = $null
            $_.Directory.Name -eq 'x64' -and [version]::TryParse($_.Directory.Parent.Name, [ref]$v)
        } |
        Sort-Object { [version]$_.Directory.Parent.Name } |
        Select-Object -Last 1
    if (-not $found) { throw 'signtool.exe não encontrado (instale o Windows SDK)' }
    return $found.FullName
}

foreach ($name in 'SIGN_DLIB', 'SIGN_DLIB_METADATA', 'SIGN_TIMESTAMP_URL') {
    if (-not [Environment]::GetEnvironmentVariable($name)) {
        throw "assinatura ligada (SIGNING_ENABLED=true), mas $name não está definida; veja o cabeçalho de scripts/sign.ps1"
    }
}

$signtool = Get-SignTool
$verifyArgs = @('verify', '/pa', '/v')
if ($env:SIGN_EXPECTED_SHA1) { $verifyArgs += @('/sha1', $env:SIGN_EXPECTED_SHA1) }
foreach ($file in $Path) {
    $full = (Resolve-Path $file).Path
    Write-Host "assinando $full"
    & $signtool sign /v /fd SHA256 /td SHA256 /tr $env:SIGN_TIMESTAMP_URL `
        /dlib $env:SIGN_DLIB /dmdf $env:SIGN_DLIB_METADATA $full
    if ($LASTEXITCODE -ne 0) { throw "signtool sign falhou em $full ($LASTEXITCODE)" }
    & $signtool @verifyArgs $full
    if ($LASTEXITCODE -ne 0) { throw "assinatura de $full não confere ($LASTEXITCODE)" }
}
