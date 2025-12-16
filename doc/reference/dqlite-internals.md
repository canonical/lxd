---
relatedlinks: "[Canonical&#32;Dqlite](https://canonical.com/dqlite), [Dqlite&#32;GitHub](https://github.com/canonical/dqlite)"
---

(dqlite-internals)=
# Dqlite internals

Dqlite (distributed SQLite) implements a replicated SQLite database by combining the SQLite engine with a Raft-based consensus layer. Each LXD daemon (cluster member) runs a Dqlite node which exposes a SQLite-like API backed by a Raft replicated state machine. A single leader handles writes; followers apply replicated log entries and serve reads depending on configuration.

## Raft

[Raft](https://raft.github.io/) is a consensus algorithm that ensures a cluster of nodes can agree on a sequence of state machine commands even in the presence of failures. Raft handles leader election, log replication, safety, and membership changes.

## Dqlite Raft implementation

Raft nodes in Dqlite move between four runtime states: `RAFT_UNAVAILABLE`, `RAFT_FOLLOWER`, `RAFT_CANDIDATE` and `RAFT_LEADER`. Followers are passive: they accept `AppendEntries` RPCs (remote procedure calls) from an active leader and reset an election timer; when a follower's randomized election timeout elapses without leader contact it becomes a candidate, increments its term and sends `RequestVote` RPCs to gather votes. A candidate becomes leader after receiving votes from a majority of voting servers and then starts replicating log entries to followers using `AppendEntries` (heartbeats are empty `AppendEntries` used to maintain authority).

The election timeout is randomized, but always shorter than the heartbeat interval. Features such as pre-vote and explicit leadership transfer ensure reliable handover even if all servers cannot vote. Leaders also step down if they lose contact with a majority of voters. The {ref}`dqlite-internals-raft-roles` control whether a server participates in quorum and elections.

For more information on the Canonical Dqlite Raft implementation, see [`dqlite/src/raft.h`](https://github.com/canonical/dqlite/blob/main/src/raft.h) and [Dqlite replication](https://canonical.com/dqlite/docs/explanation/replication).

(dqlite-internals-raft-roles)=
### Dqlite raft roles

1. `RAFT_VOTER`: Replicates the log and participates in quorum/elections.
1. `RAFT_STANDBY`: Replicates the log but does not participate in quorum/elections.
1. `RAFT_SPARE`: Does not replicate the log and does not participate in quorum/elections.

(dqlite-internals-lxd-cluster-roles)=
### LXD cluster roles

LXD assigns database roles to cluster members based on their Dqlite Raft role:

1. `database-voter`: Assigned to cluster members with the `RAFT_VOTER` role (excluding the leader).
1. `database-standby`: Assigned to cluster members with the `RAFT_STANDBY` role.
1. `database-leader`: Assigned to the current Raft leader.

LXD also provides a `control-plane` role that restricts which members can be assigned Raft roles. When 3 or more members have the `control-plane` role assigned, members without it are assigned the `RAFT_SPARE` role and excluded from automatic promotion. The {config:option}`server-cluster:cluster.max_voters` and {config:option}`server-cluster:cluster.max_standby` settings determine how many control-plane members are promoted to `RAFT_VOTER` and `RAFT_STANDBY` roles. If control plane members exceed these limits, the extras remain as promotion candidates.
