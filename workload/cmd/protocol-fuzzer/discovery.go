package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// TargetNode represents a Filecoin node reachable via libp2p.
type TargetNode struct {
	Name     string
	AddrInfo peer.AddrInfo
}

const (
	discoveryRetryInterval = 5 * time.Second
	discoveryTimeout       = 5 * time.Minute
)

// discoverNodes reads multiaddr files written by each node's startup script.
// Each node writes its listening address to {devgenDir}/{name}/{name}-ipv4addr.
// The file contains a full multiaddr like /ip4/172.x.x.x/tcp/XXXX/p2p/12D3Koo...
func discoverNodes(names []string, devgenDir string) []TargetNode {
	var targets []TargetNode
	for _, name := range names {
		addrFile := fmt.Sprintf("%s/%s/%s-ipv4addr", devgenDir, name, name)

		data, err := os.ReadFile(addrFile)
		if err != nil {
			log.Printf("[discovery] skipping %s: cannot read %s: %v", name, addrFile, err)
			continue
		}

		addrStr := strings.TrimSpace(string(data))
		if addrStr == "" {
			log.Printf("[discovery] skipping %s: empty address file %s", name, addrFile)
			continue
		}

		ma, err := multiaddr.NewMultiaddr(addrStr)
		if err != nil {
			log.Printf("[discovery] skipping %s: invalid multiaddr %q: %v", name, addrStr, err)
			continue
		}

		ai, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			log.Printf("[discovery] skipping %s: cannot parse AddrInfo from %q: %v", name, addrStr, err)
			continue
		}

		targets = append(targets, TargetNode{Name: name, AddrInfo: *ai})
		log.Printf("[discovery] found %s: peer=%s addr=%s", name, ai.ID.String()[:16], addrStr)
	}
	return targets
}

// waitForNodes retries discovery until at least one node is found or timeout.
func waitForNodes(names []string, devgenDir string) []TargetNode {
	deadline := time.Now().Add(discoveryTimeout)

	for time.Now().Before(deadline) {
		targets := discoverNodes(names, devgenDir)
		if len(targets) > 0 {
			log.Printf("[discovery] found %d/%d nodes", len(targets), len(names))
			return targets
		}
		log.Printf("[discovery] no nodes found yet, retrying in %s...", discoveryRetryInterval)
		time.Sleep(discoveryRetryInterval)
	}

	log.Fatal("[discovery] FATAL: no nodes found within timeout")
	return nil
}

// loadNetworkName reads the network name written by lotus0 at startup.
func loadNetworkName(devgenDir string) string {
	path := fmt.Sprintf("%s/lotus0/network_name", devgenDir)

	deadline := time.Now().Add(discoveryTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			name := strings.TrimSpace(string(data))
			if name != "" {
				log.Printf("[discovery] network name: %s", name)
				return name
			}
		}
		time.Sleep(discoveryRetryInterval)
	}

	log.Fatal("[discovery] FATAL: cannot read network name from " + path)
	return ""
}

// discoverGenesisCID fetches the genesis CID from a Lotus node's RPC endpoint.
// Uses unauthenticated HTTP POST to Filecoin.ChainGetGenesis.
func discoverGenesisCID(rpcURL string) string {
	type rpcRequest struct {
		Jsonrpc string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
		ID      int    `json:"id"`
	}
	type rpcResponse struct {
		Result struct {
			Cids []struct {
				Root string `json:"/"`
			} `json:"Cids"`
		} `json:"result"`
	}

	reqBody, _ := json.Marshal(rpcRequest{
		Jsonrpc: "2.0",
		Method:  "Filecoin.ChainGetGenesis",
		Params:  []any{},
		ID:      1,
	})

	deadline := time.Now().Add(discoveryTimeout)
	for time.Now().Before(deadline) {
		resp, err := http.Post(rpcURL, "application/json", bytes.NewReader(reqBody))
		if err != nil {
			log.Printf("[discovery] genesis CID fetch failed: %v, retrying...", err)
			time.Sleep(discoveryRetryInterval)
			continue
		}

		var rpcResp rpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			resp.Body.Close()
			log.Printf("[discovery] genesis CID decode failed: %v, retrying...", err)
			time.Sleep(discoveryRetryInterval)
			continue
		}
		resp.Body.Close()

		if len(rpcResp.Result.Cids) > 0 {
			genCID := rpcResp.Result.Cids[0].Root
			log.Printf("[discovery] genesis CID: %s", genCID)
			return genCID
		}

		log.Printf("[discovery] genesis CID empty, retrying...")
		time.Sleep(discoveryRetryInterval)
	}

	log.Fatal("[discovery] FATAL: cannot discover genesis CID")
	return ""
}
