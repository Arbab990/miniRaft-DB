# ⚡ MiniRaft DB

> **A fault-tolerant, strongly consistent distributed Key-Value database built from scratch in Go.**

![Go](https://img.shields.io/badge/LANGUAGE-GO_1.23+-00ADD8?style=flat-square&logo=go&logoColor=white) ![Consensus](https://img.shields.io/badge/CONSENSUS-RAFT-0052CC?style=flat-square&logo=buffer&logoColor=white) ![Dependencies](https://img.shields.io/badge/DEPENDENCIES-ZERO-238636?style=flat-square&logo=checkmarx&logoColor=white) ![Storage](https://img.shields.io/badge/STORAGE-KEY_VALUE-FF9900?style=flat-square&logo=amazondynamodb&logoColor=white) ![License](https://img.shields.io/badge/LICENSE-MIT-8A2BE2?style=flat-square&logo=opensourceinitiative&logoColor=white) ![Build](https://img.shields.io/badge/BUILD-PASSING-2ea44f?style=flat-square&logo=githubactions&logoColor=white)

**MiniRaft DB** is not a wrapper around an existing consensus library. Every layer — the Raft consensus engine, the Write-Ahead Log, the TCP transport, the snapshot system, and the HTTP API — is hand-written in Go, without any external frameworks. It is the foundation for a suite of distributed systems products built on top of it.

---

## ❖ What This Is

Most developers use distributed databases without understanding what makes them fault-tolerant. **MiniRaft DB** was built to answer that question in code. It implements the full Raft consensus protocol as described in the original paper by Diego Ongaro and John Ousterhout, and exposes it as a usable Key-Value store you can query with any HTTP client.

When you run three instances of this binary on your machine, you get a cluster that guarantees:

- **Automatic Elections:** Elects a leader automatically and re-elects when the leader dies.
- **Strict Consistency:** Guarantees a write is not confirmed until a majority of nodes have persisted it.
- **Crash Recovery:** Survives individual node crashes and replays its log on restart.
- **Storage Efficiency:** Compacts its log automatically so disk usage stays bounded.
- **Data Accuracy:** Serves reads that are guaranteed to reflect the latest committed state.

*This is the exact same set of guarantees that `etcd` provides to Kubernetes, and that `CockroachDB`'s replication layer provides to its storage engine.*

---

## ❖ Architecture

The system is structured in four distinct layers, each with a clear responsibility:

```
+--------------------------------------------------+
|                  HTTP API Layer                  |
|        GET /get   POST /set   GET /status        |
+--------------------------------------------------+
|               Raft Consensus Engine              |
|   Leader Election · Log Replication · Commits    |
+--------------------------------------------------+
|                  Transport Layer                 |
|     Raw TCP · Custom Binary Framing · Gob RPC    |
+--------------------------------------------------+
|                  Storage Layer                   |
|       Write-Ahead Log · Snapshots · KV Store     |
+--------------------------------------------------+
```

### Raft Consensus Engine (`raft/`)

The brain of the system. It runs three concurrent processes on every node:

- **Election Timer** — each node sleeps for a randomized duration between 150ms and 300ms. If it does not receive a heartbeat from a leader within that window, it promotes itself to Candidate and starts an election.
- **Heartbeat Loop** — the Leader sends `AppendEntries` RPCs to all peers every 50ms. These serve as both a liveness signal and the vehicle for log replication.
- **Apply Loop** — once a log entry is confirmed replicated on a majority of nodes, it is applied to the state machine and the commit index advances.

The implementation enforces all five of the Raft safety properties from the paper:

| Property | How It Is Enforced |
|---|---|
| Election Safety | At most one leader per term, enforced by majority voting |
| Leader Append-Only | Leaders never overwrite their log |
| Log Matching | Followers verify `PrevLogIndex` and `PrevLogTerm` before accepting entries |
| Leader Completeness | Candidates must have a log at least as up-to-date as the majority |
| State Machine Safety | A log entry is only applied after being committed on a majority |

### Write-Ahead Log (`store/wal.go`)

Every log entry is persisted to disk before being acknowledged. The WAL format is:

```
[ 4 bytes CRC32 ][ 4 bytes length ][ N bytes gob-encoded LogEntry ]
```

On startup, the WAL is replayed from disk. If the process crashed mid-write, the corrupted tail is detected via CRC mismatch and truncated cleanly. This means the node always recovers to a consistent state regardless of when it was killed.

### Snapshot and Log Compaction (`store/snapshot.go`, `raft/raft.go`)

The WAL would grow unbounded without compaction. Every 100 committed entries, the node:

1. Serializes the full state machine to a `.snap` file using an atomic write (temp file + rename)
2. Truncates the WAL to remove all entries covered by the snapshot
3. On the next restart, loads the snapshot first, then replays only the remaining WAL entries

When a lagging follower is too far behind for normal log replication, the leader sends it an `InstallSnapshot` RPC containing the full state, which the follower applies directly.

### TCP Transport (`rpc/`)

Nodes communicate over raw TCP with a custom binary framing protocol. There is no dependency on `net/rpc` or gRPC. Each message is framed as:

```
[ 1 byte message type ][ 4 bytes payload length ][ N bytes gob-encoded payload ]
```

Six message types are defined: `RequestVote`, `RequestVoteReply`, `AppendEntries`, `AppendEntriesReply`, `InstallSnapshot`, `InstallSnapshotReply`.

The `Transport` interface in `raft/interfaces.go` means the Raft engine has no knowledge of TCP. In the test suite, an in-memory transport is injected instead, allowing deterministic testing of consensus logic without any network.

### HTTP API (`server/http.go`)

Each node exposes three endpoints:

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/set?key=K&value=V` | Write a key-value pair. Returns `leader_hint` if this node is not the leader. |
| `GET` | `/get?key=K` | Read a value. Returns `linearizable: true` if the read was confirmed by quorum. |
| `GET` | `/status` | Returns node state, term, commit index, applied index, and snapshot index. |

---

## Linearizable Reads

A naive read implementation would return whatever value the local state machine holds. On a follower that is a few heartbeats behind, this means stale data. MiniRaft DB implements linearizable reads using the `ReadIndex` protocol:

Before serving a read, the leader sends a round of heartbeats to all peers and waits for a majority to acknowledge. This confirms the leader has not been deposed without its knowledge. Only after quorum confirmation does it serve the read from its local state machine.

Reads from followers are still served but are flagged with `"linearizable": false` in the response so the client knows the data may be slightly behind.

---

## Project Structure

```
miniraft/
├── raft/
│   ├── interfaces.go       # LogStore, StateMachine, Transport contracts
│   ├── raft.go             # Server struct, election, heartbeat, apply loop
│   ├── rpc.go              # RequestVote, AppendEntries, InstallSnapshot handlers
│   └── snapshot.go         # Snapshot struct, SaveSnapshot, LoadSnapshot
├── store/
│   ├── memory.go           # In-memory LogStore and KVStore (used in tests)
│   ├── wal.go              # Disk-backed WAL with CRC checksums
│   └── snapshot.go         # Snapshot serialization to disk
├── rpc/
│   ├── codec.go            # Binary message framing
│   ├── transport.go        # TCP listener and RPC dispatcher
│   ├── client.go           # Outbound TCP connections
│   └── raft_transport.go   # raft.Transport implementation over TCP
├── server/
│   └── http.go             # HTTP API server
├── chaos_test.go           # Fault injection test suite
└── main.go                 # Entry point, node bootstrap
```

---

## Running the Cluster

**Requirements:** Go 1.23 or later. No external dependencies.

```bash
git clone https://github.com/your-username/miniraft
cd miniraft
go build ./...
```

Open three terminal windows and start one node per terminal:

```bash
# Terminal 1
go run . -id=node1

# Terminal 2
go run . -id=node2

# Terminal 3
go run . -id=node3
```

The cluster will elect a leader within 300ms of all three nodes being online.

### Writing Data

Direct the write to whichever node is currently the leader. If you hit a follower, it will return a `leader_hint` telling you the correct address.

```bash
curl -X POST "http://localhost:8081/set?key=name&value=arbab"
```

Response:
```json
{ "key": "name", "status": "accepted", "value": "arbab" }
```

### Reading Data

```bash
curl "http://localhost:8082/get?key=name"
```

Response from leader (linearizable):
```json
{ "key": "name", "value": "arbab", "linearizable": true }
```

Response from follower (local read):
```json
{ "key": "name", "value": "arbab", "linearizable": false }
```

### Cluster Status

```bash
curl "http://localhost:8083/status"
```

```json
{
  "id": "node3",
  "state": "Follower",
  "term": 4,
  "commit_index": 12,
  "last_applied": 12,
  "snapshot_index": 0,
  "leader_hint": ":8081"
}
```

---

## Fault Tolerance

### Leader Failure

Kill the leader terminal with `Ctrl+C`. Within one election timeout (150ms to 300ms), the two remaining nodes will detect the missing heartbeat, one will start an election, win a majority vote, and begin serving writes. The cluster self-heals with no human intervention.

### Node Restart

Restart a killed node. It will:

1. Open its WAL file and replay all persisted log entries
2. If a snapshot exists, restore the state machine from it first
3. Replay only the WAL entries after the snapshot index
4. Rejoin the cluster as a follower and receive any missed entries from the leader

### Network Partition

If a minority partition (one node) loses contact with the other two, it will keep incrementing its term as it repeatedly fails to win elections. When the partition heals, the leader will detect the node's stale term, and the node will step down and synchronize its log. No data committed by the majority is ever lost.

---

## Test Suite

```bash
go test -v ./...
```

The `chaos_test.go` file contains four deterministic fault injection tests. They use an in-memory transport that can isolate specific nodes without any network, making tests fast and reproducible.

| Test | What It Verifies |
|---|---|
| `TestLeaderElected` | A leader is elected within 3 seconds in a healthy cluster |
| `TestDataReplication` | A write is applied on all three nodes after commit |
| `TestLeaderFailover` | A new leader is elected after the current leader is isolated |
| `TestWriteAfterFailover` | Writes succeed on a new leader after a leadership change |

---

## What This Is Not

MiniRaft DB does not implement membership changes (adding or removing nodes from a live cluster), multi-key transactions, or a query language. It is intentionally minimal. The goal is a correct, readable implementation of the Raft consensus protocol that serves as a foundation for higher-level systems.

---

## What Gets Built On Top

MiniRaft DB is the foundation layer for a suite of distributed systems products. Each product is a separate repository that clones this engine and adds a focused application layer on top of it.

| Product | What It Adds | Inspired By | Redirect URL |
|---|---|---|---|
| **MiniLock** | Secrets and configuration store with namespacing, TTLs, and watch/notify | HashiCorp Vault, etcd | — |
| **MiniWatch** | Feature flag and rollout engine with consistent flag evaluation across all nodes | LaunchDarkly, Unleash | — |
| **MiniFlow** | Distributed task queue with exactly-once execution and worker registry | Celery, SQS, Temporal | [![MiniFlow](https://img.shields.io/badge/View_Repo-MiniFlow-00ADD8?style=flat-square&logo=github&logoColor=white)](https://github.com/Arbab990/MiniFlow) |

Each product inherits the fault tolerance, WAL persistence, snapshot compaction, and linearizable reads from this engine without reimplementing them.

---

## Key Design Decisions

**Why raw TCP instead of gRPC or net/rpc?**
`net/rpc` is frozen and hides the transport in ways that make debugging distributed failures harder. gRPC adds significant dependency weight. Raw TCP with custom framing means the entire communication path is visible in the codebase and debuggable with standard tools.

**Why gob encoding instead of protobuf?**
Protobuf requires a code generation step and an external dependency. Gob is part of the Go standard library, keeps the project dependency-free, and is sufficient for a system where all nodes are Go binaries.

**Why per-RPC connections instead of a connection pool?**
A persistent connection pool adds complexity around reconnection logic, health checking, and backpressure. At the heartbeat frequency of 50ms with three peers, the overhead of dialing per RPC is negligible on localhost and acceptable over a LAN.

**Why snapshot every 100 entries instead of a configurable threshold?**
Hardcoded for simplicity. In production this would be a configuration parameter. The implementation is correct regardless of the threshold value.

---

## References

- Ongaro, D. and Ousterhout, J. (2014). [In Search of an Understandable Consensus Algorithm](https://raft.github.io/raft.pdf)
- The Raft website and visualization: [raft.github.io](https://raft.github.io)
- etcd's Raft implementation (reference for production patterns): [github.com/etcd-io/etcd](https://github.com/etcd-io/etcd)
