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
	"strings"

	"github.com/hashicorp/raft"
)

// Return a string representation of the given log entries.
func stringifyLogs(logs []*raft.Log) string {
	n := len(logs)
	description := fmt.Sprintf("%d ", n)
	if n == 1 {
		description += "entry"
	} else {
		description += "entries"
	}

	if n > 0 {
		entries := make([]string, n)
		for i, log := range logs {
			name := "Other"
			switch log.Type {
			case raft.LogCommand:
				name = "Command"
			case raft.LogNoop:
				name = "Noop"
			}
			entries[i] = fmt.Sprintf("%s:term=%d,index=%d", name, log.Term, log.Index)
		}
		description += fmt.Sprintf(" [%s]", strings.Join(entries, " "))
	}

	return description
}

// This function takes a set of log entries that have been successfully
// appended to a peer and filters out any log entry with an older term relative
// to the others.
//
// The returned entries are guaranted to have the same term and that term is
// the highest among the ones in this batch.
func filterLogsWithOlderTerms(logs []*raft.Log) []*raft.Log {
	// Find the highest term.
	var highestTerm uint64
	for _, log := range logs {
		if log.Term > highestTerm {
			highestTerm = log.Term
		}
	}

	// Discard any log with an older term than the highest one.
	filteredLogs := make([]*raft.Log, 0)
	for _, log := range logs {
		if log.Term == highestTerm {
			filteredLogs = append(filteredLogs, log)
		}
	}

	return filteredLogs
}
