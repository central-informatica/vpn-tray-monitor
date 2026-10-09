# Teste manual no Windows

O que os testes automáticos (Linux e CI) não cobrem: comportamento real do
Windows, do RAS e do shell. Rodar antes de cada release, em Windows 10 e 11,
com o MSI da release candidata (`vX.Y.Z-rc.N`, artefato do CI ou pré-release)
ou, para serviço e bandeja isolados, com os exes de `make build`.
Marque cada item; anote versão do Windows, escala e o que falhou.

## Serviço (Marco A)

- [ ] Discagem real contra o servidor VPN: conecta, verifica e reconecta após
      derrubar a VPN.
- [ ] PPPoE + VPN: com a internet por PPPoE, a VPN disca e é monitorada;
      conferir que o alias da interface da VPN é o nome da entrada RAS (e que
      o PPPoE conta como rede, não como VPN).
- [ ] Suspender e retomar o computador: o serviço percebe, rediscando se
      preciso, sem estado travado.
- [ ] Senha errada (erro 691): estado "credencial rejeitada", sem novas
      tentativas; bloqueio de 15 min mantido após reiniciar o serviço.
- [ ] RDP com dois usuários logados: um serviço só, VPN única, sem disputa.
- [ ] Upgrade com a VPN de pé: ver "Instalador", abaixo.
- [ ] Após reiniciar o serviço, uma VPN com credencial rejeitada tenta de
      novo **uma vez** ao fim da janela de 15 min; credencial trocada com o
      serviço parado só vale após a janela ou um "Reconectar agora".
- [ ] Discagem manual (rasphone) da mesma entrada enquanto o serviço
      espera na fila: o serviço pode derrubá-la e discar de novo (aceito; o
      resultado final é a VPN de pé).

## Bandeja (Marco B)

### 1. Build e manifest

- [ ] O exe de `make build` abre (manifest com comctl32 v6 e DPI por monitor).
- [ ] Um build sem o `.syso` (sem `make winres`) mostra erro e não trava.

### 2. DPI e recursos

- [ ] Escalas 100, 125, 150, 175 e 200 %: ícone nítido em cada uma.
- [ ] Mover a barra de tarefas entre monitores de escalas diferentes troca o
      ícone para o tamanho certo.
- [ ] Contadores GDI e USER (Gerenciador de Tarefas, aba Detalhes) estáveis
      após horas de uso e ~200 aberturas de menu.

### 3. Explorer

- [ ] Reiniciar o Explorer: o ícone volta com o estado e o tooltip certos.
- [ ] Login com Explorer lento: o ícone aparece quando a barra fica pronta.

### 4. Balões (toasts no Windows 10/11)

- [ ] Ícone de aviso (queda) e de erro (credencial, configuração).
- [ ] Três VPNs caindo juntas viram um balão só.
- [ ] "Não perturbe" (assistente de foco) suprime os balões.
- [ ] Com `notifications=false`, nenhum balão aparece.

### 5. Tooltip

- [ ] Tooltip de ~127 caracteres com acentos e "—" aparece inteiro até o
      limite, sem caracteres quebrados.

### 6. Menu

- [ ] Fecha ao clicar fora.
- [ ] "&" em nomes de VPN aparece literal (não vira atalho sublinhado).
- [ ] Símbolos ● ○ ⚠ aparecem corretamente.
- [ ] Itens de detalhe desabilitados ficam legíveis.
- [ ] Estado mudando com o menu aberto: nada trava; ao reabrir, o menu está
      atualizado.
- [ ] Avaliar a UX do comando de credencial no menu (não dá para copiar).

### 7. Instância única e sessões

- [ ] A 2ª execução na mesma sessão sai em silêncio.
- [ ] Bandeja elevada + bandeja comum na mesma sessão (ACCESS_DENIED no
      mutex): a segunda sai sem erro.
- [ ] Duas sessões RDP ou troca rápida de usuário: cada sessão com sua bandeja.

### 8. Serviço parando e voltando

- [ ] Parar o serviço no services.msc: a bandeja mostra "parado"; iniciar
      de novo: reconecta em até 10 s.
- [ ] Matar o processo do serviço: mesmo comportamento.
- [ ] Suspender e retomar: a bandeja reconecta e mostra o estado atual.

### 9. Usuário padrão

- [ ] Usuário sem privilégio de administrador conecta pelo pipe e todas as
      ações do menu funcionam.
- [ ] Mensagens de erro em português.

### 10. Configurações

- [ ] Layout correto em DPI alto.
- [ ] Erros de validação aparecem junto ao campo.
- [ ] Salvar fica desabilitado até a configuração carregar.
- [ ] Nome de VPN existente somente leitura.
- [ ] Fechar a janela não derruba a bandeja; reabrir funciona.

### 11. Log

- [ ] Quebras de linha `\r\n` corretas.
- [ ] Abre rolado até o fim.
- [ ] Mostra ~60 KB do fim do arquivo.
- [ ] Acentos corretos.

### 12. Ações por VPN

- [ ] Pausar 15 min: mostra "Pausada até HH:MM" e retoma sozinha no horário.
- [ ] Desativar e Ativar.
- [ ] Remover… com nome contendo aspas.

### 13. Credencial rejeitada (691)

- [ ] Menu e balão mostram a credencial rejeitada com o comando completo.
- [ ] Após reiniciar o serviço, o prazo do bloqueio é restaurado e mostrado.

### 14. Logoff e desligamento

- [ ] Sem o aviso "um aplicativo está impedindo o desligamento".

### 15. Windows 11

- [ ] Ícone começa no overflow (^) e pode ser fixado na barra.
- [ ] A identidade do ícone (fixado ou não) depende do caminho do exe:
      mover o exe faz o Windows tratá-lo como ícone novo.

### 16. SmartScreen e antivírus

- [ ] Comportamento com o exe sem assinatura (SmartScreen, Defender e o
      antivírus da empresa).

## Instalador (Marco C)

O e2e do CI já cobre instalação silenciosa, seed, ACL, política do SCM,
upgrade, downgrade, desinstalação e `PURGE=1` num runner limpo. Aqui fica o
que depende de usuário, sessão, domínio ou versão do Windows.

### 17. Instalação interativa

- [ ] Duplo clique no MSI: pede UAC, mostra só o progresso, termina sem erro.
- [ ] A bandeja **não** abre ao fim; o atalho "VPN Monitor" do menu Iniciar
      a abre (sem elevação: confira no Gerenciador de Tarefas, coluna
      "Elevado" = Não).
- [ ] Outro usuário faz login: a bandeja abre sozinha (HKLM Run).
- [ ] "Aplicativos instalados" mostra VPN Monitor com ícone, versão e sem
      "Modificar".
- [ ] `%LOCALAPPDATA%\VPNMonitor\vpnmon-tray.log` criado com "bandeja
      iniciada" e a versão.

### 18. Upgrade

- [ ] Com a VPN conectada, instalar a versão nova por cima: a VPN **não
      cai** (ping contínuo ao alvo durante o upgrade).
- [ ] Com a bandeja aberta em duas sessões (RDP): a bandeja **não** é
      fechada (Restart Manager desligado no MSI); msiexec termina com 3010;
      as bandejas antigas seguem funcionando com o serviço novo; após
      reiniciar, a bandeja nova abre.
- [ ] **A confirmar:** o 3010 (reinício necessário) vem mesmo por causa de
      `MSIRESTARTMANAGERCONTROL=Disable` com a bandeja aberta? Rodar o
      upgrade com `/l*v`, anotar o código de saída e procurar no log o
      arquivo `vpnmon-tray.exe` marcado para troca na reinicialização
      (PendingFileRenameOperations); repetir com a bandeja fechada (esperado:
      código 0). Se a premissa falhar, ajustar o README (Atualização).
- [ ] Atalho do menu Iniciar (anunciado) depois de apagar a origem
      `VPNMonitor` do Event Log (`HKLM\SYSTEM\CurrentControlSet\Services\EventLog\Application\VPNMonitor`):
      dispara reparo do MSI com UAC e recria a origem.
- [ ] "Sobre / versão" na bandeja e `vpnmon-svc version` mostram a versão
      nova.

### 19. Política do serviço

- [ ] `sc qfailure VPNMonitor`: reiniciar 5000/30000/60000 ms, reset 86400;
      `sc qfailureflag VPNMonitor`: TRUE.
- [ ] Matar o `vpnmon-svc.exe` no Gerenciador de Tarefas: o serviço volta
      em ~5 s e as VPNs continuam de pé.
- [ ] Desligar a máquina com a VPN de pé: sem atraso perceptível nem
      evento de serviço que não respondeu.

### 20. Implantação gerenciada

- [ ] GPO (atribuição de computador) com `.mst` contendo o seed numa
      máquina do domínio: instala no boot, config gerada pelo seed.
- [ ] Intune (LOB ou Win32 com `scripts/deploy-exemplo.ps1`): instala,
      grava a credencial e reporta sucesso.
- [ ] `deploy-exemplo.ps1` no Windows PowerShell 5.1 com senha acentuada:
      a VPN disca (a senha chegou intacta).

### 21. Versões do Windows

- [ ] Windows 10 1809, Windows 11 e Windows Server 2019/2022: instala e
      funciona.
- [ ] Windows 10 anterior ao 1809 (ou 32 bits): o MSI recusa com a
      mensagem de requisito.

### 22. Desinstalação

- [ ] Desinstalar com a bandeja aberta: a bandeja não é fechada; msiexec
      termina com 3010 e o exe some na reinicialização; a pasta de dados
      fica (a confirmar: mesmo roteiro do §18, com `msiexec /x` e `/l*v`).
- [ ] Reinstalar: as VPNs e credenciais voltam como estavam.
- [ ] `msiexec /x … PURGE=1`: a pasta de dados some; pastas
      `VPNMonitor.naoconfiavel-*` de quarentena, se houver, ficam (apague à
      mão).

### 23. Assinatura (quando ligada)

- [ ] Propriedades → Assinaturas digitais do MSI e dos exes mostram o
      certificado da empresa com carimbo de tempo.
- [ ] SmartScreen não alerta no download do MSI assinado.
