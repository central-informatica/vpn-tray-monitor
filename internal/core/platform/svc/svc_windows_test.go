//go:build windows

package svc

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Só no job Windows: consulta de PID com acesso mínimo para serviço inexistente.
func TestWindowsServicePIDMissing(t *testing.T) {
	if _, err := servicePIDNamed("VPNMonitorInexistente"); err == nil {
		t.Fatal("serviço não instalado deveria dar erro")
	}
}

// TestWindowsServiceHelper é o "serviço" do teste abaixo: o próprio binário de
// teste, registrado no SCM com -test.run apontando para cá e o nome do
// serviço como argumento posicional. Fora do SCM, não faz nada.
func TestWindowsServiceHelper(t *testing.T) {
	if ok, _ := svc.IsWindowsService(); !ok {
		t.Skip("só roda quando iniciado pelo SCM")
	}
	name := flag.Arg(0)
	_ = runNamed(name, Hooks{Run: func(ctx context.Context) error { <-ctx.Done(); return nil }})
}

// Só no job Windows (exige elevação): Install → start → ServicePID →
// Uninstall com um nome de serviço de teste, sem RasMan como dependência.
func TestWindowsInstallStartPIDUninstall(t *testing.T) {
	if !IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("VPNMonitorTeste%d", os.Getpid())
	if err := installNamed(name, name, exe, nil, "-test.run=^TestWindowsServiceHelper$", name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uninstallNamed(name) })
	if err := installNamed(name, name, exe, nil); err == nil {
		t.Fatal("instalar de novo deveria dizer que já está instalado")
	}

	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.OpenService(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	m.Disconnect()

	var pid uint32
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if pid, err = servicePIDNamed(name); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pid == 0 || pid == uint32(os.Getpid()) {
		t.Fatalf("PID do serviço de teste: %d (%v)", pid, err)
	}
	if err := uninstallNamed(name); err != nil {
		t.Fatal(err)
	}
	if _, err := servicePIDNamed(name); err == nil {
		t.Fatal("após Uninstall o serviço não pode responder")
	}
}
