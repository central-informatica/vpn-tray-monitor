package fake

import (
	"context"
	"testing"
	"time"
)

func TestFakePinger(t *testing.T) {
	p := NewPinger()
	if r, err := p.Ping(context.Background(), "10.0.0.1", time.Second); err != nil || r.OK {
		t.Fatal("host desconhecido não responde")
	}
	p.SetReachable("10.0.0.1", true)
	if r, _ := p.Ping(context.Background(), "10.0.0.1", time.Second); !r.OK || r.RTT == 0 {
		t.Fatal("deveria responder")
	}
	p.SetBlock(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.Ping(ctx, "10.0.0.1", time.Second); err == nil {
		t.Fatal("bloqueado deve terminar com erro do ctx")
	}
	if p.Calls() != 3 {
		t.Fatal(p.Calls())
	}
	p.SetBlock(false)
	p.SetHang(true)
	done := make(chan struct{})
	go func() { p.Ping(context.Background(), "10.0.0.1", time.Second); close(done) }()
	select {
	case <-done:
		t.Fatal("SetHang deveria travar o Ping")
	case <-time.After(20 * time.Millisecond):
	}
	p.SetHang(false)
	<-done
}

func TestFakeDPAPI(t *testing.T) {
	var d DPAPI
	b, _ := d.Protect([]byte("x"), []byte("e"))
	if got, err := d.Unprotect(b, []byte("e")); err != nil || string(got) != "x" {
		t.Fatal(got, err)
	}
	if _, err := d.Unprotect(b, []byte("f")); err == nil {
		t.Fatal("entropia errada")
	}
}
