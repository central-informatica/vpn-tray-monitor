//go:build windows

package svc

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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
	cfg, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartType != mgr.StartAutomatic {
		t.Fatalf("StartType %d, quer automático", cfg.StartType)
	}
	if cfg.ErrorControl != mgr.ErrorNormal {
		t.Fatalf("ErrorControl %d, quer normal (como o MSI)", cfg.ErrorControl)
	}
	acts, err := s.RecoveryActions()
	if err != nil {
		t.Fatal(err)
	}
	wantActs := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if !reflect.DeepEqual(acts, wantActs) {
		t.Fatalf("ações de recuperação %v, quer %v", acts, wantActs)
	}
	if rp, err := s.ResetPeriod(); err != nil || rp != 86400 {
		t.Fatalf("ResetPeriod %d (%v), quer 86400", rp, err)
	}
	if f, err := s.RecoveryActionsOnNonCrashFailures(); err != nil || !f {
		t.Fatalf("flag de falhas sem crash: %v (%v)", f, err)
	}
	if ms := preshutdownMs(t, s); ms != uint32(PreshutdownTimeout/time.Millisecond) {
		t.Fatalf("preshutdown %d ms", ms)
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

// A dependência RasMan é gravada pelo Install real; aqui confere via installNamed
// com a mesma lista que Install usa.
func TestWindowsInstallRecordsRasManDependency(t *testing.T) {
	if !IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("VPNMonitorDep%d", os.Getpid())
	if err := installNamed(name, name, exe, []string{"RasMan"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uninstallNamed(name) })
	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Dependencies, []string{"RasMan"}) {
		t.Fatalf("dependências %v", cfg.Dependencies)
	}
}

// preshutdownMs lê o prazo de preshutdown gravado no SCM.
func preshutdownMs(t *testing.T, s *mgr.Service) uint32 {
	t.Helper()
	var info struct{ PreshutdownTimeout uint32 }
	var needed uint32
	if err := windows.QueryServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &needed); err != nil {
		t.Fatal(err)
	}
	return info.PreshutdownTimeout
}

// Só no job Windows (exige elevação): um serviço registrado sem a política
// (como o MSI faz) a ganha inteira com EnsurePolicy, e reaplicar não falha.
func TestWindowsEnsurePolicyRepairs(t *testing.T) {
	if !IsElevated() {
		t.Skip("exige processo elevado (o runner do CI é)")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("VPNMonitorPol%d", os.Getpid())
	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	s, err := m.CreateService(name, exe, mgr.Config{DisplayName: name, StartType: mgr.StartManual})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uninstallNamed(name) })
	defer s.Close()
	if acts, _ := s.RecoveryActions(); len(acts) != 0 {
		t.Fatalf("serviço novo já com recuperação: %v", acts)
	}
	for i := 0; i < 2; i++ {
		if err := ensurePolicyNamed(name); err != nil {
			t.Fatalf("EnsurePolicy (%d): %v", i+1, err)
		}
	}
	acts, err := s.RecoveryActions()
	if err != nil {
		t.Fatal(err)
	}
	var delays []time.Duration
	for _, a := range acts {
		if a.Type != mgr.ServiceRestart {
			t.Fatalf("ação %v, quer reiniciar", a)
		}
		delays = append(delays, a.Delay)
	}
	if !reflect.DeepEqual(delays, RecoveryDelays()) {
		t.Fatalf("esperas %v", delays)
	}
	if rp, err := s.ResetPeriod(); err != nil || rp != uint32(RecoveryReset.Seconds()) {
		t.Fatalf("ResetPeriod %d (%v)", rp, err)
	}
	if f, err := s.RecoveryActionsOnNonCrashFailures(); err != nil || !f {
		t.Fatalf("flag de falhas sem crash: %v (%v)", f, err)
	}
	if ms := preshutdownMs(t, s); ms != uint32(PreshutdownTimeout/time.Millisecond) {
		t.Fatalf("preshutdown %d ms", ms)
	}
	if ch := DiffPolicy(readPolicy(s), WantedPolicy()); ch.Any() {
		t.Fatalf("política lida diverge depois do EnsurePolicy: %+v", ch)
	}
	if err := ensurePolicyNamed("VPNMonitorInexistente"); err == nil {
		t.Fatal("serviço inexistente deveria dar erro")
	}
}
