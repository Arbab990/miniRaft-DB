package raft

import (
	"log"
	"time"
)

// --- RequestVote ---

type RequestVoteArgs struct {
	Term         uint64
	CandidateId  string
	LastLogIndex uint64
	LastLogTerm  uint64
}

type RequestVoteReply struct {
	Term        uint64
	VoteGranted bool
}

func (s *Server) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	reply.Term = s.currentTerm
	reply.VoteGranted = false

	if args.Term < s.currentTerm {
		return nil
	}
	if args.Term > s.currentTerm {
		s.state = Follower
		s.currentTerm = args.Term
		s.votedFor = ""
	}

	lastIdx, _ := s.log.LastIndex()
	var lastTerm uint64
	if lastIdx > 0 {
		if entry, err := s.log.GetLog(lastIdx); err == nil {
			lastTerm = entry.Term
		}
	}

	candidateUpToDate := args.LastLogTerm > lastTerm ||
		(args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIdx)
	if !candidateUpToDate {
		return nil
	}

	if s.votedFor == "" || s.votedFor == args.CandidateId {
		s.votedFor = args.CandidateId
		reply.VoteGranted = true
		s.lastHeartbeat = time.Now()
		log.Printf("[Node %s] Voted for %s in term %d", s.id, args.CandidateId, s.currentTerm)
	}

	reply.Term = s.currentTerm
	return nil
}

// --- AppendEntries ---

type AppendEntriesArgs struct {
	Term         uint64
	LeaderId     string
	LeaderAddr   string
	PrevLogIndex uint64
	PrevLogTerm  uint64
	Entries      []*LogEntry
	LeaderCommit uint64
}

type AppendEntriesReply struct {
	Term    uint64
	Success bool
}

func (s *Server) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	reply.Term = s.currentTerm
	reply.Success = false

	if args.Term < s.currentTerm {
		return nil
	}
	if args.Term > s.currentTerm {
		s.currentTerm = args.Term
		s.votedFor = ""
	}

	s.state = Follower
	s.lastHeartbeat = time.Now()
	s.leaderAddr = args.LeaderAddr
	reply.Term = s.currentTerm

	// Log Matching Property
	if args.PrevLogIndex > 0 {
		lastIdx, _ := s.log.LastIndex()
		if lastIdx < args.PrevLogIndex {
			return nil
		}
		prevEntry, err := s.log.GetLog(args.PrevLogIndex)
		if err != nil || prevEntry.Term != args.PrevLogTerm {
			return nil
		}
	}

	if len(args.Entries) > 0 {
		if err := s.log.StoreLogs(args.Entries); err != nil {
			return err
		}
		log.Printf("[Node %s] Replicated %d entries from %s", s.id, len(args.Entries), args.LeaderId)
	}

	if args.LeaderCommit > s.commitIndex {
		lastIdx, _ := s.log.LastIndex()
		if args.LeaderCommit < lastIdx {
			s.commitIndex = args.LeaderCommit
		} else {
			s.commitIndex = lastIdx
		}
		go s.applyCommitted()
	}

	reply.Success = true
	return nil
}

// --- InstallSnapshot ---

type InstallSnapshotArgs struct {
	Term              uint64
	LeaderId          string
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	Data              map[string]string
}

type InstallSnapshotReply struct {
	Term uint64
}

// InstallSnapshot is sent by the leader to bring a lagging follower up to date.
// Instead of replaying hundreds of log entries, the leader ships a full state snapshot.
func (s *Server) InstallSnapshot(args InstallSnapshotArgs, reply *InstallSnapshotReply) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	reply.Term = s.currentTerm

	if args.Term < s.currentTerm {
		return nil
	}

	s.state = Follower
	s.currentTerm = args.Term
	s.lastHeartbeat = time.Now()

	// Restore state machine
	s.stateMachine.Restore(args.Data)

	// Discard all logs covered by snapshot
	s.log.TruncateBefore(args.LastIncludedIndex + 1)

	s.snapshotIndex = args.LastIncludedIndex
	s.snapshotTerm = args.LastIncludedTerm
	s.commitIndex = args.LastIncludedIndex
	s.lastApplied = args.LastIncludedIndex

	// Persist snapshot to disk
	if s.snapshotPath != "" {
		go s.persistSnapshot(args.Data, args.LastIncludedIndex, args.LastIncludedTerm)
	}

	log.Printf("[Node %s] Installed snapshot up to index %d", s.id, args.LastIncludedIndex)
	return nil
}
