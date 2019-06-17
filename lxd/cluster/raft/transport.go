package raft

// Transport provides an interface for network transports
// to allow Raft to communicate with other nodes.
type Transport interface {
	// EncodePeer is used to serialize a peer's address.
	EncodePeer(id ServerID, addr ServerAddress) []byte
}
