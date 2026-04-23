package main

import (
	"fmt"
	"testing"
	"time"

	"miniraft/raft"
	"miniraft/store"
)

// inMemTransport lets test nodes communicate via direct Go calls — no TCP needed.
type inMemTransport struct {
	nodes map[string]*raft.Server
}

func newInMemTransport() *inMemTransport {
	return &inMemTransport{nodes: make(map[string]*raft.Server)}
}

func (t *inMemTransport) register(id string, node *raft.Server) {
	t.nodes[id] = node
}

func (t *inMemTransport) SendRequestVote(peerId string, args raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	peer, ok := t.nodes[peerId]
	if !ok {
		return nil, fmt.Errorf("peer %s unreachable", peerId)
	}
	var reply raft.RequestVoteReply
	return &reply, peer.RequestVote(args, &reply)
}

func (t *inMemTransport) SendAppendEntries(peerId string, args raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	peer, ok := t.nodes[peerId]
	if !ok {
		return nil, fmt.Errorf("peer %s unreachable", peerId)
	}
	var reply raft.AppendEntriesReply
	return &reply, peer.AppendEntries(args, &reply)
}

func (t *inMemTransport) SendInstallSnapshot(peerId string, args raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	peer, ok := t.nodes[peerId]
	if !ok {
		return nil, fmt.Errorf("peer %s unreachable", peerId)
	}
	var reply raft.InstallSnapshotReply
	return &reply, peer.InstallSnapshot(args, &reply)
}

// partitionedTransport wraps inMemTransport and can isolate specific nodes.
type partitionedTransport struct {
	*inMemTransport
	isolated map[string]bool
}

func newPartitionedTransport() *partitionedTransport {
	return &partitionedTransport{
		inMemTransport: newInMemTransport(),
		isolated:       make(map[string]bool),
	}
}

func (t *partitionedTransport) isolate(id string) { t.isolated[id] = true }
func (t *partitionedTransport) heal(id string)    { delete(t.isolated, id) }

func (t *partitionedTransport) SendRequestVote(peerId string, args raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if t.isolated[peerId] || t.isolated[args.CandidateId] {
		return nil, fmt.Errorf("partitioned")
	}
	return t.inMemTransport.SendRequestVote(peerId, args)
}

func (t *partitionedTransport) SendAppendEntries(peerId string, args raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	if t.isolated[peerId] || t.isolated[args.LeaderId] {
		return nil, fmt.Errorf("partitioned")
	}
	return t.inMemTransport.SendAppendEntries(peerId, args)
}

func (t *partitionedTransport) SendInstallSnapshot(peerId string, args raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	if t.isolated[peerId] || t.isolated[args.LeaderId] {
		return nil, fmt.Errorf("partitioned")
	}
	return t.inMemTransport.SendInstallSnapshot(peerId, args)
}

// --- helpers ---

func makeCluster(t *testing.T) ([]*raft.Server, *partitionedTransport) {
	t.Helper()
	transport := newPartitionedTransport()
	ids := []string{"n1", "n2", "n3"}
	nodes := make([]*raft.Server, 3)

	for i, id := range ids {
		peers := []string{}
		for _, p := range ids {
			if p != id {
				peers = append(peers, p)
			}
		}
		nodes[i] = raft.NewServer(id, peers,
			store.NewMemoryLogStore(),
			store.NewMemoryKVStore(),
			"", transport)
		transport.register(id, nodes[i])
	}
	return nodes, transport
}

func waitForLeader(t *testing.T, nodes []*raft.Server, timeout time.Duration) *raft.Server {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected within timeout")
	return nil
}

// --- tests ---

// TestLeaderElected verifies a leader is elected in a healthy cluster.
func TestLeaderElected(t *testing.T) {
	nodes, _ := makeCluster(t)
	leader := waitForLeader(t, nodes, 3*time.Second)
	if leader == nil {
		t.Fatal("expected a leader")
	}
	t.Logf("Leader elected: %s", leader.Status().ID)
}

// TestDataReplication verifies a write is applied on all nodes.
func TestDataReplication(t *testing.T) {
	nodes, _ := makeCluster(t)
	leader := waitForLeader(t, nodes, 3*time.Second)

	ok := leader.Submit([]byte("SET name arbab"))
	if !ok {
		t.Fatal("submit rejected by leader")
	}

	// Wait for replication
	time.Sleep(300 * time.Millisecond)

	for _, n := range nodes {
		s := n.Status()
		if s.CommitIndex < 1 {
			t.Errorf("node %s commit_index=%d, want >=1", s.ID, s.CommitIndex)
		}
		if s.LastApplied < 1 {
			t.Errorf("node %s last_applied=%d, want >=1", s.ID, s.LastApplied)
		}
	}
}

// TestLeaderFailover verifies a new leader is elected after the current one is isolated.
func TestLeaderFailover(t *testing.T) {
	nodes, transport := makeCluster(t)
	leader := waitForLeader(t, nodes, 3*time.Second)
	oldLeaderID := leader.Status().ID
	t.Logf("Old leader: %s", oldLeaderID)

	// Write before failure
	leader.Submit([]byte("SET before crash"))
	time.Sleep(200 * time.Millisecond)

	// Isolate the leader — simulates kill
	// (we stop it from receiving further RPCs by removing from transport)
	// New leader must emerge from remaining two nodes
	transport.isolate(oldLeaderID)

	var remaining []*raft.Server
	for _, n := range nodes {
		if n.Status().ID != oldLeaderID {
			remaining = append(remaining, n)
		}
	}

	newLeader := waitForLeader(t, remaining, 5*time.Second)
	if newLeader == nil {
		t.Fatal("no new leader after old leader failure")
	}
	if newLeader.Status().ID == oldLeaderID {
		t.Fatal("old leader should not still be leader")
	}
	t.Logf("New leader elected: %s at term %d", newLeader.Status().ID, newLeader.Status().Term)
}

// TestWriteAfterFailover verifies writes succeed after a leader change.
func TestWriteAfterFailover(t *testing.T) {
	nodes, transport := makeCluster(t)
	leader := waitForLeader(t, nodes, 3*time.Second)

	// Isolate leader from transport
	transport.isolate(leader.Status().ID)

	var remaining []*raft.Server
	for _, n := range nodes {
		if n.Status().ID != leader.Status().ID {
			remaining = append(remaining, n)
		}
	}

	newLeader := waitForLeader(t, remaining, 5*time.Second)
	ok := newLeader.Submit([]byte("SET after failover value"))
	if !ok {
		t.Fatal("write rejected after failover")
	}

	time.Sleep(300 * time.Millisecond)

	s := newLeader.Status()
	if s.CommitIndex < 1 {
		t.Errorf("expected commit after failover, got commit_index=%d", s.CommitIndex)
	}
	t.Logf("Write succeeded on new leader %s at term %d", s.ID, s.Term)
}
