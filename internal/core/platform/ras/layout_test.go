package ras

import (
	"encoding/binary"
	"strings"
	"testing"
)

// Deslocamentos calculados à mão a partir do ras.h com pshpack4 (x64).
func TestLayoutOffsets(t *testing.T) {
	cases := []struct {
		name      string
		got, want int
	}{
		{"RASDIALPARAMSW.szEntryName", dpOffEntryName, 4},
		{"RASDIALPARAMSW.szPhoneNumber", dpOffPhoneNumber, 518},
		{"RASDIALPARAMSW.szCallbackNumber", dpOffCallbackNumber, 776},
		{"RASDIALPARAMSW.szUserName", dpOffUserName, 1034},
		{"RASDIALPARAMSW.szPassword", dpOffPassword, 1548},
		{"RASDIALPARAMSW.szDomain", dpOffDomain, 2062},
		{"RASDIALPARAMSW.dwSubEntry", dpOffSubEntry, 2096},
		{"RASDIALPARAMSW.dwCallbackId", dpOffCallbackID, 2100},
		{"RASDIALPARAMSW.dwIfIndex", dpOffIfIndex, 2108},
		{"RASDIALPARAMSW.szEncPassword", dpOffEncPassword, 2112},
		{"RASCONNSTATUSW.rasconnstate", csOffState, 4},
		{"RASCONNSTATUSW.dwError", csOffError, 8},
		{"RASCONNSTATUSW.szDeviceType", csOffDeviceType, 12},
		{"RASCONNSTATUSW.szDeviceName", csOffDeviceName, 46},
		{"RASCONNSTATUSW.szPhoneNumber", csOffPhone, 304},
		{"RASCONNSTATUSW.localEndPoint", csOffLocalEP, 564},
		{"RASCONNSTATUSW.remoteEndPoint", csOffRemoteEP, 584},
		{"RASCONNSTATUSW.rasconnsubstate", csOffSubState, 604},
		{"RASCONNW.hrasconn", cnOffHandle, 4},
		{"RASCONNW.szEntryName", cnOffEntryName, 12},
		{"RASCONNW.szDeviceType", cnOffDeviceType, 526},
		{"RASCONNW.szDeviceName", cnOffDeviceName, 560},
		{"RASCONNW.szPhonebook", cnOffPhonebook, 818},
		{"RASCONNW.dwSubEntry", cnOffSubEntry, 1340},
		{"RASCONNW.guidEntry", cnOffGUIDEntry, 1344},
		{"RASCONNW.dwFlags", cnOffFlags, 1360},
		{"RASCONNW.luid", cnOffLUID, 1364},
		{"RASCONNW.guidCorrelationId", cnOffCorrelation, 1372},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s em %d, quer %d", c.name, c.got, c.want)
		}
	}
	sizes := []struct {
		name string
		got  []uint32
		want []uint32
	}{
		{"RASDIALPARAMSW", DialParamsSizes, []uint32{2120, 2112, 2108}},
		{"RASCONNSTATUSW", ConnStatusSizes, []uint32{608, 564}},
		{"RASCONNW", ConnSizes, []uint32{1388, 1372}},
	}
	for _, s := range sizes {
		for i := range s.want {
			if s.got[i] != s.want[i] {
				t.Errorf("%s tamanhos %v, quer %v", s.name, s.got, s.want)
			}
		}
	}
}

func TestDialParamsFields(t *testing.T) {
	p := NewDialParams(2112)
	if p.Size() != 2112 || len(p) != 2112 {
		t.Fatal("dwSize errado")
	}
	if err := p.SetEntry("VPN Matriz ção"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetUser("ana"); err != nil {
		t.Fatal(err)
	}
	if p.Entry() != "VPN Matriz ção" || p.User() != "ana" {
		t.Fatalf("leitura: %q %q", p.Entry(), p.User())
	}
	if binary.LittleEndian.Uint16(p[dpOffEntryName:]) != 'V' {
		t.Fatal("entrada não está no deslocamento 4")
	}
	if binary.LittleEndian.Uint16(p[dpOffUserName:]) != 'a' {
		t.Fatal("usuário não está no deslocamento 1034")
	}
	before := p.PasswordFingerprint()
	_ = p.SetPassword("s3nha")
	if binary.LittleEndian.Uint16(p[dpOffPassword:]) != 's' {
		t.Fatal("senha não está no deslocamento 1548")
	}
	if p.PasswordFingerprint() == before {
		t.Fatal("impressão digital deveria mudar com a senha")
	}
	p.Wipe()
	if p.PasswordFingerprint() != before {
		t.Fatal("Wipe deveria zerar o campo de senha")
	}
	if err := p.SetEntry(strings.Repeat("x", 257)); err == nil {
		t.Fatal("entrada longa demais deveria falhar em vez de truncar")
	}
	if err := p.SetEntry(strings.Repeat("x", 256)); err != nil {
		t.Fatal("256 caracteres cabem")
	}
}

func TestDecodeConnStatus(t *testing.T) {
	b := NewConnStatusBuffer(608)
	binary.LittleEndian.PutUint32(b[4:], RASCS_Disconnected)
	binary.LittleEndian.PutUint32(b[8:], 691)
	_ = putUTF16(b, 12, maxDeviceType, "vpn")
	_ = putUTF16(b, 46, maxDeviceName, "WAN Miniport (IKEv2)")
	st := DecodeConnStatus(b)
	if st.State != RASCS_Disconnected || st.Error != 691 || st.DeviceType != "vpn" || st.DeviceName != "WAN Miniport (IKEv2)" {
		t.Fatalf("%+v", st)
	}
}

func TestDecodeConns(t *testing.T) {
	b := NewConnArray(1388, 2)
	EncodeConnForTest(b, 1388, 0, 0x1234, "Matriz")
	EncodeConnForTest(b, 1388, 1, 0xdeadbeefcafe, "Filial")
	got := DecodeConns(b, 1388, 2)
	if len(got) != 2 || got[0] != (RasConn{0x1234, "Matriz"}) || got[1] != (RasConn{0xdeadbeefcafe, "Filial"}) {
		t.Fatalf("%+v", got)
	}
}

func TestDecodersShortBuffersDoNotPanic(t *testing.T) {
	for _, n := range []int{0, 3, 11, 12, 30, 100, 563} {
		_ = DecodeConnStatus(make([]byte, n))
	}
	b := NewConnArray(1388, 2)
	EncodeConnForTest(b, 1388, 0, 0x1234, "Matriz")
	if got := DecodeConns(b[:1388+10], 1388, 2); len(got) != 1 || got[0].Entry != "Matriz" {
		t.Fatalf("deveria limitar ao que cabe: %+v", got)
	}
	if got := DecodeConns(b, 1388, 5); len(got) != 2 {
		t.Fatalf("count acima do buffer: %d", len(got))
	}
	for _, c := range []struct {
		size  uint32
		count int
	}{{0, 3}, {4, 3}, {1388, 0}, {1388, -1}} {
		if got := DecodeConns(b, c.size, c.count); len(got) != 0 {
			t.Fatalf("%+v: %+v", c, got)
		}
	}
	if got := DecodeConns(nil, 1388, 1); len(got) != 0 {
		t.Fatal("buffer nil")
	}
}
