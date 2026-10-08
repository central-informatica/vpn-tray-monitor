// Package icmp faz eco ICMP pela API do Windows (IcmpSendEcho2). Só o
// status 0 conta como resposta do próprio destino: o ping.exe trata
// "destino inacessível" enviado por um roteador como sucesso.
package icmp

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Result é o resultado de um eco. OK=false sem erro significa "sem resposta"
// (um resultado válido); erro significa que o teste nem pôde ser feito.
type Result struct {
	OK     bool
	RTT    time.Duration
	Status uint32 // IP_STATUS do Windows (0 = IP_SUCCESS)
}

// Pinger envia um eco ICMP.
type Pinger interface {
	Ping(ctx context.Context, host string, timeout time.Duration) (Result, error)
}

// Status de ICMP_ECHO_REPLY relevantes (ipexport.h).
const (
	IP_SUCCESS               = 0
	IP_DEST_NET_UNREACHABLE  = 11002
	IP_DEST_HOST_UNREACHABLE = 11003
	IP_REQ_TIMED_OUT         = 11010
)

// Layout de ICMP_ECHO_REPLY em x64 (alinhamento natural).
const (
	replyOffStatus = 4
	replyOffRTT    = 8
	ReplySize      = 40 // Address,Status,RTT(12) DataSize,Reserved(4) Data*(8) Options(16)
)

// DecodeReply lê Status e RoundTripTime do buffer de resposta.
func DecodeReply(b []byte) Result {
	st := binary.LittleEndian.Uint32(b[replyOffStatus:])
	rtt := binary.LittleEndian.Uint32(b[replyOffRTT:])
	return Result{OK: st == IP_SUCCESS, Status: st, RTT: time.Duration(rtt) * time.Millisecond}
}

// IPv4ToUint32 monta o IPAddr na ordem de bytes que a API espera.
func IPv4ToUint32(ip net.IP) (uint32, error) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, fmt.Errorf("%s não é IPv4", ip)
	}
	return uint32(v4[0]) | uint32(v4[1])<<8 | uint32(v4[2])<<16 | uint32(v4[3])<<24, nil
}

// ResolveIPv4 aceita IPv4 literal ou nome (resolvido só para IPv4).
func ResolveIPv4(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return nil, fmt.Errorf("%s não é IPv4", host)
		}
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, fmt.Errorf("resolvendo %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%q não resolveu para IPv4", host)
	}
	return ips[0], nil
}
