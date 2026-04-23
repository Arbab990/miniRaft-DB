package store

import (
	"errors"
	"miniraft/raft"
	"strings"
	"sync"
)

// ErrLogNotFound is returned when Raft asks for a log index that doesn't exist.
var ErrLogNotFound = errors.New("log entry not found")

// ---------------------------------------------------------
// 1. IN-MEMORY LOG STORE (The WAL)
// ---------------------------------------------------------

// MemoryLogStore implements raft.LogStore entirely in RAM.
type MemoryLogStore struct {
	mu   sync.RWMutex
	logs []*raft.LogEntry
}

func NewMemoryLogStore() *MemoryLogStore {
	// Raft logs are 1-indexed. We pad the 0th index so the math is easier later.
	return &MemoryLogStore{
		logs: []*raft.LogEntry{{Term: 0, Index: 0, Command: nil}},
	}
}

func (m *MemoryLogStore) FirstIndex() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// In a real DB with compaction, this wouldn't always be 0/1.
	if len(m.logs) == 0 {
		return 0, nil
	}
	return m.logs[0].Index, nil
}

func (m *MemoryLogStore) LastIndex() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.logs) == 0 {
		return 0, nil
	}
	return m.logs[len(m.logs)-1].Index, nil
}

func (m *MemoryLogStore) GetLog(index uint64) (*raft.LogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Basic bounds checking
	if index >= uint64(len(m.logs)) || index == 0 {
		return nil, ErrLogNotFound
	}
	return m.logs[index], nil
}

func (m *MemoryLogStore) StoreLogs(logs []*raft.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, l := range logs {
		// If we are overwriting existing uncommitted logs, truncate and append
		if l.Index < uint64(len(m.logs)) {
			m.logs = m.logs[:l.Index]
		}
		m.logs = append(m.logs, l)
	}
	return nil
}

// TruncateBefore discards all log entries before the given index.
// Called after a snapshot is taken to compact the log.
func (m *MemoryLogStore) TruncateBefore(index uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if index >= uint64(len(m.logs)) {
		// Truncate everything except the padding entry.
		m.logs = m.logs[:1]
		return nil
	}

	// Keep padding + entries from index onward.
	m.logs = append(m.logs[:1], m.logs[index:]...)
	return nil
}

// ---------------------------------------------------------
// 2. IN-MEMORY STATE MACHINE (The Database)
// ---------------------------------------------------------

// MemoryKVStore implements raft.StateMachine.
type MemoryKVStore struct {
	mu sync.RWMutex
	kv map[string]string
}

func NewMemoryKVStore() *MemoryKVStore {
	return &MemoryKVStore{
		kv: make(map[string]string),
	}
}

// Apply executes a command against the database.
// We expect commands in a simple "SET KEY VALUE" string format for now.
func (m *MemoryKVStore) Apply(command []byte) (interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmdStr := string(command)
	parts := strings.SplitN(cmdStr, " ", 3)

	if len(parts) != 3 || strings.ToUpper(parts[0]) != "SET" {
		return nil, errors.New("invalid command format, expected 'SET KEY VALUE'")
	}

	key, value := parts[1], parts[2]
	m.kv[key] = value

	return value, nil
}

// Get allows us to read from the DB without going through Raft (for testing).
func (m *MemoryKVStore) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.kv[key]
	return val, ok
}

// Snapshot captures full KV state.
func (m *MemoryKVStore) Snapshot() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp := make(map[string]string, len(m.kv))
	for k, v := range m.kv {
		cp[k] = v
	}
	return cp
}

// Restore replaces KV state from a snapshot.
func (m *MemoryKVStore) Restore(data map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.kv = data
}
