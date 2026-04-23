package raft

import (
	"encoding/gob"
	"fmt"
	"os"
)

// Snapshot is the serialized state of the KV store at a point in time.
type Snapshot struct {
	LastIndex uint64
	LastTerm  uint64
	Data      map[string]string
}

// SaveSnapshot writes a snapshot to disk atomically.
func SaveSnapshot(path string, snap Snapshot) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("snapshot create: %w", err)
	}

	if err := gob.NewEncoder(f).Encode(snap); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("snapshot encode: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("snapshot sync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("snapshot close: %w", err)
	}

	return os.Rename(tmp, path)
}

// LoadSnapshot reads a snapshot from disk.
func LoadSnapshot(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("snapshot open: %w", err)
	}
	defer f.Close()

	var snap Snapshot
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return nil, fmt.Errorf("snapshot decode: %w", err)
	}
	return &snap, nil
}
