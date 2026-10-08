package main

import (
	"net"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/acl"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/dpapi"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/icmp"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/netwatch"
	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/ras"
)

// Platform reúne o acesso ao SO usado pelo serviço; no Windows vem de
// realPlatform, nos testes de fakes.
type Platform struct {
	RAS    ras.Client
	Pinger icmp.Pinger
	Net    netwatch.Watcher
	DPAPI  dpapi.Protector
	// ACL cria o endurecedor da pasta de dados com a função de log dada
	// (no Windows, acl.NewWithLog): o log do serviço fica na própria pasta e
	// ainda não está aberto quando a pasta é endurecida.
	ACL      func(acl.Logf) acl.Securer
	Listen   func() (net.Listener, error)
	Events   logging.EventSink
	ReadSeed config.SeedReader
	// TokenOwner informa o dono padrão do token do processo, só para o log da
	// partida; nil = não registra.
	TokenOwner func() (string, error)
	// ReadFile lê config.json (partida, observador e recarga); nil =
	// shared.ReadFileShared. Os testes simulam com ele a violação de compartilhamento
	// do Windows, onde chmod não torna o arquivo ilegível.
	ReadFile func(string) ([]byte, error)
}
