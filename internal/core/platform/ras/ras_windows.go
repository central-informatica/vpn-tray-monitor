//go:build windows

package ras

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// As funções RAS não estão no x/sys: carregamos da DLL do sistema.
var (
	rasapi32                       = windows.NewLazySystemDLL("rasapi32.dll")
	procRasDialW                   = rasapi32.NewProc("RasDialW")
	procRasHangUpW                 = rasapi32.NewProc("RasHangUpW")
	procRasGetConnectStatusW       = rasapi32.NewProc("RasGetConnectStatusW")
	procRasEnumConnectionsW        = rasapi32.NewProc("RasEnumConnectionsW")
	procRasGetEntryDialParamsW     = rasapi32.NewProc("RasGetEntryDialParamsW")
	procRasConnectionNotificationW = rasapi32.NewProc("RasConnectionNotificationW")
	procRasGetErrorStringW         = rasapi32.NewProc("RasGetErrorStringW")
)

const (
	notifierRasDialFunc = 0 // dwNotifierType: RasDialFunc(UINT, RASCONNSTATE, DWORD)
	rascnDisconnection  = 2 // RASCN_Disconnection
)

// dialCallback é o ÚNICO callback passado ao RasDialW (§4.4): criado uma vez
// na inicialização do pacote (callbacks do Go nunca são liberados e têm
// limite) e não faz nada além de retornar. O andamento é acompanhado por
// RasGetConnectStatus; RasHangUp nunca é chamado daqui.
var dialCallback = windows.NewCallback(func(msg, state, code uintptr) uintptr { return 0 })

type winClient struct {
	// phonebook é passado explicitamente ao RasDialW e ao
	// RasGetEntryDialParamsW: com NULL o Windows escolheria o catálogo
	// "padrão" do contexto, que não é garantidamente o de todos os usuários.
	phonebook string
	pbPtr     *uint16
	mu        sync.Mutex
	// pending mantém vivos os buffers passados ao RasDialW até a discagem
	// terminar (conectada, falha ou hangup).
	pending map[Handle]DialParams
}

// AllUsersPhonebook é o catálogo que o LocalSystem enxerga.
func AllUsersPhonebook() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, `Microsoft\Network\Connections\Pbk\rasphone.pbk`), nil
}

// NewClient cria o cliente RAS real.
func NewClient() (Client, error) {
	if err := rasapi32.Load(); err != nil {
		return nil, fmt.Errorf("carregando rasapi32.dll: %w", err)
	}
	pb, err := AllUsersPhonebook()
	if err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(pb)
	if err != nil {
		return nil, err
	}
	return &winClient{phonebook: pb, pbPtr: ptr, pending: map[Handle]DialParams{}}, nil
}

func (c *winClient) Entries() ([]string, error) {
	raw, err := os.ReadFile(c.phonebook)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParsePhonebook(raw), nil
}

func (c *winClient) Active() ([]ActiveConn, error) {
sizes:
	for _, size := range ConnSizes {
		n := 4
		for attempt := 0; attempt < 4; attempt++ {
			buf := NewConnArray(size, n)
			cb := uint32(len(buf))
			var count uint32
			r, _, _ := procRasEnumConnectionsW.Call(
				uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&cb)), uintptr(unsafe.Pointer(&count)))
			switch r {
			case 0:
				out := make([]ActiveConn, 0, count)
				for _, rc := range DecodeConns(buf, size, int(count)) {
					out = append(out, ActiveConn{Handle: Handle(rc.Handle), Entry: rc.Entry})
				}
				return out, nil
			case ERROR_BUFFER_TOO_SMALL:
				n = int(cb/size) + 1
			case ERROR_INVALID_SIZE:
				continue sizes
			default:
				return nil, &Error{Op: "RasEnumConnectionsW", Code: uint32(r)}
			}
		}
		return nil, &Error{Op: "RasEnumConnectionsW", Code: ERROR_BUFFER_TOO_SMALL}
	}
	return nil, &Error{Op: "RasEnumConnectionsW", Code: ERROR_INVALID_SIZE}
}

func (c *winClient) release(h Handle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pending[h]; ok {
		p.Wipe()
		delete(c.pending, h)
	}
}

func (c *winClient) rawStatus(h Handle) (Status, uint32) {
	for _, size := range ConnStatusSizes {
		buf := NewConnStatusBuffer(size)
		r, _, _ := procRasGetConnectStatusW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])))
		switch r {
		case 0:
			return StatusFromRaw(DecodeConnStatus(buf)), 0
		case ERROR_INVALID_SIZE:
			continue
		default:
			return Status{}, uint32(r)
		}
	}
	return Status{}, ERROR_INVALID_SIZE
}

func (c *winClient) Status(h Handle) (Status, error) {
	st, code := c.rawStatus(h)
	if code == ERROR_INVALID_HANDLE {
		c.release(h)
		return Status{State: StateDisconnected}, nil
	}
	if code != 0 {
		return Status{}, &Error{Op: "RasGetConnectStatusW", Code: code}
	}
	if st.State != StateConnecting {
		c.release(h)
	}
	return st, nil
}

func (c *winClient) StartDial(req DialRequest) (Handle, error) {
	sizes := DialParamsSizes
	if req.Saved != nil {
		sizes = []uint32{req.Saved.params.Size()}
	}
	var last uint32
	for _, size := range sizes {
		p, err := BuildDialParams(req, size)
		if err != nil {
			return 0, err
		}
		var h uintptr
		r, _, _ := procRasDialW.Call(0, uintptr(unsafe.Pointer(c.pbPtr)), uintptr(unsafe.Pointer(&p[0])),
			notifierRasDialFunc, dialCallback, uintptr(unsafe.Pointer(&h)))
		if r == 0 {
			c.mu.Lock()
			c.pending[Handle(h)] = p // mantém o buffer vivo até o fim
			c.mu.Unlock()
			runtime.KeepAlive(p)
			return Handle(h), nil
		}
		if h != 0 {
			_ = c.HangUp(Handle(h)) // falha imediata pode deixar handle aberto
		}
		p.Wipe()
		last = uint32(r)
		if r != ERROR_INVALID_SIZE {
			break
		}
	}
	return 0, &Error{Op: "RasDialW", Code: last}
}

func (c *winClient) HangUp(h Handle) error {
	defer c.release(h)
	r, _, _ := procRasHangUpW.Call(uintptr(h))
	if r != 0 && r != ERROR_NO_CONNECTION && r != ERROR_INVALID_HANDLE {
		return &Error{Op: "RasHangUpW", Code: uint32(r)}
	}
	// A documentação manda esperar o handle ser liberado antes de rediscar.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, code := c.rawStatus(h); code == ERROR_INVALID_HANDLE {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &Error{Op: "RasHangUpW (espera)", Code: ERROR_INVALID_HANDLE}
}

func (c *winClient) Saved(entry string) (*Saved, error) {
	var last uint32
	for _, size := range DialParamsSizes {
		p := NewDialParams(size)
		if err := p.SetEntry(entry); err != nil {
			return nil, err
		}
		var hasPassword int32
		r, _, _ := procRasGetEntryDialParamsW.Call(uintptr(unsafe.Pointer(c.pbPtr)), uintptr(unsafe.Pointer(&p[0])), uintptr(unsafe.Pointer(&hasPassword)))
		if r == 0 {
			s := NewSaved(p, hasPassword != 0)
			p.Wipe()
			return s, nil
		}
		last = uint32(r)
		if r != ERROR_INVALID_SIZE {
			break
		}
	}
	return nil, &Error{Op: "RasGetEntryDialParamsW", Code: last}
}

func (c *winClient) WatchDisconnects(ctx context.Context) (<-chan struct{}, error) {
	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	r, _, _ := procRasConnectionNotificationW.Call(uintptr(windows.InvalidHandle), uintptr(ev), rascnDisconnection)
	if r != 0 {
		windows.CloseHandle(ev)
		return nil, &Error{Op: "RasConnectionNotificationW", Code: uint32(r)}
	}
	ch := make(chan struct{}, 1)
	go func() {
		defer windows.CloseHandle(ev)
		for ctx.Err() == nil {
			s, _ := windows.WaitForSingleObject(ev, 500)
			if s == windows.WAIT_OBJECT_0 {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ch, nil
}

func (c *winClient) ErrorText(code uint32) string {
	buf := make([]uint16, 512)
	r, _, _ := procRasGetErrorStringW.Call(uintptr(code), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r != 0 {
		return fmt.Sprintf("erro %d do Acesso Remoto", code)
	}
	return windows.UTF16ToString(buf)
}
