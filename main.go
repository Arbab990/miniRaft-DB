package main

import (
	"flag"
	"log"
	"time"

	"miniraft/raft"
	"miniraft/rpc"
	"miniraft/server"
	"miniraft/store"
)

var peers = map[string]string{
	"node1": "localhost:9001",
	"node2": "localhost:9002",
	"node3": "localhost:9003",
}

var httpAddrs = map[string]string{
	"node1": ":8081",
	"node2": ":8082",
	"node3": ":8083",
}

func main() {
	nodeId := flag.String("id", "", "Node ID: node1, node2, or node3")
	flag.Parse()

	if *nodeId == "" {
		log.Fatal("Usage: go run . -id=node1")
	}

	tcpAddr := peers[*nodeId]
	httpAddr := httpAddrs[*nodeId]
	if tcpAddr == "" || httpAddr == "" {
		log.Fatalf("Unknown node ID %q. Valid IDs: node1, node2, node3", *nodeId)
	}

	var peerIds []string
	peerAddrs := make(map[string]string)
	for id, addr := range peers {
		if id != *nodeId {
			peerIds = append(peerIds, id)
			peerAddrs[id] = addr
		}
	}

	walPath := *nodeId + ".wal"
	logStore, err := store.NewWALLogStore(walPath)
	if err != nil {
		log.Fatalf("Failed to open WAL: %v", err)
	}
	log.Printf("[Node %s] WAL opened: %s", *nodeId, walPath)

	kvStore := store.NewMemoryKVStore()
	transport := rpc.NewTCPRaftTransport(peerAddrs)
	node := raft.NewServer(*nodeId, peerIds, logStore, kvStore, httpAddr, transport)

	tcpServer := rpc.NewTCPTransport(tcpAddr, node)
	if err := tcpServer.Start(); err != nil {
		log.Fatalf("[Node %s] TCP failed: %v", *nodeId, err)
	}

	go server.NewHttpServer(httpAddr, node, kvStore).Start()

	log.Printf("[Node %s] up - TCP=%s HTTP=%s", *nodeId, tcpAddr, httpAddr)
	time.Sleep(2 * time.Second)
	log.Printf("[Node %s] ready", *nodeId)

	select {}
}
