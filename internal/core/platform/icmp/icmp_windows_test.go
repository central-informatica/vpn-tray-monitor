//go:build windows

package icmp

import (
	"context"
	"testing"
	"time"
)

// Só no job Windows: eco real no loopback e num TEST-NET sem resposta.
func TestWindowsPingLoopback(t *testing.T) {
	r, err := New().Ping(context.Background(), "127.0.0.1", 2*time.Second)
	if err != nil || !r.OK {
		t.Fatalf("loopback: %+v %v", r, err)
	}
}

func TestWindowsPingUnreachable(t *testing.T) {
	r, err := New().Ping(context.Background(), "192.0.2.1", time.Second)
	if err != nil || r.OK {
		t.Fatalf("TEST-NET deveria ficar sem resposta: %+v %v", r, err)
	}
}
