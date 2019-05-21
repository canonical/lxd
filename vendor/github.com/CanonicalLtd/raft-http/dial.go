// Copyright 2017 Canonical Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rafthttp

import (
	"crypto/tls"
	"net"
	"time"
)

// Dial is a function that given an address and a timeout returns a
// new network connection (typically TCP or TLS over TCP).
type Dial func(addr string, timeout time.Duration) (net.Conn, error)

// NewDialTCP returns a Dial function that establishes a network
// connection using raw TCP.
func NewDialTCP() Dial {
	dial := func(addr string, timeout time.Duration) (net.Conn, error) {
		dialer := newDialerWithTimeout(timeout)
		return dialer.Dial("tcp", addr)
	}
	return dial
}

// NewDialTLS returns a Dial function that enstablishes a network
// connection using TLS over TCP.
func NewDialTLS(config *tls.Config) Dial {
	dial := func(addr string, timeout time.Duration) (net.Conn, error) {
		dialer := newDialerWithTimeout(timeout)
		return tls.DialWithDialer(dialer, "tcp", addr, config)
	}
	return dial
}

// Convenience to create a Dialer configured with the give timeout.
func newDialerWithTimeout(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout}
}
