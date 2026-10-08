package icmp

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestDecodeReply(t *testing.T) {
	b := make([]byte, ReplySize)
	binary.LittleEndian.PutUint32(b[4:], IP_SUCCESS)
	binary.LittleEndian.PutUint32(b[8:], 12)
	if r := DecodeReply(b); !r.OK || r.RTT != 12*time.Millisecond {
		t.Fatalf("%+v", r)
	}
	binary.LittleEndian.PutUint32(b[4:], IP_DEST_HOST_UNREACHABLE)
	if r := DecodeReply(b); r.OK || r.Status != 11003 {
		t.Fatalf("destino inacessível não é sucesso: %+v", r)
	}
}

func TestIPv4ToUint32(t *testing.T) {
	got, err := IPv4ToUint32(net.ParseIP("10.254.1.172"))
	if err != nil || got != 0xAC01FE0A {
		t.Fatalf("%#x %v", got, err)
	}
	if _, err := IPv4ToUint32(net.ParseIP("::1")); err == nil {
		t.Fatal("IPv6 deve falhar")
	}
}

func TestResolveIPv4Literal(t *testing.T) {
	ip, err := ResolveIPv4(context.Background(), "192.0.2.1")
	if err != nil || ip.String() != "192.0.2.1" {
		t.Fatal(ip, err)
	}
	if _, err := ResolveIPv4(context.Background(), "fe80::1"); err == nil {
		t.Fatal("IPv6 literal deve falhar")
	}
}
