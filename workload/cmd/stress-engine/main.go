package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"workload/internal/chain"
	"workload/internal/foc"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/antithesishq/antithesis-sdk-go/random"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	_ "github.com/filecoin-project/lotus/lib/sigs/secp"
	"github.com/ipfs/go-cid"
)

// ---------------------------------------------------------------------------
// Global State (flat architecture — no constructors, no DI)
// ---------------------------------------------------------------------------

var (
	ctx    context.Context
	cancel context.CancelFunc

	// Node connections: key = node hostname (e.g. "lotus0")
	nodes    map[string]api.FullNode
	nodeKeys []string

	// Wallet state loaded from stress_keystore.json
	keystore map[address.Address]*types.KeyInfo
	addrs    []address.Address

	// Per-address monotonic nonce counter
	nonces map[address.Address]uint64

	// Weighted action deck with names for logging
	deck []namedAction

	// Deployed contract registry (protected by contractsMu)
	deployedContracts []deployedContract
	contractsMu       sync.Mutex

	// Contract bytecodes (loaded from embedded hex in contracts.go)
	contractBytecodes map[string][]byte
	contractTypes     []string // keys of contractBytecodes for random selection

	// Pending deploy CIDs for deferred verification
	pendingDeploys []pendingDeploy
	pendingMu      sync.Mutex

	// FOC config — nil when the FOC compose profile is not active
	focCfg *foc.Config
)

type deployedContract struct {
	addr     address.Address
	ctype    string // "recursive", "selfdestruct", "simplecoin", etc.
	deployer address.Address
	deployKI *types.KeyInfo
}

type pendingDeploy struct {
	msgCid   cid.Cid
	ctype    string
	deployer address.Address
	deployKI *types.KeyInfo
	epoch    abi.ChainEpoch
}

// namedAction pairs an action function with its name for logging
type namedAction struct {
	name string
	fn   func()
}

// ---------------------------------------------------------------------------
// Configuration helpers
// ---------------------------------------------------------------------------

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("[config] invalid int for %s=%q, using default %d", key, v, fallback)
		return fallback
	}
	return n
}

// ---------------------------------------------------------------------------
// Randomness helpers (Antithesis SDK — deterministic)
// ---------------------------------------------------------------------------

func rngIntn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(random.GetRandom() % uint64(n))
}

func rngChoice[T any](items []T) T {
	return random.RandomChoice(items)
}

func pickNode() (string, api.FullNode) {
	name := rngChoice(nodeKeys)
	return name, nodes[name]
}

func pickWallet() (address.Address, *types.KeyInfo) {
	addr := rngChoice(addrs)
	return addr, keystore[addr]
}

// ---------------------------------------------------------------------------
// Initialization
// ---------------------------------------------------------------------------

func connectNodes() {
	cfg := chain.NodeConfig{
		Names:      strings.Split(envOrDefault("STRESS_NODES", "lotus0"), ","),
		Port:       envOrDefault("STRESS_RPC_PORT", "1234"),
		ForestPort: envOrDefault("STRESS_FOREST_RPC_PORT", "3456"),
	}

	var err error
	nodes, nodeKeys, err = chain.ConnectNodes(ctx, cfg)
	if err != nil {
		log.Fatalf("[init] FATAL: %v", err)
	}
}

// KeystoreEntry matches the JSON format written by genesis-prep.
type KeystoreEntry struct {
	Address    string `json:"Address"`
	PrivateKey string `json:"PrivateKey"`
}

func loadKeystore() {
	path := envOrDefault("STRESS_KEYSTORE_PATH", "/shared/stress_keystore.json")
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("[init] FATAL: cannot read keystore at %s: %v", path, err)
	}

	var entries []KeystoreEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Fatalf("[init] FATAL: cannot parse keystore: %v", err)
	}

	keystore = make(map[address.Address]*types.KeyInfo, len(entries))
	nonces = make(map[address.Address]uint64, len(entries))
	addrs = make([]address.Address, 0, len(entries))

	for _, e := range entries {
		addr, err := address.NewFromString(e.Address)
		if err != nil {
			log.Printf("[init] WARN: skipping invalid address %q: %v", e.Address, err)
			continue
		}
		pk, err := hex.DecodeString(e.PrivateKey)
		if err != nil {
			log.Printf("[init] WARN: skipping address %s, bad private key hex: %v", e.Address, err)
			continue
		}
		keystore[addr] = &types.KeyInfo{
			Type:       types.KTSecp256k1,
			PrivateKey: pk,
		}
		addrs = append(addrs, addr)
	}

	if len(addrs) == 0 {
		log.Fatal("[init] FATAL: no valid keys loaded from keystore")
	}
	log.Printf("[init] loaded %d keys from keystore", len(addrs))
}

func waitForChain() {
	targetHeight := envInt("STRESS_WAIT_HEIGHT", 10)
	node := nodes[nodeKeys[0]]
	log.Printf("[init] waiting for chain height >= %d ...", targetHeight)

	for {
		head, err := node.ChainHead(ctx)
		if err != nil {
			log.Printf("[init] ChainHead error: %v, retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if int(head.Height()) >= targetHeight {
			log.Printf("[init] chain at height %d, proceeding", head.Height())
			return
		}
		log.Printf("[init] chain at height %d, waiting...", head.Height())
		time.Sleep(2 * time.Second)
	}
}

func initNonces() {
	node := nodes[nodeKeys[0]]
	for _, addr := range addrs {
		n, err := node.MpoolGetNonce(ctx, addr)
		if err != nil {
			log.Printf("[init] WARN: cannot get nonce for %s: %v, starting at 0", addr, err)
			nonces[addr] = 0
			continue
		}
		nonces[addr] = n
	}
	log.Printf("[init] initialized nonces for %d addresses", len(addrs))
}

// ---------------------------------------------------------------------------
// Deck building
// ---------------------------------------------------------------------------

func buildDeck() {
	profile, mults := resolveProfile()

	// Gather applicable vectors
	vectors := coreVectors()
	if focCfg != nil {
		vectors = append(vectors, focVectors()...)
	}

	logProfileSummary(profile, mults)

	deck = nil
	for _, v := range vectors {
		w := resolveWeight(v, mults)
		if w > 0 {
			suffix := ""
			if _, ok := envIntIfSet(v.EnvVar); ok {
				suffix = fmt.Sprintf("  (override: %s)", v.EnvVar)
			}
			log.Printf("[init]   %-35s cat=%-12s base=%d x mult=%d => %d%s",
				v.Name, v.Category, v.BaseWeight, mults[v.Category], w, suffix)
		}
		for i := 0; i < w; i++ {
			deck = append(deck, namedAction{name: v.Name, fn: v.Fn})
		}
	}

	if len(deck) == 0 {
		log.Fatal("[init] FATAL: deck is empty — check STRESS_PROFILE or set weights > 0")
	}
	log.Printf("[init] deck built with %d entries", len(deck))
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Handle STRESS_PROFILE=help before connecting to nodes
	if os.Getenv("STRESS_PROFILE") == "help" {
		printProfileHelp()
		os.Exit(0)
	}

	log.Println("[engine] stress engine starting")

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	connectNodes()
	loadKeystore()
	waitForChain()
	initNonces()
	initContractBytecodes()
	focCfg = foc.ParseEnvironment()
	buildDeck()

	lifecycle.SetupComplete(map[string]any{
		"nodes":   len(nodes),
		"wallets": len(addrs),
		"deck":    len(deck),
	})

	// Start background fork monitor — runs independently of the deck so it
	// can observe forks while DoReorgChaos partitions are active.
	startForkMonitor()

	log.Println("[engine] entering main loop")

	// Track action execution counts for periodic summary
	actionCounts := make(map[string]int)
	iteration := 0

	for {
		idx := rngIntn(len(deck))
		action := deck[idx]

		debugLog("[engine] running: %s", action.name)
		action.fn()

		actionCounts[action.name]++
		iteration++

		// Periodic summary every 500 iterations
		if iteration%500 == 0 {
			log.Printf("[engine] === iteration %d summary ===", iteration)
			for name, count := range actionCounts {
				log.Printf("[engine]   %s: %d", name, count)
			}
			if focCfg != nil {
				logFOCProgress()
			}
		}
	}
}
