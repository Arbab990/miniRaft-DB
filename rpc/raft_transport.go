package rpc

import (
	"fmt"

	"miniraft/raft"
)

// TCPRaftTransport implements raft.Transport.
// It holds one TCPClient per peer and dispatches RPCs to them.
type TCPRaftTransport struct {
	clients map[string]*TCPClient // peerId → client
}

func NewTCPRaftTransport(peerAddrs map[string]string) *TCPRaftTransport {
	clients := make(map[string]*TCPClient)
	for id, addr := range peerAddrs {
		clients[id] = NewTCPClient(addr)
	}
	return &TCPRaftTransport{clients: clients}
}

func (t *TCPRaftTransport) SendRequestVote(peerId string, args raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	client, ok := t.clients[peerId]
	if !ok {
		return nil, fmt.Errorf("no client for peer %s", peerId)
	}
	return client.SendRequestVote(args)
}

func (t *TCPRaftTransport) SendAppendEntries(peerId string, args raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	client, ok := t.clients[peerId]
	if !ok {
		return nil, fmt.Errorf("no client for peer %s", peerId)
	}
	return client.SendAppendEntries(args)
}

func (t *TCPRaftTransport) SendInstallSnapshot(peerId string, args raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	client, ok := t.clients[peerId]
	if !ok {
		return nil, fmt.Errorf("no client for peer %s", peerId)
	}
	return client.SendInstallSnapshot(args)
}
