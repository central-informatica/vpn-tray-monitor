//go:build windows

package acl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type winSecurer struct{ logf Logf }

// New devolve o Securer real, sem log.
func New() Securer { return winSecurer{} }

// NewWithLog devolve o Securer real, registrando o que foi posto de lado.
func NewWithLog(logf Logf) Securer { return winSecurer{logf: logf} }

const secInfoOwnerDACL = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

// EnsureDir nunca modifica filhos: só lê a árvore (sem seguir links). Se algo
// destoa, a raiz inteira vai para o lado e é recriada vazia, já com o
// descritor. Uma raiz vazia com dono confiável tem só o descritor reaplicado.
func (s winSecurer) EnsureDir(path string) (bool, error) {
	path = filepath.Clean(path)
	d := Decide(observeTree(path))
	if d.Action == ActionOK {
		return false, nil
	}

	var notice error
	if d.Action == ActionCreate {
		err := createRoot(path)
		if err == nil {
			d = Decision{Action: ActionOK}
		} else if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			// Corrida: alguém criou entre a observação e a criação. Reobserva
			// uma vez e segue o fluxo normal.
			if d = Decide(observeTree(path)); d.Action == ActionCreate {
				return false, fmt.Errorf("criando %s: %w", path, err)
			}
		} else {
			return false, err
		}
	}
	if d.Action == ActionReapplyRoot || d.Action == ActionQuarantine {
		var err error
		if notice, err = s.fix(path, d); err != nil {
			return false, err
		}
	}

	// Verificação final.
	if final := Decide(observeTree(path)); final.Action != ActionOK {
		return true, fmt.Errorf("pasta de dados %s ainda não confere após a correção: %s", path, final.Reason)
	}
	return true, notice
}

// fix trata reaplicar/pôr de lado. Devolve o aviso ErrQuarantined, se houve.
func (s winSecurer) fix(path string, d Decision) (notice, err error) {
	if d.Action == ActionReapplyRoot {
		rerr := reapplyRoot(path)
		if rerr == nil {
			return nil, nil
		}
		d = Decision{ActionQuarantine, fmt.Sprintf("%s; reaplicar falhou: %v", d.Reason, rerr)}
	}
	dest := fmt.Sprintf("%s.naoconfiavel-%s", path, time.Now().Format("20060102-150405.000000000"))
	// os.Rename de um link renomeia o link, não o alvo.
	if err := os.Rename(path, dest); err != nil {
		return nil, fmt.Errorf("pondo %s de lado (%s): %w", path, d.Reason, err)
	}
	if err := createRoot(path); err != nil {
		return nil, err
	}
	if s.logf != nil {
		s.logf("pasta de dados não confiável movida de %s para %s: %s", path, dest, d.Reason)
	}
	return fmt.Errorf("%w: %s -> %s (%s)", ErrQuarantined, path, dest, d.Reason), nil
}

// observeTree lê a raiz e toda a árvore sem seguir links nem modificar nada.
func observeTree(root string) (Entry, []Entry) {
	re := observe(root, ".")
	if !re.Exists || re.Reparse || re.Unreadable || !re.IsDir {
		return re, nil
	}
	var children []Entry
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if p == root && err == nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			rel = p
		}
		if err != nil {
			children = append(children, Entry{Path: rel, Exists: true, Unreadable: true})
			return nil
		}
		e := observe(p, rel)
		children = append(children, e)
		if e.Reparse && d.IsDir() {
			return filepath.SkipDir // nunca desce em ponto de reparse
		}
		return nil
	})
	return re, children
}

// observe lê atributos, dono e DACL de uma entrada, sem seguir link.
func observe(path, display string) Entry {
	e := Entry{Path: display}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		e.Exists, e.Unreadable = true, true
		return e
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return e
		}
		e.Exists, e.Unreadable = true, true
		return e
	}
	e.Exists = true
	e.IsDir = attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	e.Reparse = attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	if e.Reparse {
		return e // não olhamos o alvo
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, secInfoOwnerDACL)
	if err != nil {
		e.Unreadable = true // inclusive acesso negado: não confere
		return e
	}
	e.SDDL = sd.String()
	return e
}

// createRoot cria o último componente já com o descritor (sem janela em que
// a pasta exista só com a herança). Os pais não são nossos: MkdirAll.
func createRoot(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(DirSDDL)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	if err := windows.CreateDirectory(p, sa); err != nil {
		return fmt.Errorf("criando %s: %w", path, err)
	}
	return nil
}

// reapplyRoot reaplica dono e DACL por um handle aberto sem seguir links,
// depois de confirmar pelo handle que a raiz não é ponto de reparse.
func reapplyRoot(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p,
		windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("abrindo %s: %w", path, err)
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("a raiz virou link ou não é pasta")
	}
	sd, err := windows.SecurityDescriptorFromString(DirSDDL)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
}
