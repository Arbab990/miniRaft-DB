package rpc

import (
	"bytes"
	"encoding/gob"
	"log"
	"net"

	"miniraft/raft"
)

// Handler is implemented by raft.Server — it processes incoming RPCs.
type Handler interface {
	RequestVote(args raft.RequestVoteArgs, reply *raft.RequestVoteReply) error
	AppendEntries(args raft.AppendEntriesArgs, reply *raft.AppendEntriesReply) error
	InstallSnapshot(args raft.InstallSnapshotArgs, reply *raft.InstallSnapshotReply) error
}

// TCPTransport listens for incoming Raft RPCs over TCP.
type TCPTransport struct {
	addr    string
	handler Handler
}

func NewTCPTransport(addr string, handler Handler) *TCPTransport {
	return &TCPTransport{addr: addr, handler: handler}
}

// Start begins listening for inbound RPC connections.
// Each connection is handled in its own goroutine.
func (t *TCPTransport) Start() error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return err
	}
	log.Printf("[Transport] Listening on %s", t.addr)
	go t.acceptLoop(ln)
	return nil
}

func (t *TCPTransport) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[Transport] Accept error: %v", err)
			return
		}
		go t.handleConn(conn)
	}
}

// handleConn reads one RPC request, dispatches it, writes the reply.
func (t *TCPTransport) handleConn(conn net.Conn) {
	defer conn.Close()

	msgType, payload, err := ReadMessage(conn)
	if err != nil {
		return
	}

	switch msgType {
	case MsgRequestVote:
		var args raft.RequestVoteArgs
		if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&args); err != nil {
			return
		}
		var reply raft.RequestVoteReply
		t.handler.RequestVote(args, &reply)
		WriteMessage(conn, MsgRequestVoteReply, reply)

	case MsgAppendEntries:
		var args raft.AppendEntriesArgs
		if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&args); err != nil {
			return
		}
		var reply raft.AppendEntriesReply
		t.handler.AppendEntries(args, &reply)
		WriteMessage(conn, MsgAppendEntriesReply, reply)

	case MsgInstallSnapshot:
		var args raft.InstallSnapshotArgs
		if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&args); err != nil {
			return
		}
		var reply raft.InstallSnapshotReply
		t.handler.InstallSnapshot(args, &reply)
		WriteMessage(conn, MsgInstallSnapshotReply, reply)

	default:
		log.Printf("[Transport] Unknown message type: %d", msgType)
	}
}
