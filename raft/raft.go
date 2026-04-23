package raft

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

const snapshotThreshold = 100 // take snapshot every 100 committed entries

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

type Server struct {
	mu sync.Mutex

	id        string
	peerIds   []string
	transport Transport

	currentTerm uint64
	votedFor    string
	log         LogStore

	state        State
	stateMachine StateMachine
	leaderAddr   string
	httpAddr     string

	lastHeartbeat time.Time
	commitIndex   uint64
	lastApplied   uint64
	nextIndex     map[string]uint64
	matchIndex    map[string]uint64

	// Snapshot state
	snapshotIndex uint64 // last log index covered by latest snapshot
	snapshotTerm  uint64 // term of that index
	snapshotPath  string // where to persist snapshot on disk
}

func NewServer(id string, peerIds []string, logStore LogStore, sm StateMachine, httpAddr string, transport Transport) *Server {
	s := &Server{
		id:            id,
		peerIds:       peerIds,
		transport:     transport,
		state:         Follower,
		log:           logStore,
		stateMachine:  sm,
		httpAddr:      httpAddr,
		snapshotPath:  id + ".snap",
		lastHeartbeat: time.Now(),
	}

	// Load snapshot if one exists
	s.loadSnapshot()

	log.Printf("[Node %s] Initialized as %s for term %d (snapshotIndex:%d)",
		s.id, s.state, s.currentTerm, s.snapshotIndex)
	go s.runElectionTimer()
	return s
}

// loadSnapshot restores state from disk snapshot on startup.
func (s *Server) loadSnapshot() {
	snap, err := LoadSnapshot(s.snapshotPath)
	if err != nil {
		log.Printf("[Node %s] Snapshot load error: %v", s.id, err)
		return
	}
	if snap == nil {
		return // no snapshot yet, fresh start
	}
	s.stateMachine.Restore(snap.Data)
	s.snapshotIndex = snap.LastIndex
	s.snapshotTerm = snap.LastTerm
	s.commitIndex = snap.LastIndex
	s.lastApplied = snap.LastIndex
	log.Printf("[Node %s] Restored snapshot at index %d", s.id, snap.LastIndex)
}

// persistSnapshot saves the current state to disk.
func (s *Server) persistSnapshot(data map[string]string, lastIndex, lastTerm uint64) {
	snap := Snapshot{
		LastIndex: lastIndex,
		LastTerm:  lastTerm,
		Data:      data,
	}
	if err := SaveSnapshot(s.snapshotPath, snap); err != nil {
		log.Printf("[Node %s] Failed to persist snapshot: %v", s.id, err)
		return
	}
	log.Printf("[Node %s] Snapshot persisted at index %d", s.id, lastIndex)
}

// maybeSnapshot checks if we should take a snapshot and compact the log.
// Called after applying committed entries. Caller must NOT hold mu.
func (s *Server) maybeSnapshot() {
	s.mu.Lock()
	applied := s.lastApplied
	snapIdx := s.snapshotIndex
	if applied-snapIdx < snapshotThreshold {
		s.mu.Unlock()
		return
	}

	// Capture state
	lastIndex := applied
	lastTerm := uint64(0)
	if entry, err := s.log.GetLog(lastIndex); err == nil {
		lastTerm = entry.Term
	} else {
		lastTerm = s.snapshotTerm // already compacted
	}

	data := s.stateMachine.Snapshot()
	s.snapshotIndex = lastIndex
	s.snapshotTerm = lastTerm
	s.mu.Unlock()

	// Persist to disk (outside lock)
	s.persistSnapshot(data, lastIndex, lastTerm)

	// Truncate WAL
	s.mu.Lock()
	s.log.TruncateBefore(lastIndex + 1)
	s.mu.Unlock()

	log.Printf("[Node %s] Log compacted up to index %d", s.id, lastIndex)
}

func (s *Server) randomTimeout() time.Duration {
	return time.Duration(150+rand.Intn(150)) * time.Millisecond
}

func (s *Server) runElectionTimer() {
	for {
		timeout := s.randomTimeout()
		time.Sleep(timeout)

		s.mu.Lock()
		if s.state == Leader {
			s.mu.Unlock()
			continue
		}
		if time.Since(s.lastHeartbeat) >= timeout {
			s.startElection()
		}
		s.mu.Unlock()
	}
}

func (s *Server) startElection() {
	s.state = Candidate
	s.currentTerm++
	s.votedFor = s.id
	s.lastHeartbeat = time.Now()
	savedTerm := s.currentTerm

	lastIdx, _ := s.log.LastIndex()
	// If log was compacted, use snapshot index
	if lastIdx < s.snapshotIndex {
		lastIdx = s.snapshotIndex
	}
	lastTerm := s.snapshotTerm
	if entry, err := s.log.GetLog(lastIdx); err == nil {
		lastTerm = entry.Term
	}

	log.Printf("[Node %s] Election for Term %d (LastIdx:%d LastTerm:%d)",
		s.id, s.currentTerm, lastIdx, lastTerm)

	args := RequestVoteArgs{
		Term:         savedTerm,
		CandidateId:  s.id,
		LastLogIndex: lastIdx,
		LastLogTerm:  lastTerm,
	}

	votesReceived := 1

	for _, peerId := range s.peerIds {
		go func(peer string) {
			reply, err := s.transport.SendRequestVote(peer, args)
			if err != nil {
				return
			}

			s.mu.Lock()
			defer s.mu.Unlock()

			if s.state != Candidate || s.currentTerm != savedTerm {
				return
			}
			if reply.Term > s.currentTerm {
				s.state = Follower
				s.currentTerm = reply.Term
				s.votedFor = ""
				return
			}
			if reply.VoteGranted {
				votesReceived++
				if votesReceived > (len(s.peerIds)+1)/2 {
					s.becomeLeader()
				}
			}
		}(peerId)
	}
}

func (s *Server) becomeLeader() {
	s.state = Leader
	s.leaderAddr = s.httpAddr
	log.Printf("[Node %s] Leader for Term %d", s.id, s.currentTerm)

	lastLogIdx, _ := s.log.LastIndex()
	s.nextIndex = make(map[string]uint64)
	s.matchIndex = make(map[string]uint64)
	for _, p := range s.peerIds {
		s.nextIndex[p] = lastLogIdx + 1
		s.matchIndex[p] = 0
	}
	go s.runHeartbeats()
}

func (s *Server) runHeartbeats() {
	for {
		s.mu.Lock()
		if s.state != Leader {
			s.mu.Unlock()
			return
		}
		savedTerm := s.currentTerm
		s.mu.Unlock()

		for _, peerId := range s.peerIds {
			go func(peer string) {
				s.mu.Lock()

				nextIdx := s.nextIndex[peer]

				// If peer needs entries before our snapshot, send snapshot instead
				if nextIdx <= s.snapshotIndex {
					snapData := s.stateMachine.Snapshot()
					snapArgs := InstallSnapshotArgs{
						Term:              savedTerm,
						LeaderId:          s.id,
						LastIncludedIndex: s.snapshotIndex,
						LastIncludedTerm:  s.snapshotTerm,
						Data:              snapData,
					}
					s.mu.Unlock()

					reply, err := s.transport.SendInstallSnapshot(peer, snapArgs)
					if err != nil {
						return
					}
					s.mu.Lock()
					defer s.mu.Unlock()
					if reply.Term > s.currentTerm {
						s.state = Follower
						s.currentTerm = reply.Term
						s.votedFor = ""
						return
					}
					s.nextIndex[peer] = s.snapshotIndex + 1
					s.matchIndex[peer] = s.snapshotIndex
					return
				}

				prevLogIndex := nextIdx - 1
				var prevLogTerm uint64
				if prevLogIndex > s.snapshotIndex {
					if entry, err := s.log.GetLog(prevLogIndex); err == nil {
						prevLogTerm = entry.Term
					}
				} else if prevLogIndex == s.snapshotIndex {
					prevLogTerm = s.snapshotTerm
				}

				var entriesToSend []*LogEntry
				lastIdx, _ := s.log.LastIndex()
				if lastIdx >= nextIdx {
					if entry, err := s.log.GetLog(nextIdx); err == nil && entry != nil {
						entriesToSend = append(entriesToSend, entry)
					}
				}

				args := AppendEntriesArgs{
					Term:         savedTerm,
					LeaderId:     s.id,
					LeaderAddr:   s.httpAddr,
					PrevLogIndex: prevLogIndex,
					PrevLogTerm:  prevLogTerm,
					Entries:      entriesToSend,
					LeaderCommit: s.commitIndex,
				}
				s.mu.Unlock()

				reply, err := s.transport.SendAppendEntries(peer, args)
				if err != nil {
					return
				}

				s.mu.Lock()
				defer s.mu.Unlock()

				if reply.Term > s.currentTerm {
					s.state = Follower
					s.currentTerm = reply.Term
					s.votedFor = ""
					return
				}

				if reply.Success && len(entriesToSend) > 0 {
					lastSentIndex := entriesToSend[len(entriesToSend)-1].Index
					s.nextIndex[peer] = lastSentIndex + 1
					s.matchIndex[peer] = lastSentIndex
					s.advanceCommitIndex()
				} else if !reply.Success {
					if s.nextIndex[peer] > 1 {
						s.nextIndex[peer]--
					}
				}
			}(peerId)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Server) advanceCommitIndex() {
	lastIdx, _ := s.log.LastIndex()
	for n := lastIdx; n > s.commitIndex; n-- {
		entry, err := s.log.GetLog(n)
		if err != nil || entry.Term != s.currentTerm {
			continue
		}
		replicatedOn := 1
		for _, mIdx := range s.matchIndex {
			if mIdx >= n {
				replicatedOn++
			}
		}
		if replicatedOn > (len(s.peerIds)+1)/2 {
			s.commitIndex = n
			log.Printf("[Node %s] Commit index advanced to %d", s.id, s.commitIndex)
			go s.applyCommitted()
			break
		}
	}
}

func (s *Server) applyCommitted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.lastApplied < s.commitIndex {
		s.lastApplied++
		entry, err := s.log.GetLog(s.lastApplied)
		if err != nil || entry.Command == nil {
			continue
		}
		s.mu.Unlock()
		result, err := s.stateMachine.Apply(entry.Command)
		s.mu.Lock()
		if err != nil {
			log.Printf("[Node %s] State machine error at index %d: %v", s.id, s.lastApplied, err)
		} else {
			log.Printf("[Node %s] Applied index %d - result: %v", s.id, s.lastApplied, result)
		}
	}
	s.mu.Unlock()
	// Check if we should snapshot (outside lock)
	go s.maybeSnapshot()
	s.mu.Lock()
}

func (s *Server) Submit(command []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != Leader {
		return false
	}

	lastIdx, _ := s.log.LastIndex()
	newIndex := lastIdx + 1

	entry := &LogEntry{
		Term:    s.currentTerm,
		Index:   newIndex,
		Command: command,
	}

	if err := s.log.StoreLogs([]*LogEntry{entry}); err != nil {
		log.Printf("[Node %s] Failed to append to local log: %v", s.id, err)
		return false
	}

	log.Printf("[Node %s] Accepted command at index %d - awaiting replication", s.id, newIndex)
	return true
}

func (s *Server) IsLeader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == Leader
}

func (s *Server) LeaderAddress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaderAddr
}

type NodeStatus struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	Term          uint64 `json:"term"`
	CommitIndex   uint64 `json:"commit_index"`
	LastApplied   uint64 `json:"last_applied"`
	SnapshotIndex uint64 `json:"snapshot_index"`
	LeaderHint    string `json:"leader_hint,omitempty"`
}

func (s *Server) Status() NodeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return NodeStatus{
		ID:            s.id,
		State:         s.state.String(),
		Term:          s.currentTerm,
		CommitIndex:   s.commitIndex,
		LastApplied:   s.lastApplied,
		SnapshotIndex: s.snapshotIndex,
		LeaderHint:    s.leaderAddr,
	}
}
func (s *Server) ReadIndex() (uint64, error) {
	s.mu.Lock()

	if s.state != Leader {
		s.mu.Unlock()
		return 0, fmt.Errorf("not leader")
	}

	// Capture current commit index — we must apply up to this before reading
	readIdx := s.commitIndex
	s.mu.Unlock()

	// Confirm leadership by sending a heartbeat round and checking quorum ack
	// For simplicity we use a lease-based approach:
	// if we received heartbeat acks from majority within last election timeout,
	// we are still leader. We implement this by doing a synchronous quorum check.
	acks := make(chan bool, len(s.peerIds))

	s.mu.Lock()
	savedTerm := s.currentTerm
	peers := s.peerIds
	args := AppendEntriesArgs{
		Term:         savedTerm,
		LeaderId:     s.id,
		LeaderAddr:   s.httpAddr,
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      nil, // empty = heartbeat
		LeaderCommit: s.commitIndex,
	}
	s.mu.Unlock()

	for _, peerId := range peers {
		go func(peer string) {
			reply, err := s.transport.SendAppendEntries(peer, args)
			acks <- (err == nil && reply.Success && reply.Term == savedTerm)
		}(peerId)
	}

	// Count acks — need majority (including self)
	acksReceived := 1 // count self
	total := len(peers) + 1
	needed := total/2 + 1
	deadline := time.After(500 * time.Millisecond)

	for acksReceived < needed {
		select {
		case ok := <-acks:
			if ok {
				acksReceived++
			}
		case <-deadline:
			return 0, fmt.Errorf("quorum check timed out — may have lost leadership")
		}
	}

	// Wait until state machine has applied up to readIdx
	waitDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		s.mu.Lock()
		applied := s.lastApplied
		s.mu.Unlock()

		if applied >= readIdx {
			return readIdx, nil
		}
		if time.Now().After(waitDeadline) {
			return 0, fmt.Errorf("timed out waiting for apply index %d", readIdx)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
