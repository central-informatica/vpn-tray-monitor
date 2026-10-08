package ipc

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := NewCodec(&buf)
	until := int64(1700000000)
	if err := c.Write(MustMessage("1", TypePause, PauseRequest{VPN: "Matriz", UntilUnix: &until})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `{"v":1,"id":"1","type":"pause","payload":{"vpn":"Matriz","untilUnix":1700000000}}`) {
		t.Fatalf("formato: %s", buf.String())
	}
	m, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	var p PauseRequest
	if err := DecodePayload(m.Payload, &p); err != nil || p.VPN != "Matriz" || *p.UntilUnix != until {
		t.Fatalf("%+v %v", p, err)
	}
	var null PauseRequest
	if err := DecodePayload([]byte(`{"vpn":"x","untilUnix":null}`), &null); err != nil || null.UntilUnix != nil {
		t.Fatal("null = indefinida")
	}
}

func TestPipeSDDLDoesNotLetUsersCreateInstances(t *testing.T) {
	if !strings.Contains(PipeSDDL, "(A;;0x0012019b;;;IU)") || strings.Contains(PipeSDDL, "GW;;;IU") {
		t.Fatalf("ACE de IU inesperada: %s", PipeSDDL)
	}
	if PipeIUMask&FileCreatePipeInstance != 0 {
		t.Fatal("IU não pode ter FILE_CREATE_PIPE_INSTANCE")
	}
	const fileReadData, fileWriteData = 0x1, 0x2
	if PipeIUMask&fileReadData == 0 || PipeIUMask&fileWriteData == 0 {
		t.Fatal("IU precisa ler e escrever")
	}
}

func TestDecodeRejects(t *testing.T) {
	bad := []string{
		`{"v":1,"type":"status","extra":1}`,
		`{"v":2,"type":"status"}`,
		`{"v":1}`,
		`{"v":1,"type":"status"} {}`,
		`não é json`,
		``,
	}
	for _, in := range bad {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) deveria falhar", in)
		}
	}
	var p VPNRef
	if err := DecodePayload([]byte(`{"vpn":"a","x":1}`), &p); err == nil {
		t.Fatal("payload com campo desconhecido")
	}
	var e *Error
	if err := DecodePayload([]byte(`{"vpn":1}`), &p); !errors.As(err, &e) || e.Code != CodeBadRequest {
		t.Fatalf("erro de payload deve ser bad_request: %v", err)
	}
}

func TestCodecLimits(t *testing.T) {
	big := `{"v":1,"type":"x","payload":"` + strings.Repeat("a", MaxMessage) + "\"}\n"
	c := NewCodec(bytes.NewBufferString(big))
	if _, err := c.Read(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("leitura grande: %v", err)
	}
	var out bytes.Buffer
	w := NewCodec(&out)
	if err := w.Write(MustMessage("1", TypeLogTail, LogTail{Text: strings.Repeat("a", MaxMessage)})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escrita grande: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"v":1,"id":"1","type":"status"}`))
	f.Add([]byte(`{"v":1,"type":"pause","payload":{"vpn":"a","untilUnix":null}}`))
	f.Add([]byte(`{"v":1,"type":"x","payload":[1,2,{"a":"\u0000"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Decode(data)
		if err != nil {
			return
		}
		if m.V != ProtocolVersion || m.Type == "" {
			t.Fatalf("mensagem inválida aceita: %+v", m)
		}
		var p PauseRequest
		_ = DecodePayload(m.Payload, &p) // não pode entrar em pânico
	})
}
