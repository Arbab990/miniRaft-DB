package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"miniraft/raft"
	"miniraft/store"
)

// HttpServer wraps a Raft node and exposes it over HTTP.
type HttpServer struct {
	node    *raft.Server
	db      *store.MemoryKVStore
	address string
}

func NewHttpServer(address string, node *raft.Server, db *store.MemoryKVStore) *HttpServer {
	return &HttpServer{
		node:    node,
		db:      db,
		address: address,
	}
}

func (h *HttpServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", h.handleSet)
	mux.HandleFunc("/get", h.handleGet)
	mux.HandleFunc("/status", h.handleStatus)

	log.Printf("[HTTP] Node listening on %s", h.address)
	if err := http.ListenAndServe(h.address, mux); err != nil {
		log.Fatalf("[HTTP] Failed to start on %s: %v", h.address, err)
	}
}

// handleSet accepts POST /set?key=foo&value=bar
// If this node is not the leader, it returns the leader's address so
// the client knows where to redirect.
func (h *HttpServer) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := r.FormValue("key")
	value := r.FormValue("value")
	if key == "" || value == "" {
		http.Error(w, "key and value are required", http.StatusBadRequest)
		return
	}

	command := fmt.Sprintf("SET %s %s", key, value)
	accepted := h.node.Submit([]byte(command))

	if !accepted {
		// Not the leader — tell the client who is
		leaderAddr := h.node.LeaderAddress()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTemporaryRedirect)
		json.NewEncoder(w).Encode(map[string]string{
			"error":       "not the leader",
			"leader_hint": leaderAddr,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "accepted",
		"key":    key,
		"value":  value,
	})
}

// handleGet accepts GET /get?key=foo
// Reads directly from the local state machine.
// NOTE: for linearizable reads this must go through the leader.
// We flag non-leader reads so the client knows they may be slightly stale.
// Replace handleGet in server/http.go with this:

func (h *HttpServer) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := r.FormValue("key")
	if key == "" {
		http.Error(w, "key is required", http.StatusBadRequest)
		return
	}

	// Attempt linearizable read via ReadIndex
	_, err := h.node.ReadIndex()
	linearizable := err == nil

	// If not leader and ReadIndex failed, still serve the read but flag it
	value, ok := h.db.Get(key)
	w.Header().Set("Content-Type", "application/json")

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "key not found",
			"key":   key,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":          key,
		"value":        value,
		"linearizable": linearizable,
	})
}

// handleStatus returns the full cluster view from this node's perspective.
func (h *HttpServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := h.node.Status()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
