package ipc

import (
	"bytes"
	"errors"
	"io"
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
		`{"v":1,"type":"status"}]`,
		`{"v":1,"type":"status"}}}}`,
		"{\"v\":1,\"type\":\"a\xff\"}",
		`{"v":1,"type":"status","extra":1,"payload":{"vpn":"a"}}`,
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
	f.Add([]byte(`{"v":1,"type":"addVpn","payload":{"config":{"name":"a","entry":"b"}}}`))
	f.Add([]byte(`{"v":1,"type":"x","payload":[1,2,{"a":"\u0000"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Decode(data)
		if err != nil {
			return
		}
		if m.V != EnvelopeVersion || m.Type == "" {
			t.Fatalf("mensagem inválida aceita: %+v", m)
		}
		var p PauseRequest
		_ = DecodePayload(m.Payload, &p) // não pode entrar em pânico
		var a AddVPNRequest
		_ = DecodePayload(m.Payload, &a)
	})
}

func TestEnvelopeVersionIndependentFromProtocol(t *testing.T) {
	m, err := Decode([]byte(`{"v":1,"id":"h","type":"hello","payload":{"protocol":2,"appVersion":"9"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var h Hello
	if err := DecodePayload(m.Payload, &h); err != nil || h.Protocol != 2 {
		t.Fatalf("%+v %v", h, err)
	}
	if h.Protocol == ProtocolVersion {
		t.Fatal("teste inválido")
	}
	_ = CodeIncompatible
}

func TestCodecBoundary(t *testing.T) {
	pad := func(n int) string { // linha JSON com exatamente n bytes
		base := `{"v":1,"type":"x","payload":""}`
		return `{"v":1,"type":"x","payload":"` + strings.Repeat("a", n-len(base)) + `"}`
	}
	ok := pad(MaxMessage)
	if len(ok) != MaxMessage {
		t.Fatal(len(ok))
	}
	if _, err := NewCodec(bytes.NewBufferString(ok + "\n")).Read(); err != nil {
		t.Fatalf("MaxMessage deve passar: %v", err)
	}
	if _, err := NewCodec(bytes.NewBufferString(pad(MaxMessage+1) + "\n")).Read(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("MaxMessage+1 deve falhar: %v", err)
	}
	mk := func(n int) Message {
		base := `{"v":1,"type":"x","payload":""}`
		return Message{V: 1, Type: "x", Payload: []byte(`"` + strings.Repeat("a", n-len(base)) + `"`)}
	}
	var out bytes.Buffer
	if err := NewCodec(&out).Write(mk(MaxMessage)); err != nil {
		t.Fatalf("escrita MaxMessage: %v", err)
	}
	if err := NewCodec(&out).Write(mk(MaxMessage + 1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("escrita MaxMessage+1: %v", err)
	}
}

func TestCodecUnexpectedEOF(t *testing.T) {
	_, err := NewCodec(bytes.NewBufferString(`{"v":1,"type":"status"}`)).Read()
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("%v", err)
	}
}

func TestWriteValidatesEnvelope(t *testing.T) {
	c := NewCodec(&bytes.Buffer{})
	if err := c.Write(Message{Type: "x"}); err == nil {
		t.Fatal("v obrigatório")
	}
	if err := c.Write(Message{V: EnvelopeVersion}); err == nil {
		t.Fatal("type obrigatório")
	}
}
