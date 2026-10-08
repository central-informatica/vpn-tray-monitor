// Package instance garante uma única bandeja por sessão com um mutex
// nomeado (§4.9).
package instance

import "errors"

// TrayMutex é o mutex da bandeja: "Local\" o limita à sessão do usuário.
const TrayMutex = `Local\VPNMonitorTray`

// ErrAlreadyRunning: outra instância já detém a trava nesta sessão.
var ErrAlreadyRunning = errors.New("o VPN Monitor já está aberto nesta sessão")

// classify traduz o erro de criar o mutex: "já existe" e "acesso negado"
// (o mutex foi criado por uma bandeja elevada na mesma sessão, e a DACL dele
// não deixa este processo abri-lo) significam que outra instância o detém.
func classify(err, exists, denied error) error {
	if err != nil && (errors.Is(err, exists) || errors.Is(err, denied)) {
		return ErrAlreadyRunning
	}
	return err
}
