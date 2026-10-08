package ras

import "testing"

// A tabela fixa código → constante → classe. Mudar um valor aqui exige
// conferir o raserror.h e a §4.5 do spec.
func TestClassifyTable(t *testing.T) {
	cases := []struct {
		code   uint32
		const_ uint32
		class  Class
	}{
		{718, ERROR_PPP_TIMEOUT, ClassTransitorio},
		{800, ERROR_AUTOMATIC_VPN_FAILED, ClassTransitorio},
		{809, ERROR_VPN_TIMEOUT, ClassTransitorio},
		{868, ERROR_DNSNAME_NOT_RESOLVABLE, ClassTransitorio},
		{691, ERROR_AUTHENTICATION_FAILURE, ClassCredencial},
		{646, ERROR_RESTRICTED_LOGON_HOURS, ClassCredencial},
		{647, ERROR_ACCT_DISABLED, ClassCredencial},
		{648, ERROR_PASSWD_EXPIRED, ClassCredencial},
		{649, ERROR_NO_DIALIN_PERMISSION, ClassCredencial},
		{812, ERROR_SERVER_POLICY, ClassCredencial},
		{623, ERROR_CANNOT_FIND_PHONEBOOK_ENTRY, ClassConfiguracao},
		{703, ERROR_INTERACTIVE_MODE, ClassConfiguracao},
		{720, ERROR_PPP_NO_PROTOCOLS_CONFIGURED, ClassConfiguracao},
		{735, ERROR_PPP_REQUIRED_ADDRESS_REJECTED, ClassConfiguracao},
		{13801, ERROR_IPSEC_IKE_AUTH_FAIL, ClassConfiguracao},
		{13806, ERROR_IPSEC_IKE_NO_CERT, ClassConfiguracao},
		{13868, ERROR_IPSEC_IKE_POLICY_MATCH, ClassConfiguracao},
		{756, ERROR_DIAL_ALREADY_IN_PROGRESS, ClassJaDiscando},
		{711, ERROR_RASMAN_CANNOT_INITIALIZE, ClassTransitorio},
	}
	for _, c := range cases {
		if c.code != c.const_ {
			t.Errorf("constante de %d vale %d", c.code, c.const_)
		}
		if got := Classify(c.code); got != c.class {
			t.Errorf("Classify(%d) = %v, quer %v", c.code, got, c.class)
		}
	}
	for _, unknown := range []uint32{0, 1, 619, 651, 99999} {
		if Classify(unknown) != ClassTransitorio {
			t.Errorf("código %d não listado deve ser transitório", unknown)
		}
	}
}
