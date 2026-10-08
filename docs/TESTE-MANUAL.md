# Teste manual no Windows

O que os testes automáticos (Linux e CI) não cobrem: comportamento real do
Windows, do RAS e do shell. Rodar antes de cada release, em Windows 10 e 11,
com o `vpnmon-svc.exe` e o `vpnmon-tray.exe` gerados por `make build`.
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
- [ ] Upgrade com a VPN de pé (fica para o MSI, Marco C).

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
