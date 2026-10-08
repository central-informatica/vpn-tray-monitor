package shared

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// OpenShared abre path só para leitura permitindo que outros leiam, gravem,
// apaguem ou renomeiem por cima enquanto o handle está aberto. os.Open não
// passa FILE_SHARE_DELETE: com ele aberto, DeleteFile e MoveFileEx no mesmo
// nome falham com violação de compartilhamento.
func OpenShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// ReadFileShared é os.ReadFile sem bloquear a remoção ou a troca do arquivo
// por quem o edita (ver OpenShared).
func ReadFileShared(path string) ([]byte, error) {
	f, err := OpenShared(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenAppendShared abre (ou cria) path só para acrescentar, permitindo que
// outros leiam, gravem, apaguem ou renomeiem o arquivo enquanto estiver
// aberto (FILE_SHARE_DELETE). os.OpenFile não o passa: com duas bandejas no
// mesmo log, o rename da rotação falharia para sempre. perm é ignorado.
func OpenAppendShared(path string, _ os.FileMode) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.FILE_APPEND_DATA,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
