// Package config define a configuração v2 (config.json): tipos, padrões,
// leitura estrita, validação e gravação atômica.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/guibsu/vpn-tray-monitor/internal/shared"
)

// ContentHash é o SHA-256 (hex) dos bytes de config.json: identifica a
// própria gravação do serviço e detecta edição manual.
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DataDirName é o nome da pasta de dados dentro da ProgramData; o serviço e
// o MSI (Directory DATAFOLDER do installer/Product.wxs) usam o mesmo. A
// bandeja usa o mesmo nome para a pasta do seu log no %LOCALAPPDATA%.
const DataDirName = "VPNMonitor"

// Version é a única versão de config aceita.
const Version = 2

// CheckKind é o tipo de verificação de alcance.
type CheckKind string

const (
	CheckPing CheckKind = "ping"
	CheckTCP  CheckKind = "tcp"
	CheckLink CheckKind = "link"
)

// Padrões da §5.2 do spec.
const (
	DefaultCheckTimeout    = 5
	DefaultInterval        = 30
	DefaultFailures        = 3
	DefaultGrace           = 15
	DefaultConnectTimeout  = 60
	DefaultMaxBackoff      = 300
	DefaultLogLevel        = "info"
	DefaultLogMaxSizeMB    = 1
	DefaultLogMaxFiles     = 5
	DefaultNotificationsOn = true
	DefaultVPNEnabled      = true
	DefaultCheckKind       = CheckPing
	maxNameRunes           = 64
)

// Config é a configuração já normalizada (padrões aplicados).
type Config struct {
	Version       int       `json:"version"`
	Notifications bool      `json:"notifications"`
	LogLevel      string    `json:"logLevel"`
	Log           LogConfig `json:"log"`
	VPNs          []VPN     `json:"vpns"`
}

// LogConfig controla a rotação do log.
type LogConfig struct {
	MaxSizeMB int `json:"maxSizeMB"`
	MaxFiles  int `json:"maxFiles"`
}

// VPN é a config de uma VPN. É comparável com ==, o que o orquestrador usa
// para saber se o trecho mudou.
type VPN struct {
	Name                     string `json:"name"`
	RasEntry                 string `json:"rasEntry"`
	Enabled                  bool   `json:"enabled"`
	Check                    Check  `json:"check"`
	IntervalSeconds          int    `json:"intervalSeconds"`
	FailuresBeforeReconnect  int    `json:"failuresBeforeReconnect"`
	GraceAfterConnectSeconds int    `json:"graceAfterConnectSeconds"`
	ConnectTimeoutSeconds    int    `json:"connectTimeoutSeconds"`
	MaxBackoffSeconds        int    `json:"maxBackoffSeconds"`
}

// Check descreve a verificação de alcance.
type Check struct {
	Kind           CheckKind `json:"kind"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	TimeoutSeconds int       `json:"timeoutSeconds"`
}

// Empty é a config válida sem VPNs, gerada quando não há seed nem arquivo.
func Empty() Config {
	return Config{
		Version:       Version,
		Notifications: DefaultNotificationsOn,
		LogLevel:      DefaultLogLevel,
		Log:           LogConfig{MaxSizeMB: DefaultLogMaxSizeMB, MaxFiles: DefaultLogMaxFiles},
		VPNs:          []VPN{},
	}
}

// Formas cruas, com ponteiros, para distinguir "omitido" de "zero".
type rawConfig struct {
	Version       *int     `json:"version"`
	Notifications *bool    `json:"notifications"`
	LogLevel      *string  `json:"logLevel"`
	Log           *rawLog  `json:"log"`
	VPNs          []RawVPN `json:"vpns"`
}

type rawLog struct {
	MaxSizeMB *int `json:"maxSizeMB"`
	MaxFiles  *int `json:"maxFiles"`
}

// RawVPN é uma VPN como chega de fora (arquivo, pipe, CLI): campos omitidos
// ficam nil e recebem o padrão em Normalize.
type RawVPN struct {
	Name                     string    `json:"name"`
	RasEntry                 string    `json:"rasEntry"`
	Enabled                  *bool     `json:"enabled,omitempty"`
	Check                    *RawCheck `json:"check,omitempty"`
	IntervalSeconds          *int      `json:"intervalSeconds,omitempty"`
	FailuresBeforeReconnect  *int      `json:"failuresBeforeReconnect,omitempty"`
	GraceAfterConnectSeconds *int      `json:"graceAfterConnectSeconds,omitempty"`
	ConnectTimeoutSeconds    *int      `json:"connectTimeoutSeconds,omitempty"`
	MaxBackoffSeconds        *int      `json:"maxBackoffSeconds,omitempty"`
}

// RawCheck é a verificação como chega de fora.
type RawCheck struct {
	Kind           CheckKind `json:"kind,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	TimeoutSeconds *int      `json:"timeoutSeconds,omitempty"`
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Normalize aplica os padrões da §5.2 a uma VPN crua.
func (r RawVPN) Normalize() VPN {
	v := VPN{
		Name:                     r.Name,
		RasEntry:                 r.RasEntry,
		Enabled:                  boolOr(r.Enabled, DefaultVPNEnabled),
		IntervalSeconds:          intOr(r.IntervalSeconds, DefaultInterval),
		FailuresBeforeReconnect:  intOr(r.FailuresBeforeReconnect, DefaultFailures),
		GraceAfterConnectSeconds: intOr(r.GraceAfterConnectSeconds, DefaultGrace),
		ConnectTimeoutSeconds:    intOr(r.ConnectTimeoutSeconds, DefaultConnectTimeout),
		MaxBackoffSeconds:        intOr(r.MaxBackoffSeconds, DefaultMaxBackoff),
		Check:                    Check{Kind: DefaultCheckKind, TimeoutSeconds: DefaultCheckTimeout},
	}
	if r.Check != nil {
		if r.Check.Kind != "" {
			v.Check.Kind = r.Check.Kind
		}
		v.Check.Host = r.Check.Host
		v.Check.Port = r.Check.Port
		v.Check.TimeoutSeconds = intOr(r.Check.TimeoutSeconds, DefaultCheckTimeout)
	}
	return v
}

// DecodeStrict decodifica JSON recusando campos desconhecidos e lixo após o
// valor. Aceita BOM UTF-8 no início.
func DecodeStrict(data []byte, out any) error {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON inválido: conteúdo extra após o objeto")
	}
	return nil
}

// Parse lê um config.json: estrito, com padrões aplicados e validado.
func Parse(data []byte) (Config, error) {
	var raw rawConfig
	if err := DecodeStrict(data, &raw); err != nil {
		return Config{}, err
	}
	if raw.Version == nil {
		return Config{}, &ValidationError{Problems: []FieldError{{Field: "version", Message: "campo obrigatório (use 2)"}}}
	}
	c := Empty()
	c.Version = *raw.Version
	c.Notifications = boolOr(raw.Notifications, DefaultNotificationsOn)
	if raw.LogLevel != nil {
		c.LogLevel = *raw.LogLevel
	}
	if raw.Log != nil {
		c.Log.MaxSizeMB = intOr(raw.Log.MaxSizeMB, DefaultLogMaxSizeMB)
		c.Log.MaxFiles = intOr(raw.Log.MaxFiles, DefaultLogMaxFiles)
	}
	for _, rv := range raw.VPNs {
		c.VPNs = append(c.VPNs, rv.Normalize())
	}
	if err := Validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Marshal serializa com indentação, pronto para gravar.
func Marshal(c Config) ([]byte, error) {
	if c.VPNs == nil {
		c.VPNs = []VPN{}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Load lê e valida o arquivo.
func Load(path string) (Config, error) {
	data, err := shared.ReadFileShared(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Save valida e grava de forma atômica. Config inválida nunca é gravada.
// Devolve os bytes gravados (o chamador guarda o hash para ignorar a
// própria gravação ao observar o arquivo).
func Save(path string, c Config) ([]byte, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	data, err := Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := shared.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}
