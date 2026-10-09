//go:build windows

package icmp

import (
	"context"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpSendEcho2   = iphlpapi.NewProc("IcmpSendEcho2")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
)

const payloadSize = 32

type winPinger struct{}

// New devolve o Pinger real.
func New() Pinger { return winPinger{} }

func (winPinger) Ping(ctx context.Context, host string, timeout time.Duration) (Result, error) {
	ip, err := ResolveIPv4(ctx, host)
	if err != nil {
		return Result{}, err
	}
	dst, _ := IPv4ToUint32(ip)
	h, _, errno := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(windows.InvalidHandle) {
		return Result{}, fmt.Errorf("IcmpCreateFile: %w", errno)
	}
	defer func() { _, _, _ = procIcmpCloseHandle.Call(h) }()

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte('a' + i%23)
	}
	reply := make([]byte, ReplySize+payloadSize+8+64) // margem pedida pela documentação
	ms := timeout.Milliseconds()
	if ms <= 0 {
		ms = 1000
	}
	n, _, callErr := procIcmpSendEcho2.Call(h, 0, 0, 0, uintptr(dst),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(ms))
	if n == 0 {
		var errno uint32
		if e, ok := callErr.(syscall.Errno); ok {
			errno = uint32(e)
		}
		return classifyEchoFailure(errno)
	}
	return DecodeReply(reply), nil
}
