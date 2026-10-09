@{
    # Os scripts são de linha de comando (CI, implantação): Write-Host é a
    # saída pretendida, e os nomes de função seguem o verbo aprovado.
    ExcludeRules = @('PSAvoidUsingWriteHost')
}
