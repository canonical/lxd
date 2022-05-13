package events

import (
	"context"
	"net"
)

// EventListenerConnection represents an event listener connection.
type EventListenerConnection interface {
	Reader(ctx context.Context, recvFunc EventHandler)
	WriteJSON(event any) error
	Close() error
	LocalAddr() net.Addr  // Used for logging
	RemoteAddr() net.Addr // Used for logging
}
