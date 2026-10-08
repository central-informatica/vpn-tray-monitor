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

// NewWithLog devolve o Securer real, registrando privilégios não obtidos,
// links removidos e entradas redefinidas.
func NewWithLog(logf Logf) Securer { return winSecurer{logf: logf} }

func (s winSecurer) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// secInfoOwnerDACL é o que lemos de cada entrada.
const secInfoOwnerDACL = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

func (s winSecurer) EnsureDir(path string) (bool, error) {
	s.enablePrivileges()

	root, err := observe(path)
	if err != nil {
		return false, err
	}
	switch DecideRoot(root) {
	case ActionCreateRoot:
		if err := createRoot(path); err != nil {
			return false, err
		}
		return true, nil
	case ActionQuarantineRoot:
		dest := fmt.Sprintf("%s.naoconfiavel-%s", path, time.Now().Format("20060102-150405"))
		// os.Rename de um link renomeia o link, não o alvo.
		if err := os.Rename(path, dest); err != nil {
			return false, fmt.Errorf("pondo %s de lado: %w", path, err)
		}
		if err := createRoot(path); err != nil {
			return false, err
		}
		s.log("pasta de dados não confiável movida de %s para %s", path, dest)
		return true, fmt.Errorf("%w: %s -> %s", ErrQuarantined, path, dest)
	case ActionApplyRoot:
		if err := applyRoot(path); err != nil {
			return false, err
		}
		_, err := s.sanitizeTree(path)
		return true, err
	}
	return s.sanitizeTree(path)
}

// observe lê o estado de uma entrada sem seguir links.
func observe(path string) (Entry, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Entry{}, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return Entry{}, nil
		}
		return Entry{}, fmt.Errorf("lendo atributos de %s: %w", path, err)
	}
	e := Entry{
		Exists:  true,
		IsDir:   attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
		Reparse: attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0,
	}
	if e.Reparse {
		return e, nil // não olhamos o alvo
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, secInfoOwnerDACL)
	if err != nil {
		return Entry{}, fmt.Errorf("lendo a segurança de %s: %w", path, err)
	}
	e.SDDL = sd.String()
	return e, nil
}

// createRoot cria o último componente já com o descritor (sem janela em que
// a pasta exista com a herança de ProgramData). Os pais já existem.
func createRoot(path string) error {
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

// applyRoot: dono = Administradores (tira o WRITE_DAC implícito de quem
// pré-criou a pasta); PROTECTED desliga a herança.
func applyRoot(path string) error {
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
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("aplicando a segurança em %s: %w", path, err)
	}
	return nil
}

// sanitizeTree percorre a árvore sem seguir links: remove pontos de reparse e
// redefine filhos (dono BA, DACL só herdada da raiz).
func (s winSecurer) sanitizeTree(root string) (bool, error) {
	ba, err := windows.StringToSid("BA")
	if err != nil {
		return false, err
	}
	empty, err := windows.ACLFromEntries(nil, nil)
	if err != nil {
		return false, err
	}
	changed := false
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("percorrendo %s: %w", p, err)
		}
		if p == root {
			return nil
		}
		e, err := observe(p)
		if err != nil {
			return err
		}
		switch DecideChild(e) {
		case ActionRemoveLink:
			// os.Remove remove só o link (junção ou symlink), nunca o alvo.
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("removendo o link %s: %w", p, err)
			}
			s.log("link removido da pasta de dados: %s", p)
			changed = true
			if d.IsDir() {
				return filepath.SkipDir
			}
		case ActionResetChild:
			// Dono BA e DACL vazia + UNPROTECTED: sobram só as ACEs herdadas da raiz.
			err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
				windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
				ba, nil, empty, nil)
			if err != nil {
				return fmt.Errorf("redefinindo a segurança de %s: %w", p, err)
			}
			s.log("segurança redefinida: %s", p)
			changed = true
		}
		return nil
	})
	return changed, err
}

// enablePrivileges liga no token do processo os privilégios que permitem
// retomar a posse e ler/gravar a segurança de entradas alheias. Fora de um
// serviço (sem o privilégio) é esperado falhar: só registra.
func (s winSecurer) enablePrivileges() {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		s.log("abrindo o token do processo: %v", err)
		return
	}
	defer tok.Close()
	for _, name := range []string{"SeTakeOwnershipPrivilege", "SeRestorePrivilege", "SeBackupPrivilege"} {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr(name), &luid); err != nil {
			s.log("privilégio %s desconhecido: %v", name, err)
			continue
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		if err := windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil); err != nil {
			s.log("habilitando %s: %v", name, err)
			continue
		}
		// AdjustTokenPrivileges "tem sucesso" mesmo sem atribuir; o código fica no último erro.
		if windows.GetLastError() == windows.ERROR_NOT_ALL_ASSIGNED {
			s.log("privilégio %s não está no token (esperado fora de serviço)", name)
		}
	}
}
