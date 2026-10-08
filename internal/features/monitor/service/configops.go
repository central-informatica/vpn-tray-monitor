package service

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
	"github.com/guibsu/vpn-tray-monitor/internal/core/logging"
)

var _ ipc.Backend = (*Orchestrator)(nil)

func invalid(probs []config.FieldError) error {
	msgs := make([]string, len(probs))
	for i, p := range probs {
		msgs[i] = p.Field + ": " + p.Message
	}
	return &ipc.Error{Code: ipc.CodeInvalidConfig, Message: "config inválida: " + strings.Join(msgs, "; "), Fields: probs}
}

// mutate aplica f a uma cópia da config, valida, grava e aplica. Uma config
// inválida nunca é gravada (§5.2). Depois de Stop nada é gravado.
func (o *Orchestrator) mutate(f func(c *config.Config) error) error {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return errStopping()
	}
	if bad := o.diskInvalid; bad != nil {
		o.mu.Unlock()
		return &ipc.Error{Code: ipc.CodeInvalidConfig,
			Message: "config.json em disco está inválido; corrija o arquivo antes de alterar pela bandeja ou CLI: " + bad.Error()}
	}
	c := o.cfg
	c.VPNs = slices.Clone(o.cfg.VPNs)
	o.mu.Unlock()
	if err := f(&c); err != nil {
		return err
	}
	if err := config.Validate(c); err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			return invalid(ve.Problems)
		}
		return err
	}
	// Uma edição manual ainda não recarregada (o observador espera o arquivo
	// assentar) seria apagada pela gravação: recusa até a recarga.
	if cur, err := o.opts.ReadFile(o.opts.Paths.ConfigFile); err == nil {
		o.mu.Lock()
		changed := config.ContentHash(cur) != o.lastWritten
		o.mu.Unlock()
		if changed {
			return &ipc.Error{Code: ipc.CodeInvalidConfig,
				Message: "config.json foi alterado no disco; aguarde a recarga e tente de novo"}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &ipc.Error{Code: ipc.CodeInternal, Message: "lendo config.json: " + err.Error()}
	}
	data, err := config.Save(o.opts.Paths.ConfigFile, c)
	if err != nil {
		return &ipc.Error{Code: ipc.CodeInternal, Message: "gravando config.json: " + err.Error()}
	}
	o.mu.Lock()
	o.lastWritten = config.ContentHash(data)
	o.mu.Unlock()
	o.opts.OnConfig(c)
	if err := o.apply(c); err != nil {
		// Stop chegou durante a gravação: gravado, mas não aplicado agora.
		return &ipc.Error{Code: ipc.CodeInternal,
			Message: "serviço parando; a mudança foi gravada e vale na próxima partida"}
	}
	o.publishConfigStatus(ipc.ConfigStatus{OK: true})
	return nil
}

func indexOf(c *config.Config, name string) int {
	key := config.NameKey(name)
	for i, v := range c.VPNs {
		if config.NameKey(v.Name) == key {
			return i
		}
	}
	return -1
}

func notFound(name string) error {
	return &ipc.Error{Code: ipc.CodeNotFound, Message: fmt.Sprintf("VPN %q não existe", name)}
}

// SetEnabled ativa ou desativa uma VPN.
func (o *Orchestrator) SetEnabled(name string, enabled bool) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		c.VPNs[i].Enabled = enabled
		return nil
	})
}

// AddVPN adiciona uma VPN; campos omitidos recebem os padrões.
func (o *Orchestrator) AddVPN(raw config.RawVPN) error {
	return o.mutate(func(c *config.Config) error {
		v := raw.Normalize()
		probs := config.ValidateVPN(v)
		if indexOf(c, v.Name) >= 0 {
			probs = append(probs, config.FieldError{Field: "name", Message: "já existe uma VPN com esse nome"})
		}
		if len(probs) > 0 {
			return invalid(probs)
		}
		c.VPNs = append(c.VPNs, v)
		return nil
	})
}

// UpdateVPN troca a config de uma VPN. O nome é imutável (§5.2).
func (o *Orchestrator) UpdateVPN(name string, raw config.RawVPN) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		if raw.Name != "" && config.NameKey(raw.Name) != config.NameKey(name) {
			return invalid([]config.FieldError{{Field: "name", Message: "o nome não pode ser alterado; remova e adicione de novo"}})
		}
		raw.Name = c.VPNs[i].Name
		if raw.Enabled == nil { // omitido: fica como está (não volta ao padrão)
			enabled := c.VPNs[i].Enabled
			raw.Enabled = &enabled
		}
		v := raw.Normalize()
		if probs := config.ValidateVPN(v); len(probs) > 0 {
			return invalid(probs)
		}
		c.VPNs[i] = v
		return nil
	})
}

// RemoveVPN remove uma VPN (a credencial no cofre fica; use credential clear).
func (o *Orchestrator) RemoveVPN(name string) error {
	return o.mutate(func(c *config.Config) error {
		i := indexOf(c, name)
		if i < 0 {
			return notFound(name)
		}
		c.VPNs = slices.Delete(c.VPNs, i, i+1)
		return nil
	})
}

// SetGlobal muda avisos e nível de log.
func (o *Orchestrator) SetGlobal(req ipc.SetGlobalRequest) error {
	return o.mutate(func(c *config.Config) error {
		if req.Notifications != nil {
			c.Notifications = *req.Notifications
		}
		if req.LogLevel != nil {
			c.LogLevel = *req.LogLevel
		}
		return nil
	})
}

// GetConfig devolve a config atual.
func (o *Orchestrator) GetConfig() config.Config {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.cfg
	c.VPNs = slices.Clone(o.cfg.VPNs)
	return c
}

// ListRasEntries lista o catálogo de todos os usuários, marcando as monitoradas.
func (o *Orchestrator) ListRasEntries() ([]ipc.RasEntry, error) {
	names, err := o.opts.RAS.Entries()
	if err != nil {
		return nil, err
	}
	c := o.GetConfig()
	out := make([]ipc.RasEntry, 0, len(names))
	for _, n := range names {
		mon := slices.ContainsFunc(c.VPNs, func(v config.VPN) bool { return strings.EqualFold(v.RasEntry, n) })
		out = append(out, ipc.RasEntry{Name: n, Monitored: mon})
	}
	return out, nil
}

// LogTail devolve o fim do log.
func (o *Orchestrator) LogTail(maxBytes int) (string, error) {
	return logging.Tail(o.opts.Paths.LogFile, int64(maxBytes))
}

// ReloadFromDisk relê config.json após edição manual. Ignora a própria
// gravação (mesmo hash), salvo se o arquivo estava marcado inválido: voltar
// ao que o serviço gravou também conserta. Inválida (ou vazia, com o editor
// gravando em dois passos): mantém a anterior, registra no log e no Event Log
// e publica configStatus com o motivo. Lê sob applyMu: uma mutação pelo pipe
// não passa entre a leitura e a aplicação. Depois de Stop não faz nada.
//
// Devolve só a falha de leitura do arquivo (violação de compartilhamento no
// Windows, arquivo ausente...), sem registrá-la: quem observa o arquivo
// repete com espera e, se persistir, chama ConfigUnreadable. Problemas de
// conteúdo são tratados aqui e devolvem nil.
func (o *Orchestrator) ReloadFromDisk() error {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	o.mu.Lock()
	stopped := o.stopped
	o.mu.Unlock()
	if stopped {
		return nil
	}
	data, err := o.opts.ReadFile(o.opts.Paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("lendo config.json: %w", err)
	}
	h := config.ContentHash(data)
	o.mu.Lock()
	same := h == o.lastWritten && o.diskInvalid == nil
	wasUnreadable := o.unreadable
	o.unreadable = false
	o.mu.Unlock()
	if same {
		if wasUnreadable {
			// O arquivo voltou igual ao que está em uso (ex.: apagado e
			// restaurado): nada a reaplicar, mas o aviso de "removido" sai.
			o.opts.Log.Info("config.json legível de novo; configuração em uso mantida")
			o.publishConfigStatus(ipc.ConfigStatus{OK: true})
		}
		return nil
	}
	c, err := config.Parse(data)
	if err != nil {
		o.MarkDiskInvalid(err)
		msg := "config.json inválido; mantendo a config anterior: " + err.Error()
		o.opts.Log.Error(msg)
		o.opts.Events.Warning(msg)
		o.publishConfigStatus(invalidStatus(err))
		return nil
	}
	o.mu.Lock()
	o.lastWritten = h
	o.diskInvalid = nil
	o.mu.Unlock()
	o.opts.Log.Info("config.json recarregado")
	o.opts.OnConfig(c)
	if err := o.apply(c); err != nil {
		return nil // Stop chegou durante a recarga
	}
	o.publishConfigStatus(ipc.ConfigStatus{OK: true})
	return nil
}

// ConfigUnreadable avisa que config.json não pôde ser lido mesmo após as
// novas tentativas da montagem: registra no log e no Event Log e publica
// configStatus com o motivo. A config em uso continua valendo.
// Arquivo apagado tem aviso próprio (recriado, o observador recarrega).
func (o *Orchestrator) ConfigUnreadable(err error) {
	msg := "não foi possível ler config.json; mantendo a config anterior: " + err.Error()
	if errors.Is(err, fs.ErrNotExist) {
		msg = "config.json removido; mantendo a configuração em uso"
	}
	o.mu.Lock()
	o.unreadable = true // a próxima leitura bem-sucedida publica OK
	o.mu.Unlock()
	o.opts.Log.Error(msg)
	o.opts.Events.Warning(msg)
	o.publishConfigStatus(ipc.ConfigStatus{OK: false, Message: msg})
}

// MarkWritten registra o hash do config.json carregado ou gravado fora do
// orquestrador. A montagem chama a cada partida com os bytes carregados (ou
// gravados pelo seed): o observador não o recarrega à toa e mutate só aceita
// gravar por cima de um arquivo com esse hash (senão seria uma edição manual
// ainda não recarregada).
func (o *Orchestrator) MarkWritten(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastWritten = config.ContentHash(data)
}

// MarkDiskInvalid registra que o config.json em disco está inválido (na
// partida, pela montagem; depois, pela recarga). Mudanças pelo pipe ficam
// recusadas até uma recarga válida.
//
// O motivo passa a ir no snapshot (Snapshot.Config): uma bandeja que se
// inscreve depois, inclusive com o arquivo já inválido na partida, o vê.
func (o *Orchestrator) MarkDiskInvalid(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.diskInvalid = err
	o.cfgStatus = invalidStatus(err)
}

// invalidStatus é o configStatus de um config.json inválido, com os
// problemas por campo quando a validação os aponta.
func invalidStatus(err error) ipc.ConfigStatus {
	st := ipc.ConfigStatus{OK: false, Message: err.Error()}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		st.Fields = ve.Problems
	}
	return st
}

// publishConfigStatus guarda o estado da config (vai em todo snapshot) e o
// publica, sob o mesmo lock do snapshot: quem se inscreve não perde a troca.
func (o *Orchestrator) publishConfigStatus(st ipc.ConfigStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfgStatus = st
	o.bus.publish(ipc.MustMessage("", ipc.TypeConfigStatus, st))
}
