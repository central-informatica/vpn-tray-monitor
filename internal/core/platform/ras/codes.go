package ras

import "fmt"

// Tabela única de códigos RAS/Win32 e sua classificação (§4.5).
// Nomes iguais aos do raserror.h / winerror.h.
const (
	ERROR_INVALID_HANDLE                = 6
	ERROR_BUFFER_TOO_SMALL              = 603
	ERROR_CANNOT_FIND_PHONEBOOK_ENTRY   = 623
	ERROR_INVALID_SIZE                  = 632
	ERROR_RESTRICTED_LOGON_HOURS        = 646
	ERROR_ACCT_DISABLED                 = 647
	ERROR_PASSWD_EXPIRED                = 648
	ERROR_NO_DIALIN_PERMISSION          = 649
	ERROR_NO_CONNECTION                 = 668
	ERROR_AUTHENTICATION_FAILURE        = 691
	ERROR_RASMAN_CANNOT_INITIALIZE      = 711
	ERROR_INTERACTIVE_MODE              = 703
	ERROR_PPP_TIMEOUT                   = 718
	ERROR_PPP_NO_PROTOCOLS_CONFIGURED   = 720
	ERROR_PPP_REQUIRED_ADDRESS_REJECTED = 735
	ERROR_DIAL_ALREADY_IN_PROGRESS      = 756
	ERROR_AUTOMATIC_VPN_FAILED          = 800
	ERROR_VPN_TIMEOUT                   = 809
	ERROR_AUTH_PROTOCOL_RESTRICTED      = 812
	ERROR_DNSNAME_NOT_RESOLVABLE        = 868
	ERROR_IPSEC_IKE_AUTH_FAIL           = 13801
	ERROR_IPSEC_IKE_NO_CERT             = 13806
	ERROR_IPSEC_IKE_POLICY_MATCH        = 13868
)

// Class é a classe de um erro de discagem.
type Class int

const (
	// ClassTransitorio: backoff exponencial com jitter. Vale para todo código não listado.
	ClassTransitorio Class = iota
	// ClassCredencial: credencial/autorização rejeitada; para de tentar.
	ClassCredencial
	// ClassConfiguracao: entrada inexistente, exige interação, protocolo, certificado.
	ClassConfiguracao
	// ClassJaDiscando: outro processo está discando a mesma entrada.
	ClassJaDiscando
)

func (c Class) String() string {
	switch c {
	case ClassCredencial:
		return "credencial"
	case ClassConfiguracao:
		return "configuracao"
	case ClassJaDiscando:
		return "ja_discando"
	}
	return "transitorio"
}

var classes = map[uint32]Class{
	ERROR_AUTHENTICATION_FAILURE:        ClassCredencial,
	ERROR_RESTRICTED_LOGON_HOURS:        ClassCredencial,
	ERROR_ACCT_DISABLED:                 ClassCredencial,
	ERROR_PASSWD_EXPIRED:                ClassCredencial,
	ERROR_NO_DIALIN_PERMISSION:          ClassCredencial,
	ERROR_AUTH_PROTOCOL_RESTRICTED:      ClassCredencial,
	ERROR_CANNOT_FIND_PHONEBOOK_ENTRY:   ClassConfiguracao,
	ERROR_INTERACTIVE_MODE:              ClassConfiguracao,
	ERROR_PPP_NO_PROTOCOLS_CONFIGURED:   ClassConfiguracao,
	ERROR_PPP_REQUIRED_ADDRESS_REJECTED: ClassConfiguracao,
	ERROR_IPSEC_IKE_AUTH_FAIL:           ClassConfiguracao,
	ERROR_IPSEC_IKE_NO_CERT:             ClassConfiguracao,
	ERROR_IPSEC_IKE_POLICY_MATCH:        ClassConfiguracao,
	ERROR_DIAL_ALREADY_IN_PROGRESS:      ClassJaDiscando,
}

// Classify devolve a classe do código; desconhecidos são transitórios.
func Classify(code uint32) Class {
	if c, ok := classes[code]; ok {
		return c
	}
	return ClassTransitorio
}

// Error é um código devolvido por uma função RAS.
type Error struct {
	Op   string // função ou etapa (ex.: "RasDialW")
	Code uint32
}

func (e *Error) Error() string { return fmt.Sprintf("%s: erro RAS %d", e.Op, e.Code) }
