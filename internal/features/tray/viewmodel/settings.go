package viewmodel

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/guibsu/vpn-tray-monitor/internal/core/config"
	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// Campos do formulário de uma VPN, com os mesmos caminhos dos FieldError
// do serviço (sem o prefixo "vpns[N].").
const (
	FieldName           = "name"
	FieldRasEntry       = "rasEntry"
	FieldKind           = "check.kind"
	FieldHost           = "check.host"
	FieldPort           = "check.port"
	FieldTimeout        = "check.timeoutSeconds"
	FieldInterval       = "intervalSeconds"
	FieldFailures       = "failuresBeforeReconnect"
	FieldGrace          = "graceAfterConnectSeconds"
	FieldConnectTimeout = "connectTimeoutSeconds"
	FieldMaxBackoff     = "maxBackoffSeconds"
	FieldEnabled        = "enabled"
)

// FormFields é a ordem dos campos na janela.
var FormFields = []string{FieldName, FieldRasEntry, FieldKind, FieldHost, FieldPort, FieldTimeout,
	FieldInterval, FieldFailures, FieldGrace, FieldConnectTimeout, FieldMaxBackoff, FieldEnabled}

// FieldLabels são os rótulos da janela.
var FieldLabels = map[string]string{
	FieldName:           "Nome",
	FieldRasEntry:       "Entrada RAS",
	FieldKind:           "Verificação",
	FieldHost:           "Host (IPv4 ou nome)",
	FieldPort:           "Porta TCP",
	FieldTimeout:        "Prazo da verificação (s)",
	FieldInterval:       "Intervalo entre verificações (s)",
	FieldFailures:       "Falhas antes de reconectar",
	FieldGrace:          "Carência após conectar (s)",
	FieldConnectTimeout: "Prazo da discagem (s)",
	FieldMaxBackoff:     "Espera máxima entre tentativas (s)",
	FieldEnabled:        "Ativada",
}

// CheckKinds e LogLevels são as opções das listas da janela.
var (
	CheckKinds = []string{string(config.CheckPing), string(config.CheckTCP), string(config.CheckLink)}
	LogLevels  = []string{"debug", "info", "warn", "error"}
)

// FieldErrors associa um campo do formulário à mensagem a mostrar junto dele.
type FieldErrors map[string]string

// Form é o formulário de uma VPN, com os valores como texto (como nas caixas).
type Form struct {
	IsNew          bool // nome editável; Salvar envia addVpn
	Name           string
	RasEntry       string
	Kind           string
	Host           string
	Port           string
	Timeout        string
	Interval       string
	Failures       string
	Grace          string
	ConnectTimeout string
	MaxBackoff     string
	Enabled        bool
}

func itoa(n int) string { return strconv.Itoa(n) }

// FormFrom preenche o formulário com uma VPN existente (nome somente leitura, §5.2).
func FormFrom(v config.VPN) Form {
	f := Form{Name: v.Name, RasEntry: v.RasEntry, Kind: string(v.Check.Kind), Host: v.Check.Host,
		Timeout: itoa(v.Check.TimeoutSeconds), Interval: itoa(v.IntervalSeconds), Failures: itoa(v.FailuresBeforeReconnect),
		Grace: itoa(v.GraceAfterConnectSeconds), ConnectTimeout: itoa(v.ConnectTimeoutSeconds),
		MaxBackoff: itoa(v.MaxBackoffSeconds), Enabled: v.Enabled}
	if v.Check.Port != 0 {
		f.Port = itoa(v.Check.Port)
	}
	return f
}

// NewForm é o formulário de uma VPN nova, com os padrões da §5.2.
func NewForm() Form {
	f := FormFrom(config.RawVPN{}.Normalize())
	f.IsNew = true
	return f
}

// HostEnabled e PortEnabled dizem se o campo vale para o tipo escolhido.
func (f Form) HostEnabled() bool { return f.Kind != string(config.CheckLink) }
func (f Form) PortEnabled() bool { return f.Kind == string(config.CheckTCP) }

// Command converte o formulário no pedido completo (addVpn ou updateVpn).
// Só confere o que impede montar o pedido (campos vazios, não numéricos,
// tipo desconhecido); limites e regras ficam com o serviço, que responde
// com FieldError por campo.
func (f Form) Command() (Command, FieldErrors) {
	errs := FieldErrors{}
	num := func(field, s string) *int {
		s = strings.TrimSpace(s)
		if s == "" {
			errs[field] = "obrigatório"
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			errs[field] = "use um número inteiro"
			return nil
		}
		return &n
	}
	name := f.Name
	if f.IsNew {
		name = strings.TrimSpace(name)
	}
	if name == "" {
		errs[FieldName] = "obrigatório"
	}
	if !slices.Contains(CheckKinds, f.Kind) {
		errs[FieldKind] = "escolha ping, tcp ou link"
	}
	check := &config.RawCheck{Kind: config.CheckKind(f.Kind), TimeoutSeconds: num(FieldTimeout, f.Timeout)}
	if f.HostEnabled() {
		check.Host = strings.TrimSpace(f.Host)
	}
	if f.PortEnabled() {
		if p := num(FieldPort, f.Port); p != nil {
			check.Port = *p
		}
	}
	enabled := f.Enabled
	raw := config.RawVPN{
		Name: name, RasEntry: f.RasEntry, Enabled: &enabled, Check: check,
		IntervalSeconds:          num(FieldInterval, f.Interval),
		FailuresBeforeReconnect:  num(FieldFailures, f.Failures),
		GraceAfterConnectSeconds: num(FieldGrace, f.Grace),
		ConnectTimeoutSeconds:    num(FieldConnectTimeout, f.ConnectTimeout),
		MaxBackoffSeconds:        num(FieldMaxBackoff, f.MaxBackoff),
	}
	if len(errs) > 0 {
		return Command{}, errs
	}
	if f.IsNew {
		return Command{ipc.TypeAddVPN, ipc.AddVPNRequest{Config: raw}}, nil
	}
	return Command{ipc.TypeUpdateVPN, ipc.UpdateVPNRequest{Name: name, Config: raw}}, nil
}

var vpnPrefix = regexp.MustCompile(`^vpns\[\d+\]\.`)

// ServiceErrors separa a recusa do serviço em mensagens por campo do
// formulário e um aviso geral (campos de fora do formulário, ou a recusa
// inteira quando não há campos — ex.: "config.json foi alterado no disco").
func ServiceErrors(err error) (FieldErrors, string) {
	if err == nil {
		return nil, ""
	}
	var e *ipc.Error
	if !errors.As(err, &e) || len(e.Fields) == 0 {
		return FieldErrors{}, ErrorText(err)
	}
	fields := FieldErrors{}
	var general []string
	for _, fe := range e.Fields {
		key := vpnPrefix.ReplaceAllString(fe.Field, "")
		if !slices.Contains(FormFields, key) {
			general = append(general, fe.Field+": "+fe.Message)
			continue
		}
		if old := fields[key]; old != "" {
			fields[key] = old + "; " + fe.Message
		} else {
			fields[key] = fe.Message
		}
	}
	if len(general) == 0 {
		return fields, "Corrija os campos marcados."
	}
	return fields, strings.Join(general, "\n")
}

// Globals são as opções gerais da janela (§7: avisos e nível de log).
type Globals struct {
	Notifications bool
	LogLevel      string
}

// GlobalsFrom lê as opções da config.
func GlobalsFrom(c config.Config) Globals {
	return Globals{Notifications: c.Notifications, LogLevel: c.LogLevel}
}

// Command monta o setGlobal com as duas opções.
func (g Globals) Command() Command {
	n, l := g.Notifications, g.LogLevel
	return Command{ipc.TypeSetGlobal, ipc.SetGlobalRequest{Notifications: &n, LogLevel: &l}}
}

// VPNNames são os nomes da lista da janela, na ordem da config.
func VPNNames(c config.Config) []string {
	out := make([]string, len(c.VPNs))
	for i, v := range c.VPNs {
		out[i] = v.Name
	}
	return out
}

// FindVPN acha a VPN pelo nome, sem diferenciar maiúsculas.
func FindVPN(c config.Config, name string) (config.VPN, bool) {
	for _, v := range c.VPNs {
		if config.NameKey(v.Name) == config.NameKey(name) {
			return v, true
		}
	}
	return config.VPN{}, false
}

// TextFields são os campos editados em caixa de texto (o resto: entrada RAS
// em caixa editável com sugestões, tipo em lista, ativada em marcação).
var TextFields = []string{FieldName, FieldHost, FieldPort, FieldTimeout, FieldInterval, FieldFailures,
	FieldGrace, FieldConnectTimeout, FieldMaxBackoff}

// textField aponta o campo de texto do formulário (nil se não for de texto).
func (f *Form) textField(field string) *string {
	switch field {
	case FieldName:
		return &f.Name
	case FieldRasEntry:
		return &f.RasEntry
	case FieldHost:
		return &f.Host
	case FieldPort:
		return &f.Port
	case FieldTimeout:
		return &f.Timeout
	case FieldInterval:
		return &f.Interval
	case FieldFailures:
		return &f.Failures
	case FieldGrace:
		return &f.Grace
	case FieldConnectTimeout:
		return &f.ConnectTimeout
	case FieldMaxBackoff:
		return &f.MaxBackoff
	}
	return nil
}

// Text é o valor de um campo de texto ("" para os demais).
func (f Form) Text(field string) string {
	if p := f.textField(field); p != nil {
		return *p
	}
	return ""
}

// WithText devolve o formulário com o campo de texto trocado (os demais
// campos são ignorados).
func (f Form) WithText(field, value string) Form {
	if p := f.textField(field); p != nil {
		*p = value
	}
	return f
}

// KindIndex é a posição do tipo de verificação em CheckKinds (-1 se nenhum).
func (f Form) KindIndex() int { return slices.Index(CheckKinds, f.Kind) }

// WithKindIndex escolhe o tipo pela posição na lista (fora dela, mantém).
func (f Form) WithKindIndex(i int) Form {
	if i >= 0 && i < len(CheckKinds) {
		f.Kind = CheckKinds[i]
	}
	return f
}

// NameReadOnly: só uma VPN nova tem nome editável (§5.2: o nome é a identidade).
func (f Form) NameReadOnly() bool { return !f.IsNew }

// LevelIndex é a posição do nível de log em LogLevels (-1 se nenhum).
func (g Globals) LevelIndex() int { return slices.Index(LogLevels, g.LogLevel) }

// WithLevelIndex escolhe o nível pela posição na lista (fora dela, mantém).
func (g Globals) WithLevelIndex(i int) Globals {
	if i >= 0 && i < len(LogLevels) {
		g.LogLevel = LogLevels[i]
	}
	return g
}

// SelectIndex é a linha a selecionar na lista de VPNs depois de recarregar:
// a VPN dada (sem diferenciar maiúsculas, sem espaços nas pontas), senão a
// primeira; -1 com a lista vazia (abre o formulário de VPN nova).
func SelectIndex(names []string, name string) int {
	if len(names) == 0 {
		return -1
	}
	key := config.NameKey(strings.TrimSpace(name))
	if i := slices.IndexFunc(names, func(n string) bool { return config.NameKey(n) == key }); i >= 0 {
		return i
	}
	return 0
}

// EntryNames são as sugestões da caixa "Entrada RAS" (todas as entradas do
// catálogo de todos os usuários, monitoradas ou não).
func EntryNames(entries []ipc.RasEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
