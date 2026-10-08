package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
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
	var m Message
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Message{}, &DecodeError{"JSON malformado: " + err.Error()}
	}
	if dec.More() {
		return Message{}, &DecodeError{"conteúdo extra na linha"}
	}
	if m.V != ProtocolVersion {
		return Message{}, &DecodeError{fmt.Sprintf("versão de mensagem %d não suportada", m.V)}
	}
	if m.Type == "" {
		return Message{}, &DecodeError{"mensagem sem tipo"}
	}
	return m, nil
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

// Write escreve uma mensagem (seguro para uso concorrente).
func (c *Codec) Write(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxMessage {
		return ErrTooLarge
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}
