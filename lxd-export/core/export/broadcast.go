package export

import (
	"fmt"
	"sync"
)

type Message struct {
	SenderID   string
	ReceiverID string
	Content    string
}

type Broadcaster struct {
	mu       sync.RWMutex
	channels map[string]chan Message
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		channels: make(map[string]chan Message),
	}
}

func (b *Broadcaster) Register(workerID string) chan Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Message, 50) // To prevent blocking
	b.channels[workerID] = ch
	return ch
}

func (b *Broadcaster) Unregister(workerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, exists := b.channels[workerID]
	if exists {
		close(ch)
		delete(b.channels, workerID)
	}
}

func (b *Broadcaster) Broadcast(msg Message) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for id, ch := range b.channels {
		if id != msg.SenderID {
			select {
			case ch <- msg:
			default:
				fmt.Printf("Warning: Channel for worker %q is full, skipping broadcast message\n", id)
			}
		}
	}
}
