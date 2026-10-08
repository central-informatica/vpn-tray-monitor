//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenTestPipe(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\vpnmon-teste-%d-%d`, os.Getpid(), time.Now().UnixNano())
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: PipeSDDL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return name
}

// Só no job Windows: a DACL efetiva do pipe nega Rede e dá a Usuários
// Interativos leitura e escrita SEM FILE_CREATE_PIPE_INSTANCE.
func TestWindowsPipeDACL(t *testing.T) {
	name := listenTestPipe(t)
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.Contains(s, "(D;;") || !strings.Contains(s, ";;;NU)") {
		t.Fatalf("Rede deveria ser negada: %s", s)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	iu, err := windows.CreateWellKnownSid(windows.WinInteractiveSid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(iu) {
			continue
		}
		found = true
		if uint32(ace.Mask)&FileCreatePipeInstance != 0 {
			t.Fatalf("IU pode criar instância do pipe: máscara %#x", ace.Mask)
		}
	}
	if !found {
		t.Fatalf("sem ACE para IU: %s", s)
	}
}

// Só no job Windows: conferência de PID (o próprio PID passa, outro é recusado).
func TestWindowsPipePIDCheck(t *testing.T) {
	name := listenTestPipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	self := func() (uint32, error) { return uint32(os.Getpid()), nil }
	c, err := dialVerified(ctx, name, self)
	if err != nil {
		t.Fatalf("PID do próprio processo deveria passar: %v", err)
	}
	c.Close()
	other := func() (uint32, error) { return 4, nil }
	if _, err := dialVerified(ctx, name, other); err == nil {
		t.Fatal("PID diferente do serviço deve ser recusado")
	}
}

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// Só no job Windows: um cliente sem nenhum dos grupos da ACL (Administradores
// e Usuários Interativos viram "somente negação" num token restrito) é
// recusado pelo pipe com ERROR_ACCESS_DENIED.
func TestWindowsPipeRefusesClientWithoutPermission(t *testing.T) {
	name := listenTestPipe(t)

	var proc windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_IMPERSONATE, &proc); err != nil {
		t.Skipf("sem acesso ao token do processo: %v", err)
	}
	defer proc.Close()
	var disable []windows.SIDAndAttributes
	for _, k := range []windows.WELL_KNOWN_SID_TYPE{windows.WinBuiltinAdministratorsSid, windows.WinInteractiveSid, windows.WinLocalSystemSid} {
		sid, err := windows.CreateWellKnownSid(k)
		if err != nil {
			t.Fatal(err)
		}
		disable = append(disable, windows.SIDAndAttributes{Sid: sid})
	}
	const disableMaxPrivilege = 0x1
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(proc), disableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		t.Skipf("CreateRestrictedToken indisponível: %v", e)
	}
	defer restricted.Close()
	var imp windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenImpersonation, &imp); err != nil {
		t.Skipf("DuplicateTokenEx: %v", err)
	}
	defer imp.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.SetThreadToken(nil, imp); err != nil {
		t.Skipf("sem privilégio para personificar: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := winio.DialPipeContext(ctx, name)
	_ = windows.RevertToSelf()
	if err == nil {
		c.Close()
		t.Fatal("cliente sem permissão conectou no pipe")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("esperava acesso negado, veio %v", err)
	}
}
