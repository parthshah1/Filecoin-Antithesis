package main

import (
	"log"
	"math"
	"math/big"
	"sync"

	"github.com/antithesishq/antithesis-sdk-go/assert"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/crypto"
	builtintypes "github.com/filecoin-project/go-state-types/builtin"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/ipfs/go-cid"
)

// ===========================================================================
// Helpers for fuzzing vectors
// ===========================================================================

// stateCallMsg wraps StateCall with empty TipSetKey for read-only VM execution.
func stateCallMsg(node api.FullNode, msg *types.Message) (*api.InvocResult, error) {
	return node.StateCall(ctx, msg, types.EmptyTSK)
}

// rngShuffle returns a shuffled copy of the slice using Fisher-Yates with
// Antithesis RNG for deterministic replay.
func rngShuffle[T any](pool []T) []T {
	result := make([]T, len(pool))
	copy(result, pool)
	for i := len(result) - 1; i > 0; i-- {
		j := rngIntn(i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// rngBytes returns n bytes of pseudo-random data from Antithesis RNG.
func rngBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rngIntn(256))
	}
	return b
}

// ===========================================================================
// P0 — Vector 1: DoNilTicketBlock
//
// Exact repro of $19,150 "Arbitrary node crash via Nil-Ticket Block".
// Clones a real block header and randomly nils 1-4 pointer fields.
// Submits to every node via SyncSubmitBlock.
// ===========================================================================

func DoNilTicketBlock() {
	nodeName, node := pickNode()
	head, err := node.ChainHead(ctx)
	if err != nil || head == nil || len(head.Blocks()) == 0 {
		return
	}

	parentBlock := head.Blocks()[0]

	// Clone block header with all real values
	header := types.BlockHeader{
		Miner:                 parentBlock.Miner,
		Parents:               head.Cids(),
		Height:                head.Height() + 1,
		ParentStateRoot:       parentBlock.ParentStateRoot,
		ParentMessageReceipts: parentBlock.ParentMessageReceipts,
		Messages:              parentBlock.Messages,
		ParentBaseFee:         parentBlock.ParentBaseFee,
		ParentWeight:          types.BigInt{Int: parentBlock.ParentWeight.Int},
		Timestamp:             parentBlock.Timestamp + 30,
		Ticket:                parentBlock.Ticket,
		ElectionProof:         parentBlock.ElectionProof,
		BLSAggregate:          parentBlock.BLSAggregate,
		BlockSig:              parentBlock.BlockSig,
		BeaconEntries:         parentBlock.BeaconEntries,
		WinPoStProof:          parentBlock.WinPoStProof,
		ForkSignaling:         parentBlock.ForkSignaling,
	}

	// Pointer field pool — randomly nil 1 to all
	type ptrField struct {
		name string
		nil_ func(h *types.BlockHeader)
	}
	pool := []ptrField{
		{"Ticket", func(h *types.BlockHeader) { h.Ticket = nil }},
		{"ElectionProof", func(h *types.BlockHeader) { h.ElectionProof = nil }},
		{"BLSAggregate", func(h *types.BlockHeader) { h.BLSAggregate = nil }},
		{"BlockSig", func(h *types.BlockHeader) { h.BlockSig = nil }},
	}

	count := rngIntn(len(pool)) + 1
	shuffled := rngShuffle(pool)
	var nilledFields []string
	for i := 0; i < count; i++ {
		shuffled[i].nil_(&header)
		nilledFields = append(nilledFields, shuffled[i].name)
	}

	blk := &types.BlockMsg{
		Header:        &header,
		BlsMessages:   []cid.Cid{},
		SecpkMessages: []cid.Cid{},
	}

	// Submit to every node
	for _, name := range nodeKeys {
		submitErr := nodes[name].SyncSubmitBlock(ctx, blk)
		rejectedCleanly := submitErr != nil
		assert.Always(rejectedCleanly,
			"Node rejects nil-pointer block without crashing",
			map[string]any{
				"node":   name,
				"fields": nilledFields,
				"error":  errStr(submitErr),
			})
	}

	debugLog("[nil-ticket] submitted block with nil fields %v via %s", nilledFields, nodeName)
}

// ===========================================================================
// P0 — Vector 2: DoNilFieldBlock
//
// Generalized nil/empty field exploration including slice fields.
// Randomly corrupts 1-7 fields per invocation.
// ===========================================================================

func DoNilFieldBlock() {
	_, node := pickNode()
	head, err := node.ChainHead(ctx)
	if err != nil || head == nil || len(head.Blocks()) == 0 {
		return
	}

	parentBlock := head.Blocks()[0]

	header := types.BlockHeader{
		Miner:                 parentBlock.Miner,
		Parents:               head.Cids(),
		Height:                head.Height() + 1,
		ParentStateRoot:       parentBlock.ParentStateRoot,
		ParentMessageReceipts: parentBlock.ParentMessageReceipts,
		Messages:              parentBlock.Messages,
		ParentBaseFee:         parentBlock.ParentBaseFee,
		ParentWeight:          types.BigInt{Int: parentBlock.ParentWeight.Int},
		Timestamp:             parentBlock.Timestamp + 30,
		Ticket:                parentBlock.Ticket,
		ElectionProof:         parentBlock.ElectionProof,
		BLSAggregate:          parentBlock.BLSAggregate,
		BlockSig:              parentBlock.BlockSig,
		BeaconEntries:         parentBlock.BeaconEntries,
		WinPoStProof:          parentBlock.WinPoStProof,
		ForkSignaling:         parentBlock.ForkSignaling,
	}

	type fieldCorruptor struct {
		name  string
		apply func(h *types.BlockHeader)
	}

	pool := []fieldCorruptor{
		{"Ticket", func(h *types.BlockHeader) { h.Ticket = nil }},
		{"ElectionProof", func(h *types.BlockHeader) { h.ElectionProof = nil }},
		{"BLSAggregate", func(h *types.BlockHeader) { h.BLSAggregate = nil }},
		{"BlockSig", func(h *types.BlockHeader) { h.BlockSig = nil }},
		{"Parents", func(h *types.BlockHeader) { h.Parents = nil }},
		{"BeaconEntries", func(h *types.BlockHeader) { h.BeaconEntries = nil }},
		{"WinPoStProof", func(h *types.BlockHeader) { h.WinPoStProof = nil }},
	}

	count := rngIntn(len(pool)) + 1
	shuffled := rngShuffle(pool)
	var applied []string
	for i := 0; i < count; i++ {
		shuffled[i].apply(&header)
		applied = append(applied, shuffled[i].name)
	}

	blk := &types.BlockMsg{
		Header:        &header,
		BlsMessages:   []cid.Cid{},
		SecpkMessages: []cid.Cid{},
	}

	targetName, targetNode := pickNode()
	submitErr := targetNode.SyncSubmitBlock(ctx, blk)
	rejectedCleanly := submitErr != nil
	assert.Always(rejectedCleanly,
		"Node rejects nil-field block without crashing",
		map[string]any{
			"node":   targetName,
			"fields": applied,
			"error":  errStr(submitErr),
		})

	debugLog("[nil-field] block with nil fields %v → %s, rejected=%v", applied, targetName, rejectedCleanly)
}

// ===========================================================================
// P0 — Vector 3: DoBoundaryEpochBlock
//
// Fuzzes numeric fields (Height, Timestamp, ParentBaseFee, ParentWeight,
// ElectionProof.WinCount) with extreme boundary values.
// ===========================================================================

func DoBoundaryEpochBlock() {
	_, node := pickNode()
	head, err := node.ChainHead(ctx)
	if err != nil || head == nil || len(head.Blocks()) == 0 {
		return
	}

	parentBlock := head.Blocks()[0]

	header := types.BlockHeader{
		Miner:                 parentBlock.Miner,
		Parents:               head.Cids(),
		Height:                head.Height() + 1,
		ParentStateRoot:       parentBlock.ParentStateRoot,
		ParentMessageReceipts: parentBlock.ParentMessageReceipts,
		Messages:              parentBlock.Messages,
		ParentBaseFee:         parentBlock.ParentBaseFee,
		ParentWeight:          types.BigInt{Int: parentBlock.ParentWeight.Int},
		Timestamp:             parentBlock.Timestamp + 30,
		Ticket:                parentBlock.Ticket,
		ElectionProof:         parentBlock.ElectionProof,
		BLSAggregate:          parentBlock.BLSAggregate,
		BlockSig:              parentBlock.BlockSig,
		BeaconEntries:         parentBlock.BeaconEntries,
		WinPoStProof:          parentBlock.WinPoStProof,
		ForkSignaling:         parentBlock.ForkSignaling,
	}

	type numericCorruptor struct {
		name  string
		apply func(h *types.BlockHeader)
	}

	pool := []numericCorruptor{
		{"Height", func(h *types.BlockHeader) {
			extremes := []abi.ChainEpoch{0, -1, math.MaxInt64, math.MinInt64, h.Height + 999999}
			h.Height = extremes[rngIntn(len(extremes))]
		}},
		{"Timestamp", func(h *types.BlockHeader) {
			extremes := []uint64{0, math.MaxUint64, 1, h.Timestamp + 86400}
			h.Timestamp = extremes[rngIntn(len(extremes))]
		}},
		{"ParentBaseFee", func(h *types.BlockHeader) {
			extremes := []*big.Int{
				big.NewInt(0),
				new(big.Int).SetUint64(math.MaxUint64),
				big.NewInt(-1),
				new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil),
			}
			h.ParentBaseFee = abi.TokenAmount{Int: extremes[rngIntn(len(extremes))]}
		}},
		{"ParentWeight", func(h *types.BlockHeader) {
			extremes := []*big.Int{
				big.NewInt(0),
				big.NewInt(-1),
				new(big.Int).SetUint64(math.MaxUint64),
			}
			h.ParentWeight = types.BigInt{Int: extremes[rngIntn(len(extremes))]}
		}},
		{"WinCount", func(h *types.BlockHeader) {
			if h.ElectionProof == nil {
				h.ElectionProof = &types.ElectionProof{VRFProof: rngBytes(32)}
			}
			extremes := []int64{-1, 0, math.MaxInt64, math.MinInt64}
			h.ElectionProof.WinCount = extremes[rngIntn(len(extremes))]
		}},
	}

	count := rngIntn(len(pool)) + 1
	shuffled := rngShuffle(pool)
	var applied []string
	for i := 0; i < count; i++ {
		shuffled[i].apply(&header)
		applied = append(applied, shuffled[i].name)
	}

	blk := &types.BlockMsg{
		Header:        &header,
		BlsMessages:   []cid.Cid{},
		SecpkMessages: []cid.Cid{},
	}

	targetName, targetNode := pickNode()
	submitErr := targetNode.SyncSubmitBlock(ctx, blk)
	_ = submitErr // rejection expected; crash is auto-detected by Antithesis

	debugLog("[boundary-epoch] block with extreme fields %v → %s", applied, targetName)
}

// ===========================================================================
// P1 — Vector 4: DoFVMCallDepthBomb
//
// Targets $30,000 "FEVM Call Depth Attack" by driving recursive calls to
// the FVM call stack limit boundary. Compares ExitCode across all nodes.
// ===========================================================================

const fvmMaxCallDepth = 1024

func DoFVMCallDepthBomb() {
	contracts := getContractsByType("recursive")
	if len(contracts) == 0 {
		contracts = getContractsByType("extrecursive")
	}
	if len(contracts) == 0 {
		// No recursive contracts deployed yet — trigger a deploy
		doDeployStressContract("recursive")
		return
	}

	target := rngChoice(contracts)

	// Depth pool — randomly selected each invocation
	depthPool := []uint64{1, 100, 1022, 1023, 1024, 1025, 2048, 65535}
	targetDepth := depthPool[rngIntn(len(depthPool))]

	// Build calldata for recursiveCall(uint256)
	calldata, err := cborWrapCalldata(
		calcSelector("recursiveCall(uint256)"),
		encodeUint256(targetDepth),
	)
	if err != nil {
		log.Printf("[call-depth] cborWrapCalldata failed: %v", err)
		return
	}

	// Submit to all nodes in parallel, collect ExitCodes
	type nodeResult struct {
		name     string
		exitCode int64
		ok       bool
	}
	var results []nodeResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	fromAddr, fromKI := pickWallet()

	for _, name := range nodeKeys {
		wg.Add(1)
		go func(name string, node api.FullNode) {
			defer wg.Done()

			msg := &types.Message{
				From:   fromAddr,
				To:     target.addr,
				Value:  abi.NewTokenAmount(0),
				Method: builtintypes.MethodsEVM.InvokeContract,
				Params: calldata,
			}

			invocResult, err := stateCallMsg(node, msg)
			if err != nil {
				debugLog("[call-depth] StateCall failed on %s: %v", name, err)
				return
			}

			mu.Lock()
			results = append(results, nodeResult{
				name:     name,
				exitCode: int64(invocResult.MsgRct.ExitCode),
				ok:       true,
			})
			mu.Unlock()
		}(name, nodes[name])
	}
	wg.Wait()

	_ = fromKI // only needed for actual on-chain submission

	if len(results) < 2 {
		return
	}

	// All nodes must agree on ExitCode
	ref := results[0]
	for _, r := range results[1:] {
		assert.Always(ref.exitCode == r.exitCode,
			"FVM call depth ExitCode consistent across nodes",
			map[string]any{
				"target_depth": targetDepth,
				"node_a":       ref.name, "exit_a": ref.exitCode,
				"node_b": r.name, "exit_b": r.exitCode,
			})
	}

	// At or over depth limit — must revert
	if targetDepth >= fvmMaxCallDepth {
		for _, r := range results {
			assert.Always(r.exitCode != 0,
				"FVM call at or over depth limit must revert",
				map[string]any{"node": r.name, "depth": targetDepth, "exit": r.exitCode})
		}
	}

	debugLog("[call-depth] depth=%d, %d nodes responded, ref exit=%d", targetDepth, len(results), ref.exitCode)
}

// ===========================================================================
// P1 — Vector 5: DoSelfDestructResurrect
//
// Targets $31,250 "Missing tombstone read in EVM actor reload".
// Destroys a contract then attempts to interact with the destroyed address.
// ===========================================================================

func DoSelfDestructResurrect() {
	contracts := getContractsByType("selfdestruct")
	if len(contracts) == 0 {
		doDeployStressContract("selfdestruct")
		return
	}

	target := rngChoice(contracts)
	fromAddr, fromKI := pickWallet()
	_, node := pickNode()

	// Call destroy()
	destroyCalldata, err := cborWrapCalldata(calcSelector("destroy()"))
	if err != nil {
		return
	}

	msgCid, ok := invokeContract(node, fromAddr, fromKI, target.addr, destroyCalldata, "self-destruct")
	if !ok {
		return
	}

	// Wait for confirmation
	lookupRes, err := node.StateWaitMsg(ctx, msgCid, 2, api.LookbackNoLimit, true)
	if err != nil || lookupRes == nil {
		debugLog("[resurrect] StateWaitMsg failed: %v", err)
		return
	}

	// Now try to send value to the destroyed address
	msg := baseMsg(fromAddr, target.addr, abi.NewTokenAmount(1))
	pushMsg(node, msg, fromKI, "resurrect-value")

	// Check actor state on all nodes — destroyed contract should have no code
	for _, name := range nodeKeys {
		actor, actorErr := nodes[name].StateGetActor(ctx, target.addr, types.EmptyTSK)
		if actorErr != nil {
			// Actor not found is expected after self-destruct
			continue
		}
		if actor != nil && actor.Code != cid.Undef {
			assert.Always(false,
				"Self-destructed actor should not have code",
				map[string]any{
					"node":     name,
					"contract": target.addr.String(),
					"code_cid": actor.Code.String(),
				})
		}
	}

	debugLog("[resurrect] tested self-destruct + resurrection for %s", target.addr.String()[:16])
}

// ===========================================================================
// P1+ — Vector 6: DoMalformedActorMessage
//
// Sends messages to built-in actors with wrong methods, garbage params,
// or nil params. Uses StateCall (read-only) to exercise VM path.
// ===========================================================================

func DoMalformedActorMessage() {
	// Built-in actor address pool
	actorAddrs := []address.Address{
		builtintypes.SystemActorAddr,               // f00
		builtintypes.InitActorAddr,                 // f01
		builtintypes.RewardActorAddr,               // f02
		builtintypes.CronActorAddr,                 // f03
		builtintypes.StoragePowerActorAddr,         // f04
		builtintypes.StorageMarketActorAddr,        // f05
		builtintypes.VerifiedRegistryActorAddr,     // f06
		builtintypes.DatacapActorAddr,              // f07
		builtintypes.BurntFundsActorAddr,           // f099
	}
	targetActor := actorAddrs[rngIntn(len(actorAddrs))]

	type msgCorruptor struct {
		name  string
		apply func(m *types.Message)
	}

	pool := []msgCorruptor{
		{"wrong_method", func(m *types.Message) {
			methods := []abi.MethodNum{
				builtintypes.MethodsMiner.DeclareFaults,
				builtintypes.MethodsMarket.PublishStorageDeals,
				abi.MethodNum(rngIntn(1000) + 100),
				abi.MethodNum(math.MaxUint64),
			}
			m.Method = methods[rngIntn(len(methods))]
		}},
		{"garbage_params", func(m *types.Message) {
			sizes := []int{4, 100, 1000}
			m.Params = rngBytes(sizes[rngIntn(len(sizes))])
		}},
		{"nil_params", func(m *types.Message) {
			m.Params = nil
			// Set a method that requires params
			m.Method = builtintypes.MethodsMiner.DeclareFaults
		}},
		{"value_to_system", func(m *types.Message) {
			m.Method = 0 // Send
			m.Value = abi.NewTokenAmount(int64(rngIntn(1000) + 1))
		}},
	}

	// Apply 1-3 random corruptions
	count := rngIntn(3) + 1
	shuffled := rngShuffle(pool)
	fromAddr, _ := pickWallet()

	msg := &types.Message{
		From:       fromAddr,
		To:         targetActor,
		Value:      abi.NewTokenAmount(0),
		Method:     0,
		GasLimit:   10_000_000,
		GasFeeCap:  abi.NewTokenAmount(100_000),
		GasPremium: abi.NewTokenAmount(1_000),
	}

	var applied []string
	for i := 0; i < count && i < len(shuffled); i++ {
		shuffled[i].apply(msg)
		applied = append(applied, shuffled[i].name)
	}

	nodeName, node := pickNode()
	_, err := stateCallMsg(node, msg)
	_ = err // rejection fine; crash auto-detected by Antithesis

	debugLog("[malformed-actor] %v to %s via %s, err=%v", applied, targetActor, nodeName, err)
}

// ===========================================================================
// P1+ — Vector 7: DoGasBoundaryMessage
//
// Fuzzes gas-related message fields with extreme boundary values.
// ===========================================================================

func DoGasBoundaryMessage() {
	fromAddr, _ := pickWallet()
	toAddr, _ := pickWallet()
	if fromAddr == toAddr {
		return
	}

	type gasCorruptor struct {
		name  string
		apply func(m *types.Message)
	}

	pool := []gasCorruptor{
		{"GasLimit", func(m *types.Message) {
			extremes := []int64{0, -1, 1, math.MaxInt64, 10_000_000_000_000}
			m.GasLimit = extremes[rngIntn(len(extremes))]
		}},
		{"GasFeeCap", func(m *types.Message) {
			extremes := []*big.Int{
				big.NewInt(0), big.NewInt(-1),
				new(big.Int).SetUint64(math.MaxUint64),
				big.NewInt(1),
			}
			m.GasFeeCap = abi.TokenAmount{Int: extremes[rngIntn(len(extremes))]}
		}},
		{"GasPremium", func(m *types.Message) {
			extremes := []*big.Int{
				big.NewInt(0), big.NewInt(-1),
				new(big.Int).SetUint64(math.MaxUint64),
				new(big.Int).Add(m.GasFeeCap.Int, big.NewInt(1)), // > GasFeeCap
			}
			m.GasPremium = abi.TokenAmount{Int: extremes[rngIntn(len(extremes))]}
		}},
		{"Value", func(m *types.Message) {
			extremes := []*big.Int{
				big.NewInt(-1),
				new(big.Int).SetUint64(math.MaxUint64),
				big.NewInt(0),
			}
			m.Value = abi.TokenAmount{Int: extremes[rngIntn(len(extremes))]}
		}},
	}

	msg := baseMsg(fromAddr, toAddr, abi.NewTokenAmount(0))

	// Corrupt 1-4 fields
	count := rngIntn(len(pool)) + 1
	shuffled := rngShuffle(pool)
	var applied []string
	for i := 0; i < count; i++ {
		shuffled[i].apply(msg)
		applied = append(applied, shuffled[i].name)
	}

	nodeName, node := pickNode()

	// Try StateCall (exercises gas computation in VM)
	_, _ = stateCallMsg(node, msg)

	debugLog("[gas-boundary] %v via %s", applied, nodeName)
}

// ===========================================================================
// P1+ — Vector 8: DoOversizedParams
//
// Sends messages with extreme Params fields to stress CBOR deserialization
// and memory allocation paths.
// ===========================================================================

func DoOversizedParams() {
	fromAddr, _ := pickWallet()

	// Target actor pool
	targets := []address.Address{
		builtintypes.EthereumAddressManagerActorAddr,
		builtintypes.StorageMarketActorAddr,
		builtintypes.InitActorAddr,
	}
	targetActor := targets[rngIntn(len(targets))]

	type paramsVariant struct {
		name  string
		build func() []byte
	}

	variants := []paramsVariant{
		{"1MB_random", func() []byte {
			size := rngIntn(1<<20) + 1024 // 1KB to 1MB
			return rngBytes(size)
		}},
		{"cbor_huge_length", func() []byte {
			// CBOR byte string header claiming huge length + minimal data
			// Major type 2 (byte string), additional info 27 (8-byte length)
			header := []byte{0x5B} // major type 2, info 27
			// Claim a huge length
			lenBytes := make([]byte, 8)
			lenBytes[0] = byte(rngIntn(256))
			lenBytes[1] = byte(rngIntn(256))
			// Rest zeros — still huge
			data := append(header, lenBytes...)
			data = append(data, rngBytes(100)...) // only 100 actual bytes
			return data
		}},
		{"nested_cbor", func() []byte {
			// Deeply nested CBOR arrays: each wraps the next
			depth := rngIntn(500) + 100
			buf := make([]byte, 0, depth+1)
			for i := 0; i < depth; i++ {
				buf = append(buf, 0x81) // CBOR array of length 1
			}
			buf = append(buf, 0x00) // CBOR integer 0 at the bottom
			return buf
		}},
		{"empty_cbor", func() []byte {
			return []byte{}
		}},
	}

	variant := variants[rngIntn(len(variants))]
	params := variant.build()

	msg := &types.Message{
		From:       fromAddr,
		To:         targetActor,
		Value:      abi.NewTokenAmount(0),
		Method:     builtintypes.MethodsEAM.CreateExternal,
		Params:     params,
		GasLimit:   10_000_000,
		GasFeeCap:  abi.NewTokenAmount(100_000),
		GasPremium: abi.NewTokenAmount(1_000),
	}

	nodeName, node := pickNode()

	// Try StateCall to exercise deserialization
	_, _ = stateCallMsg(node, msg)

	debugLog("[oversized-params] %s (%d bytes) via %s", variant.name, len(params), nodeName)
}

// ===========================================================================
// P1+ — Vector 9: DoVersionFieldFuzz
//
// Fuzzes Message.Version and Message.Nonce with extreme values.
// ===========================================================================

func DoVersionFieldFuzz() {
	fromAddr, fromKI := pickWallet()
	toAddr, _ := pickWallet()
	if fromAddr == toAddr {
		return
	}

	type versionCorruptor struct {
		name  string
		apply func(m *types.Message)
	}

	pool := []versionCorruptor{
		{"Version", func(m *types.Message) {
			extremes := []uint64{1, 2, math.MaxUint64, uint64(rngIntn(1000) + 1)}
			m.Version = extremes[rngIntn(len(extremes))]
		}},
		{"Nonce", func(m *types.Message) {
			extremes := []uint64{math.MaxUint64, math.MaxUint64 - 1, 0}
			m.Nonce = extremes[rngIntn(len(extremes))]
		}},
	}

	msg := baseMsg(fromAddr, toAddr, abi.NewTokenAmount(1))
	msg.Nonce = nonces[fromAddr]

	count := rngIntn(len(pool)) + 1
	shuffled := rngShuffle(pool)
	var applied []string
	for i := 0; i < count; i++ {
		shuffled[i].apply(msg)
		applied = append(applied, shuffled[i].name)
	}

	nodeName, node := pickNode()

	// Try MpoolPush — should reject non-zero version
	smsg := signMsg(msg, fromKI)
	if smsg != nil {
		_, err := node.MpoolPush(ctx, smsg)
		_ = err // rejection expected
	}

	// Also StateCall
	_, _ = stateCallMsg(node, msg)

	debugLog("[version-fuzz] %v via %s", applied, nodeName)
}

// ===========================================================================
// P1+ — Vector 10: DoSignatureTypeMismatch
//
// Builds a valid message, signs correctly, then randomly corrupts the
// signature type and/or data. Must be rejected without crash.
// ===========================================================================

func DoSignatureTypeMismatch() {
	fromAddr, fromKI := pickWallet()
	toAddr, _ := pickWallet()
	if fromAddr == toAddr {
		return
	}

	msg := baseMsg(fromAddr, toAddr, abi.NewTokenAmount(1))
	msg.Nonce = nonces[fromAddr]

	// Sign correctly first
	smsg := signMsg(msg, fromKI)
	if smsg == nil {
		return
	}

	type sigCorruptor struct {
		name  string
		apply func(s *crypto.Signature)
	}

	pool := []sigCorruptor{
		{"type_bls", func(s *crypto.Signature) {
			s.Type = crypto.SigTypeBLS
		}},
		{"type_invalid", func(s *crypto.Signature) {
			s.Type = crypto.SigType(255)
		}},
		{"type_zero", func(s *crypto.Signature) {
			s.Type = crypto.SigType(0)
		}},
		{"data_nil", func(s *crypto.Signature) {
			s.Data = nil
		}},
		{"data_empty", func(s *crypto.Signature) {
			s.Data = []byte{}
		}},
		{"data_truncated", func(s *crypto.Signature) {
			if len(s.Data) > 1 {
				s.Data = s.Data[:1]
			}
		}},
		{"data_extended", func(s *crypto.Signature) {
			// Extend to 96 bytes (BLS sig length) with garbage
			extended := make([]byte, 96)
			copy(extended, s.Data)
			for i := len(s.Data); i < 96; i++ {
				extended[i] = byte(rngIntn(256))
			}
			s.Data = extended
		}},
		{"data_all_zeros", func(s *crypto.Signature) {
			s.Data = make([]byte, 65)
		}},
	}

	// Apply 1-3 random corruptions
	count := rngIntn(3) + 1
	shuffled := rngShuffle(pool)
	var applied []string
	for i := 0; i < count && i < len(shuffled); i++ {
		shuffled[i].apply(&smsg.Signature)
		applied = append(applied, shuffled[i].name)
	}

	nodeName, node := pickNode()
	_, err := node.MpoolPush(ctx, smsg)

	rejected := err != nil
	assert.Always(rejected,
		"Corrupted signature message was rejected",
		map[string]any{
			"node":       nodeName,
			"corruptions": applied,
			"rejected":   rejected,
			"error":      errStr(err),
		})

	debugLog("[sig-mismatch] %v via %s, rejected=%v", applied, nodeName, rejected)
}

// ===========================================================================
// P1+ — Vector 11: DoAddressConfusion
//
// Sends messages with unusual/nonexistent address types to exercise
// address resolution code paths.
// ===========================================================================

func DoAddressConfusion() {
	fromAddr, fromKI := pickWallet()

	type addrVariant struct {
		name  string
		build func() address.Address
	}

	variants := []addrVariant{
		{"f0_system", func() address.Address {
			addr, _ := address.NewIDAddress(0)
			return addr
		}},
		{"f0_huge", func() address.Address {
			addr, _ := address.NewIDAddress(uint64(1) << 62)
			return addr
		}},
		{"f2_random", func() address.Address {
			addr, _ := address.NewActorAddress(rngBytes(20))
			return addr
		}},
		{"f3_random", func() address.Address {
			addr, _ := address.NewBLSAddress(rngBytes(48))
			return addr
		}},
		{"f4_delegated_eam", func() address.Address {
			addr, _ := address.NewDelegatedAddress(10, rngBytes(20))
			return addr
		}},
		{"f4_delegated_system", func() address.Address {
			addr, _ := address.NewDelegatedAddress(0, rngBytes(20))
			return addr
		}},
		{"f4_delegated_random_ns", func() address.Address {
			ns := uint64(rngIntn(20))
			addr, _ := address.NewDelegatedAddress(ns, rngBytes(rngIntn(54)+1))
			return addr
		}},
	}

	variant := variants[rngIntn(len(variants))]
	toAddr := variant.build()
	if toAddr == address.Undef {
		return
	}

	msg := baseMsg(fromAddr, toAddr, abi.NewTokenAmount(1))

	nodeName, node := pickNode()

	// Try StateCall (exercises address resolution)
	_, _ = stateCallMsg(node, msg)

	// Sometimes also try MpoolPush
	if rngIntn(2) == 0 {
		smsg := signMsg(msg, fromKI)
		if smsg != nil {
			_, _ = node.MpoolPush(ctx, smsg)
		}
	}

	debugLog("[addr-confusion] %s → %s via %s", variant.name, toAddr, nodeName)
}

// ===========================================================================
// P1+ — Vector 12: DoMessageBatchStress
//
// Floods a single node with many valid messages in a tight loop, then
// queries MpoolPending to force serialization of the large pool.
// ===========================================================================

func DoMessageBatchStress() {
	fromAddr, fromKI := pickWallet()
	toAddr, _ := pickWallet()
	if fromAddr == toAddr {
		return
	}

	nodeName, node := pickNode()
	count := rngIntn(150) + 50 // 50-200 messages

	successCount := 0
	for i := 0; i < count; i++ {
		msg := baseMsg(fromAddr, toAddr, abi.NewTokenAmount(1))
		if pushMsg(node, msg, fromKI, "msg-flood") {
			successCount++
		}
	}

	// Force serialization of the entire mempool
	_, err := node.MpoolPending(ctx, types.EmptyTSK)
	_ = err

	assert.Sometimes(successCount > 0,
		"Mempool accepted some messages during flood",
		map[string]any{"node": nodeName, "success": successCount})

	debugLog("[msg-flood] %d/%d messages pushed to %s", successCount, count, nodeName)
}

// ===========================================================================
// P1+ — Vector 13: DoCBORLengthBomb
//
// Crafts CBOR with huge claimed lengths to trigger preallocation bugs.
// Targets $18,850 go-data-transfer DAG-CBOR preallocation class.
// ===========================================================================

func DoCBORLengthBomb() {
	fromAddr, _ := pickWallet()

	type cborVariant struct {
		name  string
		build func() []byte
	}

	variants := []cborVariant{
		{"huge_array", func() []byte {
			// CBOR array header (major type 4) claiming huge count
			// 0x9B = major type 4, additional info 27 (8-byte length)
			header := []byte{0x9B}
			length := make([]byte, 8)
			length[3] = byte(rngIntn(256)) // claim millions of elements
			length[4] = byte(rngIntn(256))
			data := append(header, length...)
			data = append(data, rngBytes(10)...) // minimal actual data
			return data
		}},
		{"huge_bytestring", func() []byte {
			// CBOR byte string (major type 2) claiming huge length
			header := []byte{0x5B}
			length := make([]byte, 8)
			length[3] = byte(rngIntn(256))
			length[4] = byte(rngIntn(256))
			data := append(header, length...)
			data = append(data, rngBytes(10)...)
			return data
		}},
		{"huge_map", func() []byte {
			// CBOR map (major type 5) claiming huge count
			header := []byte{0xBB}
			length := make([]byte, 8)
			length[5] = byte(rngIntn(256))
			length[6] = byte(rngIntn(256))
			data := append(header, length...)
			data = append(data, rngBytes(20)...)
			return data
		}},
		{"recursive_indefinite", func() []byte {
			// Indefinite-length arrays nested
			depth := rngIntn(100) + 50
			buf := make([]byte, 0, depth*2+1)
			for i := 0; i < depth; i++ {
				buf = append(buf, 0x9F) // indefinite-length array
			}
			buf = append(buf, 0x00) // integer 0
			for i := 0; i < depth; i++ {
				buf = append(buf, 0xFF) // break
			}
			return buf
		}},
	}

	variant := variants[rngIntn(len(variants))]
	params := variant.build()

	// Send to a random built-in actor
	targets := []address.Address{
		builtintypes.EthereumAddressManagerActorAddr,
		builtintypes.StorageMarketActorAddr,
		builtintypes.InitActorAddr,
	}

	msg := &types.Message{
		From:       fromAddr,
		To:         targets[rngIntn(len(targets))],
		Value:      abi.NewTokenAmount(0),
		Method:     builtintypes.MethodsEAM.CreateExternal,
		Params:     params,
		GasLimit:   10_000_000,
		GasFeeCap:  abi.NewTokenAmount(100_000),
		GasPremium: abi.NewTokenAmount(1_000),
	}

	nodeName, node := pickNode()
	_, _ = stateCallMsg(node, msg)

	debugLog("[cbor-bomb] %s (%d bytes) via %s", variant.name, len(params), nodeName)
}
