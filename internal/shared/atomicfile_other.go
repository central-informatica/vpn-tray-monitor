//go:build !windows

package shared

// isTransientRenameErr: fora do Windows o rename por cima do destino é
// atômico e não sofre bloqueio de compartilhamento, então nenhum erro é
// transitório e não há retry.
func isTransientRenameErr(error) bool { return false }
