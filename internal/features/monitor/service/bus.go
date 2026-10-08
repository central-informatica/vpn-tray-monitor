package service

import (
	"sync"

	"github.com/guibsu/vpn-tray-monitor/internal/core/ipc"
)

// bus distribui eventos aos assinantes do pipe. Assinante cuja fila enche
// é removido e tem o canal fechado (o servidor IPC então o desconecta).
type bus struct {
	mu   sync.Mutex
	subs map[int]chan ipc.Message
	next int
	size int
}

func newBus(size int) *bus { return &bus{subs: map[int]chan ipc.Message{}, size: size} }

func (b *bus) subscribe(first ipc.Message) (<-chan ipc.Message, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan ipc.Message, b.size)
	ch <- first
	id := b.next
	b.next++
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

func (b *bus) publish(m ipc.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- m:
		default:
			delete(b.subs, id)
			close(ch)
		}
	}
}

func (b *bus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
