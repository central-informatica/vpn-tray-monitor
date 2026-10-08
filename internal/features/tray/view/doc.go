// Package view desenha a bandeja com github.com/tailscale/walk: ícone,
// menu, balões, janela de Configurações e janela de log. Só desenha o
// modelo do viewmodel e repassa cliques ao cliente; não tem lógica de
// apresentação. Existe só no Windows (os arquivos são _windows.go); este
// arquivo mantém o pacote visível para `go vet ./...` no Linux.
package view
