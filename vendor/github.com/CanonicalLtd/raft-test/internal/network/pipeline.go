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

package network

import (
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// Wrap a regular raft.AppendPipeline, adding support for triggering events at
// specific times.
type eventPipeline struct {
	logger hclog.Logger

	// Server ID sending RPCs.
	source raft.ServerID

	// Server ID this pipeline is sending RPCs to.
	target raft.ServerID

	// Regular pipeline that we are wrapping.
	pipeline raft.AppendPipeline

	// All other peers connected to our transport. Syncing logs after a
	// disconnection.
	peers *peers

	// Fault that should happen in this transport during a term.
	schedule *schedule

	// If non-zero, the pipeline will artificially return an error to its
	// consumer when firing the response of a request whose entries contain
	// a log with this index. This happens after the peer as actually
	// appended the request's entries and it effectively simulates a
	// follower disconnecting before it can acknowledge the leader of a
	// successful request.
	failure uint64

	// To stop the pipeline.
	shutdownCh chan struct{}
}

// AppendEntries is used to add another request to the pipeline.
// The send may block which is an effective form of back-pressure.
func (p *eventPipeline) AppendEntries(
	args *raft.AppendEntriesRequest, resp *raft.AppendEntriesResponse) (raft.AppendFuture, error) {

	p.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: pipeline: append to %s: %s", p.source, p.target, stringifyLogs(args.Entries)))

	peer := p.peers.Get(p.target)
	faulty := false
	if p.schedule != nil {
		n := peer.CommandLogsCount()
		args, faulty = p.schedule.FilterRequest(n, args)
		if faulty && p.schedule.IsEnqueueFault() {
			p.logger.Debug(fmt.Sprintf(
				"[DEBUG] raft-test: server %s: pipeline: append to: %s: enqueue fault: command %d", p.source, p.target, p.schedule.Command()))
		}
	}

	if p.peers.DisconnectedAndNotSyncing(p.target) {
		p.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: pipeline: append to %s: not connected", p.source, p.target))
		return nil, fmt.Errorf("cannot reach server %s", p.target)
	}

	if faulty && p.schedule.IsAppendFault() {
		p.logger.Debug(fmt.Sprintf("[DEBUG] raft-test: server %s: pipeline: append to %s: append fault: command %d", p.source, p.target, p.schedule.n))
		p.failure = args.Entries[0].Index
	}

	future, err := p.pipeline.AppendEntries(args, resp)
	if err != nil {
		return nil, err
	}
	peer.UpdateLogs(args.Entries)

	if faulty && p.schedule.IsEnqueueFault() {
		p.schedule.OccurredOn(p.target)
		p.schedule.event.Block()
		return nil, fmt.Errorf("cannot reach server %s", p.target)
	}

	return &appendFutureWrapper{
		id:     p.target,
		future: future,
	}, nil
}

// Consumer returns a channel that can be used to consume
// response futures when they are ready.
func (p *eventPipeline) Consumer() <-chan raft.AppendFuture {
	ch := make(chan raft.AppendFuture)

	go func() {
		for {
			select {
			case future := <-p.pipeline.Consumer():
				entries := future.Request().Entries
				fail := false
				if len(entries) > 0 && entries[0].Index == p.failure {
					fail = true
				}
				if fail {
					p.schedule.OccurredOn(p.target)
					p.schedule.event.Block()
					future = &appendFutureWrapper{id: p.target, future: future, failing: true}
				}
				ch <- future
			case <-p.shutdownCh:
				return
			}
		}
	}()
	return ch

}

// Close closes the pipeline and cancels all inflight RPCs
func (p *eventPipeline) Close() error {
	err := p.pipeline.Close()
	close(p.shutdownCh)
	return err
}

type appendFutureWrapper struct {
	id      raft.ServerID
	future  raft.AppendFuture
	failing bool
}

func (f *appendFutureWrapper) Error() error {
	if f.failing {
		return fmt.Errorf("cannot reach server %s", f.id)
	}
	return f.future.Error()
}

func (f *appendFutureWrapper) Start() time.Time {
	return f.future.Start()
}

func (f *appendFutureWrapper) Request() *raft.AppendEntriesRequest {
	return f.future.Request()
}
func (f *appendFutureWrapper) Response() *raft.AppendEntriesResponse {
	response := f.future.Response()
	if f.failing {
		response.Success = false
	}
	return response
}
