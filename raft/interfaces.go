package raft

// LogEntry represents a single command in the Raft log.
type LogEntry struct {
	Term    uint64
	Index   uint64
	Command []byte
}

// LogStore is the contract for our Write-Ahead Log.
type LogStore interface {
	FirstIndex() (uint64, error)
	LastIndex() (uint64, error)
	GetLog(index uint64) (*LogEntry, error)
	StoreLogs(logs []*LogEntry) error
	TruncateBefore(index uint64) error // NEW: discard logs before index (post-snapshot)
}

// StateMachine is the actual Key-Value database.
type StateMachine interface {
	Apply(command []byte) (interface{}, error)
	Snapshot() map[string]string    // NEW: capture full state
	Restore(data map[string]string) // NEW: restore from snapshot
}

// Transport sends RPCs to peers.
type Transport interface {
	SendRequestVote(peerId string, args RequestVoteArgs) (*RequestVoteReply, error)
	SendAppendEntries(peerId string, args AppendEntriesArgs) (*AppendEntriesReply, error)
	SendInstallSnapshot(peerId string, args InstallSnapshotArgs) (*InstallSnapshotReply, error)
}
