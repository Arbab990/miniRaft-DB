package store

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"

	"miniraft/raft"
)

// WALLogStore persists log entries to disk with CRC checksums.
// Format per record: [4 bytes CRC][4 bytes length][gob-encoded LogEntry]
type WALLogStore struct {
	mu   sync.RWMutex
	logs []*raft.LogEntry // in-memory index for fast access
	file *os.File
}

func NewWALLogStore(path string) (*WALLogStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("wal open: %w", err)
	}
	w := &WALLogStore{
		file: f,
		logs: []*raft.LogEntry{{Term: 0, Index: 0}}, // 1-indexed padding
	}
	if err := w.recover(); err != nil {
		return nil, fmt.Errorf("wal recover: %w", err)
	}
	return w, nil
}

// recover replays the WAL file into memory on startup.
// Truncates corrupted tail entries; handles crash-mid-write.
func (w *WALLogStore) recover() error {
	// Re-open for reading from start
	r, err := os.Open(w.file.Name())
	if err != nil {
		return err
	}
	defer r.Close()

	br := bufio.NewReader(r)
	var validOffset int64

	for {
		// Read CRC (4 bytes)
		var storedCRC uint32
		if err := binary.Read(br, binary.BigEndian, &storedCRC); err != nil {
			if err == io.EOF {
				break
			}
			// Partial write; truncate here.
			break
		}

		// Read length (4 bytes)
		var length uint32
		if err := binary.Read(br, binary.BigEndian, &length); err != nil {
			break
		}

		if length > 10*1024*1024 {
			break // corrupt
		}

		// Read payload
		payload := make([]byte, length)
		if _, err := io.ReadFull(br, payload); err != nil {
			break
		}

		// Verify CRC
		if crc32.ChecksumIEEE(payload) != storedCRC {
			break // corrupted entry; truncate
		}

		var entry raft.LogEntry
		if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&entry); err != nil {
			break
		}

		w.logs = append(w.logs, &entry)
		validOffset += 4 + 4 + int64(length)
	}

	// Truncate file to last valid offset if needed
	w.file.Close()
	if err := os.Truncate(w.file.Name(), validOffset); err != nil {
		return err
	}
	f, err := os.OpenFile(w.file.Name(), os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *WALLogStore) FirstIndex() (uint64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.logs) == 0 {
		return 0, nil
	}
	return w.logs[0].Index, nil
}

func (w *WALLogStore) LastIndex() (uint64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return uint64(len(w.logs) - 1), nil
}

func (w *WALLogStore) GetLog(index uint64) (*raft.LogEntry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if index >= uint64(len(w.logs)) || index == 0 {
		return nil, ErrLogNotFound
	}
	return w.logs[index], nil
}

func (w *WALLogStore) StoreLogs(logs []*raft.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, entry := range logs {
		// Truncate in-memory index on conflict
		if entry.Index < uint64(len(w.logs)) {
			w.logs = w.logs[:entry.Index]
		}

		// Gob-encode
		var buf []byte
		pw := &byteWriter{}
		if err := gob.NewEncoder(pw).Encode(entry); err != nil {
			return err
		}
		buf = pw.buf

		// Compute CRC
		crc := crc32.ChecksumIEEE(buf)

		// Write: CRC + length + payload
		if err := binary.Write(w.file, binary.BigEndian, crc); err != nil {
			return err
		}
		if err := binary.Write(w.file, binary.BigEndian, uint32(len(buf))); err != nil {
			return err
		}
		if _, err := w.file.Write(buf); err != nil {
			return err
		}

		w.logs = append(w.logs, entry)
	}

	// fsync guarantees durability before returning success.
	return w.file.Sync()
}

// TruncateBefore discards in-memory entries before index and rewrites the WAL.
// This is called after a snapshot is persisted.
func (w *WALLogStore) TruncateBefore(index uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if index >= uint64(len(w.logs)) {
		w.logs = w.logs[:1]
	} else {
		w.logs = append(w.logs[:1], w.logs[index:]...)
	}

	path := w.file.Name()
	if err := w.file.Close(); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w.file = f

	// Re-append all remaining logs, skipping padding at index 0.
	for _, entry := range w.logs[1:] {
		pw := &byteWriter{}
		if err := gob.NewEncoder(pw).Encode(entry); err != nil {
			return err
		}

		crc := crc32.ChecksumIEEE(pw.buf)
		if err := binary.Write(w.file, binary.BigEndian, crc); err != nil {
			return err
		}
		if err := binary.Write(w.file, binary.BigEndian, uint32(len(pw.buf))); err != nil {
			return err
		}
		if _, err := w.file.Write(pw.buf); err != nil {
			return err
		}
	}

	return w.file.Sync()
}

// --- helpers ---

type byteWriter struct{ buf []byte }

func (b *byteWriter) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}
