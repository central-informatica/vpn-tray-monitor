//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/guibsu/vpn-tray-monitor/internal/core/platform/svc"
)

// Listen cria o pipe do serviço com a ACL da §6.1. O go-winio já cria a
// primeira instância com FILE_FLAG_FIRST_PIPE_INSTANCE e rejeita clientes
// remotos (PIPE_REJECT_REMOTE_CLIENTS).
func Listen() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL,
		InputBufferSize:    MaxMessage,
		OutputBufferSize:   MaxMessage,
	})
}

// Dial conecta ao serviço e confere que o servidor do pipe é o processo do
// serviço VPNMonitor (PID informado pelo SCM).
func Dial(ctx context.Context) (net.Conn, error) {
	return dialVerified(ctx, PipeName, svc.ServicePID)
}

func dialVerified(ctx context.Context, name string, expected func() (uint32, error)) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("serviço VPN Monitor inacessível: %w", err)
	}
	fd, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, fmt.Errorf("conexão de pipe sem handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid); err != nil {
		c.Close()
		return nil, fmt.Errorf("consultando o servidor do pipe: %w", err)
	}
	want, err := expected()
	if err != nil {
		c.Close()
		return nil, err
	}
	if pid != want {
		c.Close()
		return nil, fmt.Errorf("o pipe %s é servido pelo PID %d, não pelo serviço (PID %d); conexão recusada", name, pid, want)
	}
	return c, nil
}
