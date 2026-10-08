# Marco B — decisões tomadas na execução e pendências

Registro gerado do ledger da execução por subagentes (plano: `2026-10-08-marco-b-bandeja.md`).

## Validação

- CI do branch verde (lint, test-linux com piso de cobertura do view-model, test-windows com build do `vpnmon-tray.exe` e recursos).
- A interface walk só foi compilada; o comportamento real é coberto pelo roteiro `docs/TESTE-MANUAL.md`.

## Decisões (Rulings)

- Ruling: nenhum conflito no plano — validado por execução completa.
- Task 5: Ruling: (plan-mandated) hello/inscrição ignoram ctx do Run → saída até ~10 s com serviço travado — context.AfterFunc(ctx, conn.Close) após o Dial + teste (<200 ms); aproveitar: teste do ajuste (b) que realmente exercita done com ctx vivo, laços de teste com prazo, comentário client.go:126 esclarecido
- Task 8: Ruling: "<1 ms" com LastCheckUnix>0 mostrava latência falsa (carência, pós-discagem, reconfiguração zeram LastRTT) — desfazer (c) no VM (0 = omitido); corrigir na origem: domínio grava LastRTT = max(RTT, 1µs) quando o alcance dá certo e o serviço publica LatencyMs arredondado PARA CIMA (sub-ms vira 1 ms) — custo: sub-ms aparece como "1 ms"
- Task 8: Ruling: (plan-mandated) agregação de balões nunca acontecia (TakeBalloon logo após cada Apply) — TakeBalloon(now) só entrega quando o aviso pendente mais antigo tem ≥1,5 s; o tique de 1 s drena; fila limpa ao desligar avisos e ao perder conexão; a view (Task 9/11) chama TakeBalloon(now) no tique — ajustar ao implementar a view
- Task 8: Ruling: CredentialCommand com regras do CRT (dobrar barras antes de aspas e antes da aspa final); teste do balão longo com emoji contando UTF-16
- Task 9: Ruling: (plan-mandated) ErrorText com mensagem vazia mostrava código cru em inglês — mapa de código→texto pt-BR para todos os códigos do ipc; aproveitar: TrimSpace no corte inicial do nome em AddFromEntry; teste de RemoveConfirm com aspas/barras
- Task 11: Ruling: aplicar já os Minors de ciclo de vida GDI e saída (difíceis de achar depois no Windows): Dispose do parcial no erro de loadIcons; liberar ícones antigos só após SetIcon OK e só gravar t.last.Icon/ToolTip em sucesso; call com t.app.Context(); EM_SETSEL ao fim com -1; remover campo morto; validar Caller nil
- Final: Ruling: corrigir em UMA leva: (1) refresh da lista RAS ao abrir o menu se >30 s e a cada 60 s no tique com conexão; (2) spec §6.2/§4.8 atualizados (Snapshot.config, blockedUntilUnix, logTail, aviso com --user); (3) constantes ipc.Notice* amarradas ao domínio por teste; (4) CLI check com o mesmo ceil de ms; + roteiro de teste manual da bandeja em docs/TESTE-MANUAL.md (lista da revisão final)

## Achados menores adiados (triados na revisão final)

- Task 1: minor (deferred): CLI decodifica Snapshot estrito — mesmo binário do serviço, ok; bandeja tolerante (decisão do plano); spec §6.2 sem config/blockedUntilUnix (atualizar na revisão final); retornos ignorados em TestSnapshotCarriesConfigStatus; CLI status não mostra config inválido nem prazo do bloqueio
- Task 2: minor (deferred): faltam testes de borda (off+len==len, length 0xFFFFFFFF, tamanho zero, offset no diretório); caso "BMP" não é DIB real; lado declarado não conferido com o PNG; teste depende da ordem das entradas
- Task 3: minor (deferred): reflow de comentário; ACCESS_DENIED real sem teste (verificar no QA manual com bandeja elevada); CloseHandle sem _= explícito
- Task 4: minor (deferred): ids fora de pending sem contagem; respostas não contam UnknownFields; i inútil no teste; hello com protocol não numérico → "ilegível"
- Task 5: minor (deferred): teste do ajuste (b) não determinístico quanto ao caminho; reset do backoff após sessão longa sem teste (time.Now não injetável); corrida rara ready→Connected com motivo enganoso; testes leem internals
- Task 6: minor (deferred): Truncate sem corte devolve UTF-8 inválido intacto (ToValidUTF8); lacunas de teste de borda (bytes inválidos, emoji em max baixo, virada de ano); corte separa grafemas; ClockTime sem ano
- Task 8: minor (deferred): CLI check imprime RTT.Milliseconds (0ms) vs status ceil (1ms); comentário no Apply sobre perda intencional de aviso pendente ao cair a conexão
- Task 9: minor (deferred): sem teste que percorra todos os códigos do ipc; ValidateVPN no fim de TestAddFromEntry usa o caso anterior; ErrorText ignora Fields
- Task 10: minor (deferred): host de VPN link descartado no update (pedido pelo brief); RasEntry sem TrimSpace; prefixo vpns[N] sem conferir índice; mensagem imprecisa em estouro de int
- Task 11: minor (deferred): Configurações sem "tentar de novo" após falha do getConfig; ICO sem 20/24 px exatos (conferir)
- Task 12: minor (deferred): falha persistente de SetIcon/SetToolTip gera Warn por tique (limitar); bandeja descarta logs (decidir log em arquivo no Marco C); appVersion chamado 2x; erro de UTF16PtrFromString ignorado em fatal
- Task 13: minor (deferred): go-winres via go run sem hash em go.sum (considerar tool no go.mod) e versão duplicada Makefile/CI; CI sem --product-version; chaves false redundantes no winres.json
- Final: re-review da leva: todos resolvidos; pronto p/ PR. Minor novo: rasBusy barra o pedido do snapshot (lista velha por até 60 s em janela rara) — Marco C
