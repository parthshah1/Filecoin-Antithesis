package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"

	cborutil "github.com/filecoin-project/go-cbor-util"
	cbg "github.com/whyrusleeping/cbor-gen"
)

// ===========================================================================
// Attack Host — standalone libp2p.Host for raw protocol stream fuzzing
// ===========================================================================

var attackHost host.Host

const chainExchangeProtocolID = "/fil/chain/xchg/0.0.1"

func initAttackHost() {
	h, err := libp2p.New(libp2p.NoListenAddrs)
	if err != nil {
		log.Printf("[init] WARN: could not create attack host: %v (protocol fuzzing disabled)", err)
		return
	}
	attackHost = h
	log.Println("[init] attack host ready for protocol-layer fuzzing")
}

// ===========================================================================
// ChainExchange Request/Response — defined locally to avoid heavy transitive
// dependencies from importing lotus/chain/exchange.
// CBOR encoding matches the wire format exactly:
//   Request  = [Head: [*CID], Length: uint64, Options: uint64]
//   Response = [Status: uint64, ErrorMessage: string, Chain: [...]]
// ===========================================================================

type chainExchangeRequest struct {
	Head    []cid.Cid
	Length  uint64
	Options uint64
}

func (r *chainExchangeRequest) MarshalCBOR(w io.Writer) error {
	// Write CBOR array of 3 elements
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajArray, 3)); err != nil {
		return err
	}

	// Field 1: Head — array of CIDs
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajArray, uint64(len(r.Head)))); err != nil {
		return err
	}
	for _, c := range r.Head {
		if err := cbg.WriteCid(w, c); err != nil {
			return err
		}
	}

	// Field 2: Length — uint64
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajUnsignedInt, r.Length)); err != nil {
		return err
	}

	// Field 3: Options — uint64
	if _, err := w.Write(cbg.CborEncodeMajorType(cbg.MajUnsignedInt, r.Options)); err != nil {
		return err
	}

	return nil
}

func (r *chainExchangeRequest) UnmarshalCBOR(br io.Reader) error {
	return fmt.Errorf("not implemented — response type used instead")
}

type chainExchangeResponse struct {
	Status       uint64
	ErrorMessage string
}

func (r *chainExchangeResponse) UnmarshalCBOR(br io.Reader) error {
	cr := cbg.NewCborReader(br)

	// Read array header
	maj, extra, err := cr.ReadHeader()
	if err != nil {
		return err
	}
	if maj != cbg.MajArray || extra < 2 {
		return fmt.Errorf("expected array of at least 2, got maj=%d extra=%d", maj, extra)
	}

	// Field 1: Status
	maj, val, err := cr.ReadHeader()
	if err != nil {
		return err
	}
	if maj != cbg.MajUnsignedInt {
		return fmt.Errorf("expected uint for status, got maj=%d", maj)
	}
	r.Status = val

	// Field 2: ErrorMessage
	s, err := cbg.ReadString(cr)
	if err != nil {
		return err
	}
	r.ErrorMessage = s

	// Skip remaining fields (Chain array)
	return nil
}

// ===========================================================================
// P0: DoMalformedChainExchange — Protocol-Layer DoS ($273,750 category)
//
// Opens raw libp2p streams to target nodes on /fil/chain/xchg/0.0.1 and
// sends crafted exchange.Request messages with randomly corrupted fields.
// Antithesis auto-detects crashes; assertion confirms clean rejection.
// ===========================================================================

func DoMalformedChainExchange() {
	if attackHost == nil {
		return
	}

	targetName, targetNode := pickNode()

	// Get target's libp2p address via RPC
	addrInfo, err := targetNode.NetAddrsListen(ctx)
	if err != nil {
		debugLog("[chain-exchange] NetAddrsListen failed for %s: %v", targetName, err)
		return
	}

	// Connect to target
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()
	if err := attackHost.Connect(dialCtx, addrInfo); err != nil {
		debugLog("[chain-exchange] Connect failed for %s: %v", targetName, err)
		return
	}

	// Open raw ChainExchange protocol stream
	streamCtx, streamCancel := context.WithTimeout(ctx, 15*time.Second)
	defer streamCancel()
	stream, err := attackHost.NewStream(streamCtx, addrInfo.ID, chainExchangeProtocolID)
	if err != nil {
		debugLog("[chain-exchange] NewStream failed for %s: %v", targetName, err)
		return
	}
	defer stream.Close()

	// Build a crafted request with randomly corrupted fields
	req := buildCraftedChainExchangeRequest()

	_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := cborutil.WriteCborRPC(stream, &req); err != nil {
		debugLog("[chain-exchange] WriteCborRPC failed: %v", err)
		return
	}
	_ = stream.CloseWrite()

	// Read response — crash = stream drops, Antithesis auto-detects process exit
	var resp chainExchangeResponse
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	readErr := cborutil.ReadCborRPC(bufio.NewReader(stream), &resp)

	// Either we got a response (even error status) or a clean EOF — both ok
	rejectedCleanly := readErr == nil || isStreamEOF(readErr)

	assert.Always(rejectedCleanly,
		"ChainExchange server handles malformed request without crashing",
		map[string]any{
			"node":     targetName,
			"head_len": len(req.Head),
			"length":   req.Length,
			"options":  req.Options,
		})

	debugLog("[chain-exchange] sent crafted request to %s, clean=%v", targetName, rejectedCleanly)
}

// buildCraftedChainExchangeRequest constructs a chainExchangeRequest with
// a random number of fields corrupted using Antithesis RNG.
func buildCraftedChainExchangeRequest() chainExchangeRequest {
	req := chainExchangeRequest{
		Head:    []cid.Cid{cid.Undef},
		Length:  1,
		Options: 1, // Headers only
	}

	// Field corruption pool
	type fieldFn struct {
		name  string
		apply func(r *chainExchangeRequest)
	}

	pool := []fieldFn{
		{"head", func(r *chainExchangeRequest) {
			variants := [][]cid.Cid{
				{cid.Undef},
				{},
				nil,
				{cid.Undef, cid.Undef},
			}
			r.Head = variants[rngIntn(len(variants))]
		}},
		{"length", func(r *chainExchangeRequest) {
			variants := []uint64{0, ^uint64(0), 1, 999999999, ^uint64(0) - 1}
			r.Length = variants[rngIntn(len(variants))]
		}},
		{"options", func(r *chainExchangeRequest) {
			variants := []uint64{0, 3, 255, ^uint64(0), 1 << 63}
			r.Options = variants[rngIntn(len(variants))]
		}},
	}

	// Randomly pick how many fields to corrupt (1 to all)
	count := rngIntn(len(pool)) + 1
	// Fisher-Yates shuffle
	shuffled := make([]fieldFn, len(pool))
	copy(shuffled, pool)
	for i := len(shuffled) - 1; i > 0; i-- {
		j := rngIntn(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	for i := 0; i < count; i++ {
		shuffled[i].apply(&req)
	}

	return req
}

// isStreamEOF returns true if the error is an expected stream close.
func isStreamEOF(err error) bool {
	if err == nil {
		return false
	}
	return err == io.EOF || err.Error() == "stream reset" || err.Error() == "connection reset"
}
