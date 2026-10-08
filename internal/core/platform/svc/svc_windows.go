//go:build windows

package svc

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const accepts = svc.AcceptStop | svc.AcceptPreShutdown | svc.AcceptPowerEvent

type handler struct{ hooks Hooks }

func (h handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	reqs := make(chan Request)
	stopWait := h.hooks.StopTimeout
	if stopWait <= 0 {
		stopWait = DefaultStopTimeout
	}
	stop := make(chan struct{})
	defer close(stop)
	send := func(r Request) {
		select {
		case reqs <- r:
		case <-stop: // Loop já terminou
		}
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case c := <-r:
				switch c.Cmd {
				case svc.Interrogate:
					s <- c.CurrentStatus
				case svc.Stop:
					send(Request{Cmd: CmdStop})
				case svc.PreShutdown:
					send(Request{Cmd: CmdPreShutdown})
				case svc.PowerEvent:
					send(Request{Cmd: CmdPowerEvent, EventType: c.EventType})
				}
			}
		}
	}()
	report := func(st State) {
		switch st {
		case StateStartPending:
			s <- svc.Status{State: svc.StartPending, WaitHint: 10000}
		case StateRunning:
			s <- svc.Status{State: svc.Running, Accepts: accepts}
		case StateStopPending:
			s <- svc.Status{State: svc.StopPending, WaitHint: uint32(stopWait / time.Millisecond)}
		}
	}
	return false, Loop(h.hooks, reqs, report)
}

// IsService diz se o processo foi iniciado pelo SCM.
func IsService() (bool, error) { return svc.IsWindowsService() }

// Run entrega o processo ao SCM.
func Run(h Hooks) error { return runNamed(ServiceName, h) }

func runNamed(name string, h Hooks) error { return svc.Run(name, handler{h}) }

// Install registra o serviço (início automático, depende do RasMan,
// LocalSystem, recuperação 5 s/30 s/60 s zerando em 1 dia) e a origem do
// Event Log. É o caminho sem MSI.
func Install(exePath string) error {
	return installNamed(ServiceName, DisplayName, exePath, Dependencies())
}

func installNamed(name, display, exePath string, deps []string, args ...string) (err error) {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(name); err == nil {
		s.Close()
		return fmt.Errorf("o serviço %s já está instalado", name)
	}
	s, err := m.CreateService(name, exePath, mgr.Config{
		DisplayName:  display,
		Description:  Description,
		StartType:    mgr.StartAutomatic,
		Dependencies: deps,
	}, args...)
	if err != nil {
		return err
	}
	defer s.Close()
	// Instalação atômica: se algo falhar depois de criar, remove o serviço.
	defer func() {
		if err != nil {
			_ = s.Delete()
		}
	}()
	if err := applyPolicy(s); err != nil {
		return err
	}
	// O MSI (util:EventSource) ou um install anterior pode já ter registrado a
	// origem; o x/sys devolve um erro de texto ("registry key already exists"),
	// não ERROR_ALREADY_EXISTS, então a checagem é pelo texto.
	if err := eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("registrando a origem do Event Log: %w", err)
	}
	return nil
}

// readPolicy lê a política atual; o que não der para ler fica zerado (e
// por isso diverge e é regravado).
func readPolicy(s *mgr.Service) Policy {
	var p Policy
	if acts, err := s.RecoveryActions(); err == nil {
		for _, a := range acts {
			p.Actions = append(p.Actions, RecoveryStep{Restart: a.Type == mgr.ServiceRestart, Delay: a.Delay})
		}
	}
	if rp, err := s.ResetPeriod(); err == nil {
		p.Reset = time.Duration(rp) * time.Second
	}
	if f, err := s.RecoveryActionsOnNonCrashFailures(); err == nil {
		p.NonCrash = f
	}
	var info struct{ PreshutdownTimeout uint32 }
	var needed uint32
	if err := windows.QueryServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &needed); err == nil {
		p.Preshutdown = time.Duration(info.PreshutdownTimeout) * time.Millisecond
	}
	return p
}

// applyPolicy grava a política do SCM (WantedPolicy): recuperação, também
// quando o serviço para com erro sem crash, e o prazo de preshutdown. Só
// grava o que diverge (DiffPolicy): regravar a recuperação zeraria a
// contagem de falhas do SCM a cada partida.
func applyPolicy(s *mgr.Service) error {
	want := WantedPolicy()
	ch := DiffPolicy(readPolicy(s), want)
	if ch.Recovery {
		var actions []mgr.RecoveryAction
		for _, a := range want.Actions {
			actions = append(actions, mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: a.Delay})
		}
		if err := s.SetRecoveryActions(actions, uint32(want.Reset.Seconds())); err != nil {
			return fmt.Errorf("configurando recuperação: %w", err)
		}
	}
	// Run que termina com erro limpo vira SERVICE_STOPPED com código de saída,
	// não crash: sem este flag a recuperação não dispararia.
	if ch.NonCrash {
		if err := s.SetRecoveryActionsOnNonCrashFailures(want.NonCrash); err != nil {
			return fmt.Errorf("configurando recuperação em falhas sem crash: %w", err)
		}
	}
	if ch.Preshutdown {
		info := struct{ PreshutdownTimeout uint32 }{uint32(want.Preshutdown / time.Millisecond)}
		if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_PRESHUTDOWN_INFO,
			(*byte)(unsafe.Pointer(&info))); err != nil {
			return fmt.Errorf("configurando tempo de preshutdown: %w", err)
		}
	}
	return nil
}

// EnsurePolicy reaplica a política do SCM ao serviço VPNMonitor (só o que
// diverge). O serviço chama na partida: é o que garante a política numa
// instalação pelo MSI. Como LocalSystem, o serviço tem SERVICE_ALL_ACCESS
// sobre si mesmo no descritor padrão do SCM.
func EnsurePolicy() error { return ensurePolicyNamed(ServiceName) }

func ensurePolicyNamed(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	return applyPolicy(s)
}

// Uninstall para o serviço (sem derrubar VPNs) e remove o registro.
func Uninstall() error { return uninstallNamed(ServiceName) }

func uninstallNamed(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Errorf("o serviço %s não está instalado", name)
		}
		return err
	}
	defer s.Close()
	stopped := true
	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		stopped = false
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
			if st, err := s.Query(); err != nil || st.State == svc.Stopped {
				stopped = true
				break
			}
		}
	}
	if err := s.Delete(); err != nil {
		return err
	}
	_ = eventlog.Remove(name)
	if !stopped {
		return fmt.Errorf("o serviço %s foi marcado para exclusão, mas não parou em 15 s; será removido quando parar", name)
	}
	return nil
}

// ServicePID devolve o PID do serviço em execução, com acesso mínimo
// (funciona sem elevação; usado pela bandeja e pela CLI para conferir o
// servidor do pipe).
func ServicePID() (uint32, error) { return servicePIDNamed(ServiceName) }

func servicePIDNamed(name string) (uint32, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(scm)
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenService(scm, n, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(h)
	var st windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if err := windows.QueryServiceStatusEx(h, windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &needed); err != nil {
		return 0, err
	}
	if st.CurrentState != windows.SERVICE_RUNNING || st.ProcessId == 0 {
		return 0, fmt.Errorf("o serviço %s não está em execução", name)
	}
	return st.ProcessId, nil
}

// IsElevated diz se o processo roda elevado (administrador).
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
