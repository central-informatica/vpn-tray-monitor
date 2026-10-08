package ras

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

const pbkANSI = "[VPN Matriz]\r\nEncoding=1\r\nType=2\r\n\r\n[ Filial ]\r\nType=2\r\n[]\r\n[VPN Matriz]\r\n"

func TestParsePhonebookANSI(t *testing.T) {
	got := ParsePhonebook([]byte(pbkANSI))
	if want := []string{"VPN Matriz", "Filial"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, quer %v", got, want)
	}
}

func TestParsePhonebookUTF8BOM(t *testing.T) {
	got := ParsePhonebook(append([]byte("\xEF\xBB\xBF"), "[Conexão]\n"...))
	if !reflect.DeepEqual(got, []string{"Conexão"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookCP1252(t *testing.T) {
	// "[Conexão – Matriz]" gravado em ANSI (Windows-1252), não UTF-8.
	raw := []byte("[Conex\xe3o \x96 Matriz]\r\nType=2\r\n")
	got := ParsePhonebook(raw)
	if !reflect.DeepEqual(got, []string{"Conexão – Matriz"}) {
		t.Fatalf("%q", got)
	}
}

func TestParsePhonebookUTF16(t *testing.T) {
	u := utf16.Encode([]rune("[Conexão São Paulo]\r\nType=2\r\n"))
	raw := []byte{0xFF, 0xFE}
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	got := ParsePhonebook(raw)
	if !reflect.DeepEqual(got, []string{"Conexão São Paulo"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookEmpty(t *testing.T) {
	if got := ParsePhonebook(nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookLongLine(t *testing.T) {
	raw := []byte("[A]\r\nX=" + strings.Repeat("y", 2<<20) + "\r\n[B]\r\n")
	if got := ParsePhonebook(raw); !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("%d entradas", len(got))
	}
}

func TestParsePhonebookLoneCR(t *testing.T) {
	got := ParsePhonebook([]byte("[A]\rType=2\r[B]\r"))
	if !reflect.DeepEqual(got, []string{"A", "B"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookUTF16BE(t *testing.T) {
	raw := []byte{0xFE, 0xFF}
	for _, c := range utf16.Encode([]rune("[Conexão]\r\nType=2\r\n")) {
		raw = append(raw, byte(c>>8), byte(c))
	}
	if got := ParsePhonebook(raw); !reflect.DeepEqual(got, []string{"Conexão"}) {
		t.Fatalf("%v", got)
	}
}

func TestParsePhonebookCaseInsensitiveDup(t *testing.T) {
	got := ParsePhonebook([]byte("[vpn]\n[VPN]\n[Outra]\n"))
	if !reflect.DeepEqual(got, []string{"vpn", "Outra"}) {
		t.Fatalf("%v", got)
	}
}
