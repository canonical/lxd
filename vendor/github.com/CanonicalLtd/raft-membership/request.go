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

package raftmembership

import (
	"fmt"
	"time"

	"github.com/hashicorp/raft"
)

// ChangeRequest represents a request to change a server's membership in
// a raft cluster (either join or leave).
//
// A requesting server uses an implementation of the membership Changer
// interface to connect to a target server through some network transport layer
// and to ask to join or leave the target server's cluster. The target server
// internally uses ChangeRequest and HandleChangeRequests as helpers to
// implement handlers to process such requests coming from the network
// transport layer.
type ChangeRequest struct {
	id      raft.ServerID      // ID of the server requesting the membership change
	address raft.ServerAddress // Address of the server requesting the membership change
	kind    ChangeRequestKind  // Kind of membership change being requested
	done    chan error         // Notify client code of request success or failure
}

// NewJoinRequest creates a new membership ChangeRequest to join a
// cluster.
func NewJoinRequest(id raft.ServerID, address raft.ServerAddress) *ChangeRequest {
	return &ChangeRequest{
		id:      id,
		address: address,
		kind:    JoinRequest,
		done:    make(chan error, 1),
	}
}

// NewLeaveRequest creates a new membership ChangeRequest to leave a
// cluster.
func NewLeaveRequest(id raft.ServerID) *ChangeRequest {
	return &ChangeRequest{
		id:   id,
		kind: LeaveRequest,
		done: make(chan error, 1),
	}
}

// ID of the server requesting to change its membership.
func (r *ChangeRequest) ID() raft.ServerID {
	return r.id
}

// Address of the server requesting to change its membership.
func (r *ChangeRequest) Address() raft.ServerAddress {
	return r.address
}

// Kind is the type of membership change requested, either join leave.
func (r *ChangeRequest) Kind() ChangeRequestKind {
	return r.kind
}

// Error blocks until this ChangeRequest is fully processed or the given
// timeout is reached and returns any error hit while handling the request, or
// nil if none was met.
func (r *ChangeRequest) Error(timeout time.Duration) error {
	var err error
	select {
	case err = <-r.done:
	case <-time.After(timeout):
		err = fmt.Errorf("timeout waiting for membership change")
	}
	return err
}

// Done should be invoked by the code handling this request (such as
// HandleChangeRequests) to notify callers that the it has been
// processed, either successfully or not.
func (r *ChangeRequest) Done(err error) {
	r.done <- err
	close(r.done)
}

// ChangeRequestKind is kind of membership change being requested.
type ChangeRequestKind int

func (k ChangeRequestKind) String() string {
	return changeRequestKindToString[k]
}

// Possible values for ChangeRequestKind
const (
	JoinRequest ChangeRequestKind = iota
	LeaveRequest
)

var changeRequestKindToString = []string{
	JoinRequest:  "join",
	LeaveRequest: "leave",
}
