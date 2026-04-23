package rpc

import (
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
)

// MessageType identifies what kind of message is on the wire.
type MessageType uint8

const (
	MsgRequestVote          MessageType = 1
	MsgRequestVoteReply     MessageType = 2
	MsgAppendEntries        MessageType = 3
	MsgAppendEntriesReply   MessageType = 4
	MsgInstallSnapshot      MessageType = 5
	MsgInstallSnapshotReply MessageType = 6
)

// Message is the envelope for every RPC over TCP.
type Message struct {
	Type    MessageType
	Payload []byte
}

// WriteMessage frames and writes a message to a writer.
// Frame format: [1 byte type][4 byte length][N byte gob payload]
func WriteMessage(w io.Writer, msgType MessageType, payload interface{}) error {
	// 1. Gob-encode the payload into a buffer
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		enc := gob.NewEncoder(pw)
		errCh <- enc.Encode(payload)
		pw.Close()
	}()

	buf, err := io.ReadAll(pr)
	if err != nil {
		return err
	}
	if err := <-errCh; err != nil {
		return err
	}

	// 2. Write type byte
	if _, err := w.Write([]byte{byte(msgType)}); err != nil {
		return err
	}

	// 3. Write 4-byte length prefix
	length := uint32(len(buf))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}

	// 4. Write payload
	_, err = w.Write(buf)
	return err
}

// ReadMessage reads one framed message from a reader.
func ReadMessage(r io.Reader) (MessageType, []byte, error) {
	// 1. Read type byte
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, typeBuf); err != nil {
		return 0, nil, err
	}
	msgType := MessageType(typeBuf[0])

	// 2. Read 4-byte length
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, nil, err
	}

	if length > 10*1024*1024 { // 10MB sanity cap
		return 0, nil, fmt.Errorf("message too large: %d bytes", length)
	}

	// 3. Read payload
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}

	return msgType, payload, nil
}
