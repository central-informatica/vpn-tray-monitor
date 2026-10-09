package shared

import (
	"os"

	"golang.org/x/sys/windows"
)

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
