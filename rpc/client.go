package rpc

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"net"
	"time"

	"miniraft/raft"
)

// TCPClient sends Raft RPCs to a single peer over TCP.
// A new connection is opened per RPC — simple and reliable for our scale.
type TCPClient struct {
	peerAddr string
}

func NewTCPClient(peerAddr string) *TCPClient {
	return &TCPClient{peerAddr: peerAddr}
}

func (c *TCPClient) SendRequestVote(args raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := WriteMessage(conn, MsgRequestVote, args); err != nil {
		return nil, err
	}

	_, payload, err := ReadMessage(conn)
	if err != nil {
		return nil, err
	}

	var reply raft.RequestVoteReply
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func (c *TCPClient) SendAppendEntries(args raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := WriteMessage(conn, MsgAppendEntries, args); err != nil {
		return nil, err
	}

	_, payload, err := ReadMessage(conn)
	if err != nil {
		return nil, err
	}

	var reply raft.AppendEntriesReply
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func (c *TCPClient) SendInstallSnapshot(args raft.InstallSnapshotArgs) (*raft.InstallSnapshotReply, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := WriteMessage(conn, MsgInstallSnapshot, args); err != nil {
		return nil, err
	}

	_, payload, err := ReadMessage(conn)
	if err != nil {
		return nil, err
	}

	var reply raft.InstallSnapshotReply
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// dial opens a TCP connection with a short timeout.
func (c *TCPClient) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", c.peerAddr, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.peerAddr, err)
	}
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	return conn, nil
}
