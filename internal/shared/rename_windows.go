package shared

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// RenameReplace renomeia from para to substituindo to mesmo que outro
// processo o tenha aberto (com FILE_SHARE_DELETE, como OpenShared): usa
// FileRenameInfoEx com FILE_RENAME_POSIX_SEMANTICS|REPLACE_IF_EXISTS
// (Windows 10 1607+, NTFS). Quem já tinha o destino aberto segue lendo o
// conteúdo antigo; aberturas novas veem o novo. Sem esse suporte no SO ou
// no sistema de arquivos, cai no os.Rename (MoveFileEx), que recusa
// substituir um destino aberto.
func RenameReplace(from, to string) error {
	err := renamePOSIX(from, to)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return os.Rename(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

func renamePOSIX(from, to string) error {
	abs, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(src, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	// FILE_RENAME_INFO: união ReplaceIfExists (BOOLEAN) / Flags (DWORD, usada
	// com FileRenameInfoEx) preenchida até o alinhamento do ponteiro,
	// RootDirectory, FileNameLength (bytes, sem o NUL) e FileName (WCHAR[]).
	type renameInfo struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	name, err := windows.UTF16FromString(abs) // com NUL
	if err != nil {
		return err
	}
	size := int(unsafe.Offsetof(renameInfo{}.FileName)) + len(name)*2
	buf := make([]uint64, (size+7)/8) // alinhado para o ponteiro
	ri := (*renameInfo)(unsafe.Pointer(&buf[0]))
	ri.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	ri.FileNameLength = uint32((len(name) - 1) * 2)
	copy(unsafe.Slice(&ri.FileName[0], len(name)), name)
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, (*byte)(unsafe.Pointer(ri)), uint32(size))
}
