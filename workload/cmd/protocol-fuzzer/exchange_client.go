package main

import (
	"context"
	"io"
	"log"
	"math"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const exchangeProtocol = "/fil/chain/xchg/0.0.1"

// openExchangeStream connects to the target and opens a ChainExchange stream.
func openExchangeStream(ctx context.Context, h host.Host, target peer.AddrInfo) (network.Stream, error) {
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := h.Connect(connectCtx, target); err != nil {
		return nil, err
	}

	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()

	return h.NewStream(streamCtx, target.ID, exchangeProtocol)
}

// buildExchangeRequest builds a valid ChainExchange request as CBOR:
// Request = [Head []CID, Length uint64, Options uint64]
func buildExchangeRequest(head []cid.Cid, length uint64, options uint64) []byte {
	return cborArray(
		cborCIDArray(head),
		cborUint64(length),
		cborUint64(options),
	)
}

// getAllExchangeClientAttacks returns all 16 ChainExchange client attack vectors.
func getAllExchangeClientAttacks() []namedAttack {
	attacks := []struct {
		name string
		fn   func(context.Context, host.Host, peer.AddrInfo)
	}{
		{"exch-empty-head", exchEmptyHead},
		{"exch-huge-head", exchHugeHead},
		{"exch-zero-length", exchZeroLength},
		{"exch-max-length", exchMaxLength},
		{"exch-zero-options", exchZeroOptions},
		{"exch-bad-options", exchBadOptions},
		{"exch-truncated-cbor", exchTruncatedCBOR},
		{"exch-oversized-cbor", exchOversizedCBOR},
		{"exch-wrong-cbor-type", exchWrongCBORType},
		{"exch-slow-read", exchSlowRead},
		{"exch-stream-burst", exchStreamBurst},
		{"exch-half-read", exchHalfRead},
		{"exch-dup-cids", exchDupCIDs},
		{"exch-max-req-len", exchMaxReqLen},
		{"exch-over-max-len", exchOverMaxLen},
		{"exch-hang-no-close", exchHangNoClose},
	}

	result := make([]namedAttack, len(attacks))
	for i, a := range attacks {
		a := a // capture
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

// N1: Empty Head array
func exchEmptyHead(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-empty-head] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest(nil, 1, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N2: Huge Head (100 random CIDs)
func exchHugeHead(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-huge-head] stream open failed: %v", err)
		return
	}
	defer s.Close()

	cids := make([]cid.Cid, 100)
	for i := range cids {
		cids[i] = randomCID()
	}

	payload := buildExchangeRequest(cids, 1, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N3: Zero Length
func exchZeroLength(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-zero-length] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 0, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N4: MaxUint64 Length
func exchMaxLength(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-max-length] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, math.MaxUint64, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N5: Zero Options
func exchZeroOptions(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-zero-options] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 0)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N6: Bad Options (0xDEAD)
func exchBadOptions(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-bad-options] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 0xDEAD)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N7: Truncated CBOR (first half of a valid request)
func exchTruncatedCBOR(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-truncated-cbor] stream open failed: %v", err)
		return
	}
	defer s.Close()

	valid := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 1)
	s.Write(valid[:len(valid)/2])
	s.CloseWrite()
	readResponse(s)
}

// N8: Oversized CBOR (array header claiming 100M elements + 1KB junk)
func exchOversizedCBOR(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-oversized-cbor] stream open failed: %v", err)
		return
	}
	defer s.Close()

	// CBOR array header claiming 100 million elements
	var buf []byte
	buf = append(buf, 0x9b) // array (major type 4), 8-byte length follows
	v := uint64(100_000_000)
	for i := 7; i >= 0; i-- {
		buf = append(buf, byte(v>>(uint(i)*8)))
	}
	// Append 1KB of junk
	buf = append(buf, randomBytes(1024)...)
	s.Write(buf)
	s.CloseWrite()
	readResponse(s)
}

// N9: Wrong CBOR type (text string instead of array)
func exchWrongCBORType(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-wrong-cbor-type] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := cborTextString("hello")
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N10: Slow read - valid request, read response 1 byte/sec for 65s
func exchSlowRead(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-slow-read] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 1)
	s.Write(payload)
	s.CloseWrite()

	// Read 1 byte at a time with 1s delays
	buf := make([]byte, 1)
	for i := 0; i < 65; i++ {
		_, err := s.Read(buf)
		if err != nil {
			break
		}
		time.Sleep(time.Second)
	}
}

// N11: Stream burst - 50 concurrent streams with valid requests
func exchStreamBurst(ctx context.Context, h host.Host, target peer.AddrInfo) {
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := openExchangeStream(ctx, h, target)
			if err != nil {
				return
			}
			defer s.Close()

			payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 1)
			s.Write(payload)
			s.CloseWrite()
			readResponse(s)
		}()
	}

	wg.Wait()
}

// N12: Half read - valid request, read 10 bytes then reset
func exchHalfRead(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-half-read] stream open failed: %v", err)
		return
	}

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 1)
	s.Write(payload)
	s.CloseWrite()

	// Read only 10 bytes then abruptly reset
	buf := make([]byte, 10)
	s.Read(buf)
	s.Reset()
}

// N13: Duplicate CIDs in Head
func exchDupCIDs(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-dup-cids] stream open failed: %v", err)
		return
	}
	defer s.Close()

	sameCID := randomCID()
	payload := buildExchangeRequest([]cid.Cid{sameCID, sameCID}, 1, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N14: Max request length (900 - at boundary)
func exchMaxReqLen(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-max-req-len] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 900, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N15: Over max length (901 - just over boundary)
func exchOverMaxLen(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-over-max-len] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 901, 1)
	s.Write(payload)
	s.CloseWrite()
	readResponse(s)
}

// N16: Hang - never close write side, wait 15s
func exchHangNoClose(ctx context.Context, h host.Host, target peer.AddrInfo) {
	s, err := openExchangeStream(ctx, h, target)
	if err != nil {
		debugLog("[exch-hang-no-close] stream open failed: %v", err)
		return
	}
	defer s.Close()

	payload := buildExchangeRequest([]cid.Cid{randomCID()}, 1, 1)
	s.Write(payload)
	// Intentionally do NOT close write side
	time.Sleep(15 * time.Second)
}

// readResponse reads up to 64KB from the stream, discarding the data.
func readResponse(s network.Stream) {
	s.SetReadDeadline(time.Now().Add(10 * time.Second))
	io.Copy(io.Discard, io.LimitReader(s, 64*1024))
}
