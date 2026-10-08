package ras

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ParsePhonebook extrai os nomes de entrada de um rasphone.pbk (INI: cada
// seção [Nome] é uma entrada). O Windows grava ora em ANSI/UTF-8, ora em
// UTF-16LE com BOM; os dois são aceitos. UTF-16 LE e BE (com BOM) são aceitos. Nomes repetidos (sem distinguir
// maiúsculas) aparecem uma vez, com a grafia da primeira ocorrência.
func ParsePhonebook(raw []byte) []string {
	text := decodePhonebook(raw)
	var names []string
	seen := map[string]bool{}
	// Divisão própria (sem bufio.Scanner, que tem limite de linha): aceita
	// \r\n, \r sozinho e \n como fim de linha.
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
	for _, l := range lines {
		line := strings.TrimSpace(l)
		if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
			continue
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		key := strings.ToLower(name)
		if name != "" && !seen[key] {
			seen[key] = true
			names = append(names, name)
		}
	}
	return names
}

func decodePhonebook(raw []byte) string {
	bigEndian := bytes.HasPrefix(raw, []byte{0xFE, 0xFF})
	if !bigEndian && !bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))
		if utf8.Valid(raw) {
			return string(raw)
		}
		return decodeCP1252(raw) // "ANSI" do Windows em português
	}
	body := raw[2:]
	u := make([]uint16, 0, len(body)/2)
	for i := 0; i+1 < len(body); i += 2 {
		if bigEndian {
			u = append(u, uint16(body[i])<<8|uint16(body[i+1]))
		} else {
			u = append(u, uint16(body[i])|uint16(body[i+1])<<8)
		}
	}
	return string(utf16.Decode(u))
}

// cp1252High mapeia 0x80–0x9F do Windows-1252 (o resto coincide com Latin-1).
var cp1252High = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8D, 'Ž', 0x8F,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9D, 'ž', 'Ÿ',
}

func decodeCP1252(raw []byte) string {
	var b strings.Builder
	for _, c := range raw {
		switch {
		case c >= 0x80 && c <= 0x9F:
			b.WriteRune(cp1252High[c-0x80])
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}
