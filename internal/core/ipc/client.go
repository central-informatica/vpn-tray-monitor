package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Client é um cliente síncrono simples (CLI). A bandeja (Marco B) terá o
// seu, com reconexão e eventos.
type Client struct {
	conn        net.Conn
	codec       *Codec
	next        int
	ServerApp   string
	CallTimeout time.Duration
}

// Handshake troca hello sobre uma conexão já aberta.
func Handshake(c net.Conn, appVersion string) (*Client, error) {
	cl := &Client{conn: c, codec: NewCodec(c), CallTimeout: 30 * time.Second}
	var h Hello
	if err := cl.Call(TypeHello, Hello{Protocol: ProtocolVersion, AppVersion: appVersion}, &h); err != nil {
		c.Close()
		return nil, err
	}
	cl.ServerApp = h.AppVersion
	return cl, nil
}

// Call envia um pedido e espera a resposta com o mesmo id. Eventos que
// chegarem no meio são ignorados. out pode ser nil.
func (c *Client) Call(typ string, payload any, out any) error {
	c.next++
	id := strconv.Itoa(c.next)
	m, err := NewMessage(id, typ, payload)
	if err != nil {
		return err
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.CallTimeout))
	defer c.conn.SetDeadline(time.Time{})
	if err := c.codec.Write(m); err != nil {
		return err
	}
	for {
		r, err := c.codec.Read()
		if err != nil {
			return err
		}
		if r.ID != id {
			// Erro sem id é do nível da conexão (busy, bad_request antes
			// de fechar): não haverá resposta ao pedido.
			if r.Type == TypeError && r.ID == "" {
				return decodeError(r)
			}
			continue
		}
		switch r.Type {
		case TypeError:
			return decodeError(r)
		case TypeOK, TypeHello:
			if out != nil && len(r.Payload) > 0 {
				return json.Unmarshal(r.Payload, out)
			}
			return nil
		}
		return fmt.Errorf("resposta inesperada %q", r.Type)
	}
}

func decodeError(r Message) error {
	var e Error
	if err := json.Unmarshal(r.Payload, &e); err != nil {
		return fmt.Errorf("resposta de erro ilegível: %w", err)
	}
	return &e
}

// Close fecha a conexão.
func (c *Client) Close() error { return c.conn.Close() }
