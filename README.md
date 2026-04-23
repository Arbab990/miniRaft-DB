# ![MiniRaft DB](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/zap.svg) MiniRaft DB

**A fault-tolerant, strongly consistent distributed Key-Value database built from scratch in Go.**

![Go](https://img.shields.io/badge/LANGUAGE-GO_1.23+-00ADD8?style=flat-square&logo=go&logoColor=white) ![Consensus](https://img.shields.io/badge/CONSENSUS-RAFT-0052CC?style=flat-square) ![Dependencies](https://img.shields.io/badge/DEPENDENCIES-ZERO-238636?style=flat-square) ![Storage](https://img.shields.io/badge/STORAGE-KEY_VALUE-FF9900?style=flat-square)

MiniRaft DB is not a wrapper around an existing consensus library. Every layer — the Raft consensus engine, the Write-Ahead Log, the TCP transport, the snapshot system, and the HTTP API — is hand-written in Go, without any external frameworks. It is the foundation for a suite of distributed systems products built on top of it.

---

### What This Is

Most developers use distributed databases without understanding what makes them fault-tolerant. MiniRaft DB was built to answer that question in code. It implements the full Raft consensus protocol as described in the original paper by Diego Ongaro and John Ousterhout, and exposes it as a usable Key-Value store you can query with any HTTP client.

When you run three instances of this binary on your machine, you get a cluster that:

* ![Check](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/check-circle.svg) Elects a leader automatically and re-elects when the leader dies
* ![Check](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/check-circle.svg) Guarantees a write is not confirmed until a majority of nodes have persisted it
* ![Check](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/check-circle.svg) Survives individual node crashes and replays its log on restart
* ![Check](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/check-circle.svg) Compacts its log automatically so disk usage stays bounded
* ![Check](https://cdn.jsdelivr.net/gh/lucide-icons/lucide@main/icons/check-circle.svg) Serves reads that are guaranteed to reflect the latest committed state

This is the same set of guarantees that etcd provides to Kubernetes, and that CockroachDB's replication layer provides to its storage engine.

---

### Architecture

The system is structured in four layers, each with a clear responsibility.

```text
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