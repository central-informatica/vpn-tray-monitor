package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"
)

// ErrTooLarge: linha acima de MaxMessage. A conexão deve ser fechada.
var ErrTooLarge = errors.New("mensagem acima de 64 KB")

// DecodeError é uma linha que não é uma mensagem válida do protocolo.
type DecodeError struct{ Msg string }

func (e *DecodeError) Error() string { return e.Msg }

// Codec lê e escreve mensagens, uma por linha.
type Codec struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
}

// NewCodec embrulha a conexão.
func NewCodec(rw io.ReadWriter) *Codec {
	return &Codec{r: bufio.NewReaderSize(rw, MaxMessage+1), w: rw}
}

// Decode valida uma linha: JSON estrito, versão e tipo presentes.
func Decode(line []byte) (Message, error) {
	line = bytes.TrimRight(line, "\r\n")
	if !utf8.Valid(line) {
		return Message{}, &DecodeError{"UTF-8 inválido"}
	}
	var m Message
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Message{}, &DecodeError{"JSON malformado: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Message{}, &DecodeError{"conteúdo extra na linha"}
	}
	if err := m.validate(); err != nil {
		return Message{}, err
	}
	return m, nil
}

func (m Message) validate() error {
	if m.V != EnvelopeVersion {
		return &DecodeError{fmt.Sprintf("versão de envelope %d não suportada", m.V)}
	}
	if m.Type == "" {
		return &DecodeError{"mensagem sem tipo"}
	}
	return nil
}

// Read lê a próxima mensagem.
func (c *Codec) Read() (Message, error) {
	line, err := c.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return Message{}, ErrTooLarge
	}
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return Message{}, io.ErrUnexpectedEOF
		}
		return Message{}, err
	}
	return Decode(line)
}

// Read e Write aceitam até MaxMessage bytes de conteúdo mais o '\n'.

// Write escreve uma mensagem (seguro para uso concorrente).
func (c *Codec) Write(m Message) error {
	if err := m.validate(); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > MaxMessage {
		return ErrTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}
