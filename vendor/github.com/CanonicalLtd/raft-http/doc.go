// Package rafthttp provides an extension for the github.com/hashicorp/raft
// package.
//
// It implements a raft.StreamLayer that a raft.NetworkTransport can use to
// connect to and accept connections from other raft.Transport's using
// HTTP/WebSocket rather than straight TCP.
//
// This is handy for applications that expose an HTTP endpoint and don't want
// to open an extra TCP port for handling raft-level traffic.
//
// In addition to the regular raft.StreamLayer interface, rafthttp.Layer
// implements extra methods to join and leave a cluster.
//
// Typical usage of this package is as follows:
//
// - Create a rafthttp.Handler object which implements the standard
//   http.Handler interface.
//
// - Create a standard http.Server and configure it to route an endpoint path
//   of your choice to the rafthttp.Handler above. All your raft servers must
//   use the same endpoint path. You'll probably want to gate the
//   rafthttp.Handler behind some authorization mechanism of your choice.
//
// - Create a net.Listener and use it to start a the http.Server create
//   above. From this point the rafthttp.Handler will start accepting
//   raft-related requests.
//
// - Create a rafthttp.Layer object passing it:
//
//   1) The endpoint path you chose above, which will be used to establish
//      outbound raft.Transport connections to other raft servers over
//      HTTP/WebSocket.
//
//   2) The network address of the net.Listener you used to start the
//      http.Server, which will be used by the local raft server to know its
//      own network address.
//
//   3) The rafthttp.Handler object you created above, which will be used to
//      accept inbound raft.NetworkTransport connections from other raft
//      servers over HTTP/WebSocket.
//
//   4) A rafthttp.Dial function, which will be used to establish outbound
//      raft.NetworkTransport connections to other raft servers over
//      HTTP/WebSocket (the rafthttp.Layer will use it to perform HTTP requests
//      to other servers using your chosen endpoint path).
//
// - Create a raft.NetworkTransport passing it the rafthttp.Layer you created
//   above.
//
// - Create a raft.Raft server using the raft.NetworkTransport created above.
//
// - Spawn a goroutine running the raftmembership.HandleChangeRequests function
//   from the github.com/Canonical/raft-membership package, passing it the
//   raft.Raft server you created above and the channel returned by Request()
//   method of the rafthttp.Handler created above. This will process join and
//   leave requests, that you can perform using the Join() and Leave() methods
//   of the rafthttp.Layer object you created above. This goroutine will
//   terminate automatically when you shutdown your raft.Raft server, since
//   that will close your raft.NetworkTransport, which in turn closes the your
//   rafttest.Layer, which closes your rafttest.Handler, which will ultimately
//   close the channel returned by its Requests() method and signal the
//   raftmembership.HandleChangeRequests function to return.
//
// To cleanly shutdown the service, first shutdown your raft.Raft instance,
// then call the CloseStreams() method of your raft.NetworkTransport instance
// (to close all connections) and then stop your http.Server.
package rafthttp
