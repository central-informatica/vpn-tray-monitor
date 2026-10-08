package shared

import (
	"errors"
	"syscall"
)

// errorSharingViolation é ERROR_SHARING_VIOLATION (32) da API Win32; o pacote
// syscall não o exporta.
const errorSharingViolation = syscall.Errno(32)

// isTransientRenameErr diz se o erro de rename pode sumir sozinho: no Windows,
// substituir um arquivo que outro processo tem aberto sem FILE_SHARE_DELETE
// falha com acesso negado ou violação de compartilhamento.
func isTransientRenameErr(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)
}
