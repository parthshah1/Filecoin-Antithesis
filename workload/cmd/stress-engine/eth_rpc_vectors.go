package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"workload/internal/chain"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// ===========================================================================
// RLP Encoding Helpers (minimal, for crafting malformed payloads)
//
// We intentionally do NOT use lotus's ethtypes.EncodeRLP — we need to
// produce invalid data that the real encoder would refuse to create.
// ===========================================================================

const rpcTimeout = 30 * time.Second

// rlpEncodeBytes encodes a byte slice as an RLP string.
func rlpEncodeBytes(b []byte) []byte {
	if len(b) == 1 && b[0] <= 0x7f {
		return b
	}
	if len(b) <= 55 {
		return append([]byte{byte(0x80 + len(b))}, b...)
	}
	lenBytes := encodeVarInt(len(b))
	return append(append([]byte{byte(0xb7 + len(lenBytes))}, lenBytes...), b...)
}

// rlpEncodeList wraps concatenated items in an RLP list header.
func rlpEncodeList(items ...[]byte) []byte {
	var payload []byte
	for _, item := range items {
		payload = append(payload, item...)
	}
	if len(payload) <= 55 {
		return append([]byte{byte(0xc0 + len(payload))}, payload...)
	}
	lenBytes := encodeVarInt(len(payload))
	return append(append([]byte{byte(0xf7 + len(lenBytes))}, lenBytes...), payload...)
}

// encodeVarInt encodes an integer as big-endian bytes with no leading zeros.
func encodeVarInt(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte(n & 0xff)}, buf...)
		n >>= 8
	}
	return buf
}

// buildMinimalEip1559RLP builds a 12-element EIP-1559 RLP payload prefixed with 0x02.
// overrides replaces specific field indices (0-11) with custom RLP-encoded values.
func buildMinimalEip1559RLP(overrides map[int][]byte) []byte {
	// Fields: chainID, nonce, maxPriorityFeePerGas, maxFeePerGas, gasLimit, to, value, data, accessList, V, R, S
	defaults := [][]byte{
		rlpEncodeBytes([]byte{0x01}),       // 0: chainID = 1
		rlpEncodeBytes([]byte{}),           // 1: nonce = 0
		rlpEncodeBytes([]byte{0x01}),       // 2: maxPriorityFeePerGas = 1
		rlpEncodeBytes([]byte{0x64}),       // 3: maxFeePerGas = 100
		rlpEncodeBytes([]byte{0x52, 0x08}), // 4: gasLimit = 21000
		rlpEncodeBytes(make([]byte, 20)),   // 5: to = zero address
		rlpEncodeBytes([]byte{}),           // 6: value = 0
		rlpEncodeBytes([]byte{}),           // 7: data = empty
		rlpEncodeList(),                    // 8: accessList = []
		rlpEncodeBytes([]byte{}),           // 9: V = 0
		rlpEncodeBytes(make([]byte, 32)),   // 10: R = 32 zeros
		rlpEncodeBytes(make([]byte, 32)),   // 11: S = 32 zeros
	}
	for idx, val := range overrides {
		if idx >= 0 && idx < len(defaults) {
			defaults[idx] = val
		}
	}
	list := rlpEncodeList(defaults...)
	return append([]byte{0x02}, list...) // EIP-1559 type prefix
}

// buildMinimalLegacyRLP builds a 9-element legacy tx RLP (first byte > 0x7f).
// overrides replaces specific field indices (0-8) with custom RLP-encoded values.
func buildMinimalLegacyRLP(overrides map[int][]byte) []byte {
	// Fields: nonce, gasPrice, gasLimit, to, value, data, V, R, S
	defaults := [][]byte{
		rlpEncodeBytes([]byte{}),           // 0: nonce = 0
		rlpEncodeBytes([]byte{0x64}),       // 1: gasPrice = 100
		rlpEncodeBytes([]byte{0x52, 0x08}), // 2: gasLimit = 21000
		rlpEncodeBytes(make([]byte, 20)),   // 3: to = zero address
		rlpEncodeBytes([]byte{}),           // 4: value = 0
		rlpEncodeBytes([]byte{}),           // 5: data = empty
		rlpEncodeBytes([]byte{0x1b}),       // 6: V = 27
		rlpEncodeBytes(make([]byte, 32)),   // 7: R = 32 zeros
		rlpEncodeBytes(make([]byte, 32)),   // 8: S = 32 zeros
	}
	for idx, val := range overrides {
		if idx >= 0 && idx < len(defaults) {
			defaults[idx] = val
		}
	}
	return rlpEncodeList(defaults...)
}

// pickNodeURL returns a random node name and its raw HTTP URL.
func pickNodeURL() (string, string) {
	name := rngChoice(nodeKeys)
	return name, nodeURLs[name]
}

// sendRawTx sends a hex-encoded raw transaction via eth_sendRawTransaction and
// returns whether the node rejected it (error response or RPC error).
func sendRawTx(url string, hexPayload string) (rejected bool, errMsg string) {
	params, _ := json.Marshal([]string{hexPayload})
	result, _, err := chain.RawRPC(url, "eth_sendRawTransaction", params, rpcTimeout)
	if err != nil {
		return true, fmt.Sprintf("transport: %v", err)
	}
	if result.Error != nil {
		return true, result.Error.Message
	}
	return false, ""
}

// ===========================================================================
// DoEthRLPFuzz — RLP Parsing Attack Surface
//
// Sends malformed payloads via raw HTTP eth_sendRawTransaction.
// 14 sub-actions covering structural attacks, non-canonical encoding,
// transaction-specific attacks, and hex/transport-layer attacks.
// ===========================================================================

func DoEthRLPFuzz() {
	subActions := []struct {
		name string
		fn   func(string)
	}{
		// Structural attacks
		{"truncated-payload", doRLPTruncatedPayload},
		{"oversized-length", doRLPOversizedLength},
		{"nested-list-bomb", doRLPNestedListBomb},
		{"integer-overflow-length", doRLPIntegerOverflowLength},
		// Non-canonical encoding (consensus-split class)
		{"non-canonical-single-byte", doRLPNonCanonicalSingleByte},
		{"non-canonical-leading-zeros", doRLPNonCanonicalLeadingZeros},
		{"non-canonical-integer-zeros", doRLPNonCanonicalIntegerZeros},
		// Transaction-specific
		{"bad-tx-type", doRLPBadTxType},
		{"legacy-bad-element-count", doRLPLegacyBadElementCount},
		{"eip1559-bad-element-count", doRLPEip1559BadElementCount},
		{"bad-signature-v", doRLPBadSignatureV},
		{"type-confusion", doRLPTypeConfusion},
		// Hex/transport layer
		{"invalid-hex", doRLPInvalidHex},
		{"huge-hex-param", doRLPHugeHexParam},
	}

	idx := rngIntn(len(subActions))
	sub := subActions[idx]
	_, url := pickNodeURL()

	debugLog("[rlp-fuzz] sub-action: %s", sub.name)
	sub.fn(url)

	assert.Sometimes(true, "EthRLP fuzz vector executed", map[string]any{
		"sub_action": sub.name,
	})
}

// --- Structural Attacks ---

func doRLPTruncatedPayload(url string) {
	payload := buildMinimalEip1559RLP(nil)
	// Truncate at a random point (at least 2 bytes for type prefix + something)
	truncLen := rngIntn(len(payload)-2) + 2
	truncated := payload[:truncLen]

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(truncated))
	assert.Always(rejected, "Truncated RLP rejected", map[string]any{
		"payload_len":  len(truncated),
		"original_len": len(payload),
		"error":        errMsg,
	})
}

func doRLPOversizedLength(url string) {
	// 0x02 (EIP-1559) + 0xbf (string, 8-byte length follows) + 0xFFFFFFFF as 4 bytes
	// Claims a 4GB string follows, but only a few bytes are present
	payload := []byte{0x02, 0xbb, 0xff, 0xff, 0xff, 0xff, 0x01, 0x02, 0x03}

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Oversized RLP length rejected", map[string]any{
		"error": errMsg,
	})
}

func doRLPNestedListBomb(url string) {
	// Build deeply nested empty lists: c1 c1 c1 ... c0
	// Each 0xc1 = list of length 1 (containing the next element)
	// Final 0xc0 = empty list
	depth := rngIntn(40000) + 10000  // 10K-50K depth
	payload := make([]byte, depth+2) // +1 for 0x02 prefix, +1 for final 0xc0
	payload[0] = 0x02                // EIP-1559 type prefix
	for i := 1; i < depth+1; i++ {
		payload[i] = 0xc1
	}
	payload[depth+1] = 0xc0

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Nested list bomb rejected", map[string]any{
		"depth": depth,
		"error": errMsg,
	})
}

func doRLPIntegerOverflowLength(url string) {
	// 0x02 + 0xbf (string with 8-byte length) + 8 bytes that would overflow int
	// 0x7fffffffffffffff = max int64, offset+len wraps on 32-bit or panics
	payload := []byte{0x02, 0xbf, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Integer overflow length rejected", map[string]any{
		"error": errMsg,
	})
}

// --- Non-Canonical Encoding (consensus-split class) ---

func doRLPNonCanonicalSingleByte(url string) {
	// Byte 0x05 should be encoded as itself (0x05), not as a 1-byte string (0x81 0x05).
	// Strict decoders reject 0x8105, lenient ones accept — caused 2020 chain split.
	payload := buildMinimalEip1559RLP(map[int][]byte{
		1: {0x81, 0x05}, // nonce = 5, non-canonically encoded
	})

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Non-canonical single byte rejected", map[string]any{
		"error": errMsg,
	})
}

func doRLPNonCanonicalLeadingZeros(url string) {
	// Length 1 encoded with 2-byte length prefix: 0xb9 0x00 0x01 <1 byte>
	// Should use 0x81 <1 byte> instead. Non-canonical length prefix.
	payload := buildMinimalEip1559RLP(map[int][]byte{
		1: {0xb9, 0x00, 0x01, 0x05}, // nonce with non-canonical 2-byte length for 1-byte value
	})

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Non-canonical leading zeros in length rejected", map[string]any{
		"error": errMsg,
	})
}

func doRLPNonCanonicalIntegerZeros(url string) {
	// Integer 0xFF with leading zero byte: 0x83 0x00 0x00 0xFF
	// Should be 0x81 0xFF. Leading zeros in integers are non-canonical.
	payload := buildMinimalEip1559RLP(map[int][]byte{
		1: {0x83, 0x00, 0x00, 0xff}, // nonce = 255 with 2 leading zero bytes
	})

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Non-canonical integer zeros rejected", map[string]any{
		"error": errMsg,
	})
}

// --- Transaction-Specific Attacks ---

func doRLPBadTxType(url string) {
	// Try unsupported/invalid transaction type prefixes
	badTypes := []byte{0x01, 0x03, 0x04, 0x00, byte(rngIntn(0x7c) + 0x04)}
	chosen := badTypes[rngIntn(len(badTypes))]

	// Build a minimal payload after the type byte
	inner := buildMinimalEip1559RLP(nil)
	payload := append([]byte{chosen}, inner[1:]...) // replace the 0x02 prefix

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Bad transaction type rejected", map[string]any{
		"type_byte": fmt.Sprintf("0x%02x", chosen),
		"error":     errMsg,
	})
}

func doRLPLegacyBadElementCount(url string) {
	// Legacy tx needs exactly 9 elements. Try 8 and 10.
	base := buildMinimalLegacyRLP(nil)
	_ = base // use the helpers to build wrong-count lists

	// 8 elements (missing S field)
	fields8 := make([][]byte, 8)
	for i := range fields8 {
		fields8[i] = rlpEncodeBytes([]byte{byte(i + 1)})
	}
	payload8 := rlpEncodeList(fields8...)

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload8))
	assert.Always(rejected, "Legacy tx with 8 elements rejected", map[string]any{
		"count": 8,
		"error": errMsg,
	})

	// 10 elements (extra field)
	fields10 := make([][]byte, 10)
	for i := range fields10 {
		fields10[i] = rlpEncodeBytes([]byte{byte(i + 1)})
	}
	payload10 := rlpEncodeList(fields10...)

	rejected, errMsg = sendRawTx(url, "0x"+hex.EncodeToString(payload10))
	assert.Always(rejected, "Legacy tx with 10 elements rejected", map[string]any{
		"count": 10,
		"error": errMsg,
	})
}

func doRLPEip1559BadElementCount(url string) {
	// EIP-1559 needs exactly 12 elements. Try 11 and 13.
	// 11 elements
	fields11 := make([][]byte, 11)
	for i := range fields11 {
		fields11[i] = rlpEncodeBytes([]byte{byte(i)})
	}
	payload11 := append([]byte{0x02}, rlpEncodeList(fields11...)...)

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload11))
	assert.Always(rejected, "EIP-1559 tx with 11 elements rejected", map[string]any{
		"count": 11,
		"error": errMsg,
	})

	// 13 elements
	fields13 := make([][]byte, 13)
	for i := range fields13 {
		fields13[i] = rlpEncodeBytes([]byte{byte(i)})
	}
	payload13 := append([]byte{0x02}, rlpEncodeList(fields13...)...)

	rejected, errMsg = sendRawTx(url, "0x"+hex.EncodeToString(payload13))
	assert.Always(rejected, "EIP-1559 tx with 13 elements rejected", map[string]any{
		"count": 13,
		"error": errMsg,
	})
}

func doRLPBadSignatureV(url string) {
	// EIP-1559 V must be 0 or 1. Try 2 and 255.
	for _, v := range []byte{0x02, 0xff} {
		payload := buildMinimalEip1559RLP(map[int][]byte{
			9: rlpEncodeBytes([]byte{v}), // V field
		})

		rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
		assert.Always(rejected, "Bad signature V rejected", map[string]any{
			"v_value": v,
			"error":   errMsg,
		})
	}
}

func doRLPTypeConfusion(url string) {
	// Replace a byte-string field (nonce) with a nested list.
	// parseBigInt / parseInt does v.([]byte) — a list would fail the type assertion.
	nestedList := rlpEncodeList(rlpEncodeBytes([]byte{0x01}))
	payload := buildMinimalEip1559RLP(map[int][]byte{
		1: nestedList, // nonce field is now a list, not a byte string
	})

	rejected, errMsg := sendRawTx(url, "0x"+hex.EncodeToString(payload))
	assert.Always(rejected, "Type confusion (list as byte-string) rejected", map[string]any{
		"error": errMsg,
	})
}

// --- Hex/Transport-Layer Attacks ---

func doRLPInvalidHex(url string) {
	badHexCases := []string{
		"0xZZZZ",                          // invalid hex chars
		"0xabc",                           // odd-length hex
		"deadbeef",                        // missing 0x prefix
		"",                                // empty string
		"0x",                              // just prefix, no data
		"0x0",                             // odd-length, single nibble
		"0x " + strings.Repeat("41", 100), // space in hex
	}

	chosen := badHexCases[rngIntn(len(badHexCases))]
	rejected, errMsg := sendRawTx(url, chosen)

	assert.Always(rejected, "Invalid hex rejected by EthSendRawTransaction", map[string]any{
		"hex_input": chosen,
		"error":     errMsg,
	})
}

func doRLPHugeHexParam(url string) {
	// 1MB-10MB hex string of zeros — OOM in hex decode / allocation
	sizeBytes := rngIntn(9*1024*1024) + 1*1024*1024 // 1-10MB
	hugeHex := "0x" + strings.Repeat("00", sizeBytes)

	rejected, errMsg := sendRawTx(url, hugeHex)
	// May be rejected or may timeout — both are acceptable
	debugLog("[rlp-fuzz] huge hex (%d bytes): rejected=%v err=%s", sizeBytes, rejected, errMsg)

	assert.Sometimes(true, "Huge hex param fuzz executed", map[string]any{
		"size_bytes": sizeBytes,
		"rejected":   rejected,
	})
}

// ===========================================================================
// DoEthRPCStress — Public RPC Attack Surface (raw HTTP)
//
// Calls Eth RPC methods with adversarial parameters via raw HTTP POST.
// Tests param parsing, lookback limits, large responses, malformed JSON.
// ===========================================================================

func DoEthRPCStress() {
	subActions := []struct {
		name string
		fn   func()
	}{
		{"eth-getBlockByNumber-edge", doRawEthGetBlockByNumberEdge},
		{"eth-feeHistory-stress", doRawEthFeeHistoryStress},
		{"eth-getLogs-wide-range", doRawEthGetLogsWideRange},
		{"eth-estimateGas-adversarial", doRawEthEstimateGasAdversarial},
		{"eth-getBlockReceipts-stress", doRawEthGetBlockReceiptsStress},
		{"eth-call-edge-cases", doRawEthCallEdgeCases},
		{"malformed-json", doRawMalformedJSON},
	}

	idx := rngIntn(len(subActions))
	sub := subActions[idx]

	debugLog("[eth-rpc] sub-action: %s", sub.name)
	sub.fn()

	assert.Sometimes(true, "Eth RPC stress vector executed", map[string]any{
		"sub_action": sub.name,
	})
}

func doRawEthGetBlockByNumberEdge() {
	name, url := pickNodeURL()

	cases := []json.RawMessage{
		json.RawMessage(`["0x0", true]`),
		json.RawMessage(`["earliest", true]`),
		json.RawMessage(`["0xFFFFFFFF", true]`),
		json.RawMessage(`["not_a_number", true]`),
		json.RawMessage(`["pending", false]`),
		json.RawMessage(`["latest", true]`),
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_getBlockByNumber", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_getBlockByNumber transport error on %s: %v", name, err)
		return
	}

	debugLog("[eth-rpc] eth_getBlockByNumber(%s) on %s: hasResult=%v hasError=%v",
		string(chosen), name, len(result.Result) > 0, result.Error != nil)

	// Cross-node: spot-check genesis block hash matches
	if string(chosen) == `["0x0", true]` && len(nodeKeys) >= 2 {
		_, url2 := nodeKeys[1], nodeURLs[nodeKeys[1]]
		result2, _, err2 := chain.RawRPC(url2, "eth_getBlockByNumber", chosen, rpcTimeout)
		if err2 == nil && result.Error == nil && result2.Error == nil {
			match := string(result.Result) == string(result2.Result)
			assert.Always(match, "Genesis block matches across nodes via Eth RPC", map[string]any{
				"node_a": name,
				"node_b": nodeKeys[1],
			})
		}
	}
}

func doRawEthFeeHistoryStress() {
	_, url := pickNodeURL()

	cases := []json.RawMessage{
		json.RawMessage(`[1, "latest", [25, 75]]`),   // normal
		json.RawMessage(`[128, "latest", []]`),       // moderate
		json.RawMessage(`[1024, "latest", []]`),      // large
		json.RawMessage(`[10000, "latest", []]`),     // huge — may OOM
		json.RawMessage(`[0, "latest", []]`),         // zero
		json.RawMessage(`["garbage", "latest", []]`), // bad type
		json.RawMessage(`[-1, "latest", []]`),        // negative
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_feeHistory", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_feeHistory transport error: %v", err)
		return
	}
	debugLog("[eth-rpc] eth_feeHistory(%s): hasResult=%v hasError=%v",
		string(chosen), len(result.Result) > 0, result.Error != nil)
}

func doRawEthGetLogsWideRange() {
	_, url := pickNodeURL()

	cases := []json.RawMessage{
		// Full chain scan
		json.RawMessage(`[{"fromBlock":"0x0","toBlock":"latest"}]`),
		// Huge range
		json.RawMessage(`[{"fromBlock":"0x0","toBlock":"0xFFFFFFFF"}]`),
		// Reasonable range from recent blocks
		json.RawMessage(`[{"fromBlock":"0x1","toBlock":"0x100"}]`),
		// Missing fields
		json.RawMessage(`[{}]`),
		// Bad types
		json.RawMessage(`[{"fromBlock":123,"toBlock":"latest"}]`),
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_getLogs", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_getLogs transport error: %v", err)
		return
	}
	debugLog("[eth-rpc] eth_getLogs(%s): hasResult=%v hasError=%v",
		string(chosen), len(result.Result) > 0, result.Error != nil)
}

func doRawEthEstimateGasAdversarial() {
	_, url := pickNodeURL()

	// 64KB of zeros as data
	hugeData := "0x" + strings.Repeat("00", 64*1024)
	zeroAddr := "0x0000000000000000000000000000000000000000"

	cases := []json.RawMessage{
		// Huge data field
		json.RawMessage(fmt.Sprintf(`[{"to":"%s","data":"%s"},"latest"]`, zeroAddr, hugeData)),
		// Not valid hex
		json.RawMessage(fmt.Sprintf(`[{"to":"%s","data":"not_hex"},"latest"]`, zeroAddr)),
		// Missing to (contract creation)
		json.RawMessage(`[{"data":"0x60806040"},"latest"]`),
		// Absurd gas value
		json.RawMessage(fmt.Sprintf(`[{"to":"%s","gas":"0xFFFFFFFFFFFFFFFF"},"latest"]`, zeroAddr)),
		// Empty object
		json.RawMessage(`[{},"latest"]`),
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_estimateGas", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_estimateGas transport error: %v", err)
		return
	}
	debugLog("[eth-rpc] eth_estimateGas: hasResult=%v hasError=%v",
		len(result.Result) > 0, result.Error != nil)
}

func doRawEthGetBlockReceiptsStress() {
	name, url := pickNodeURL()

	cases := []json.RawMessage{
		json.RawMessage(`["0x0"]`),      // genesis
		json.RawMessage(`["latest"]`),   // current head
		json.RawMessage(`["earliest"]`), // earliest alias
		json.RawMessage(`["pending"]`),  // pending
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_getBlockReceipts", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_getBlockReceipts transport error on %s: %v", name, err)
		return
	}

	debugLog("[eth-rpc] eth_getBlockReceipts(%s) on %s: hasResult=%v hasError=%v",
		string(chosen), name, len(result.Result) > 0, result.Error != nil)

	// Cross-node comparison for latest block receipts
	if string(chosen) == `["latest"]` && len(nodeKeys) >= 2 {
		name2 := nodeKeys[1]
		url2 := nodeURLs[name2]
		result2, _, err2 := chain.RawRPC(url2, "eth_getBlockReceipts", chosen, rpcTimeout)
		if err2 == nil && result.Error == nil && result2.Error == nil {
			// Compare result lengths (exact content may differ if heads differ)
			debugLog("[eth-rpc] cross-node receipts: %s=%d bytes, %s=%d bytes",
				name, len(result.Result), name2, len(result2.Result))
		}
	}
}

func doRawEthCallEdgeCases() {
	_, url := pickNodeURL()

	zeroAddr := "0x0000000000000000000000000000000000000000"
	deadAddr := "0x000000000000000000000000000000000000dEaD"

	cases := []json.RawMessage{
		// No 'to' field — contract creation simulation
		json.RawMessage(`[{"data":"0x60806040"},"latest"]`),
		// Non-existent address
		json.RawMessage(fmt.Sprintf(`[{"to":"%s","data":"0x"},"latest"]`, deadAddr)),
		// Huge value exceeding any balance
		json.RawMessage(fmt.Sprintf(`[{"to":"%s","value":"0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"},"latest"]`, zeroAddr)),
		// Empty data to known address
		json.RawMessage(fmt.Sprintf(`[{"to":"%s"},"latest"]`, zeroAddr)),
		// Missing all fields
		json.RawMessage(`[{},"latest"]`),
		// Bad block param
		json.RawMessage(fmt.Sprintf(`[{"to":"%s"},"not_a_block"]`, zeroAddr)),
	}

	chosen := cases[rngIntn(len(cases))]
	result, _, err := chain.RawRPC(url, "eth_call", chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] eth_call transport error: %v", err)
		return
	}
	debugLog("[eth-rpc] eth_call: hasResult=%v hasError=%v",
		len(result.Result) > 0, result.Error != nil)
}

func doRawMalformedJSON() {
	_, url := pickNodeURL()

	cases := [][]byte{
		// Truncated JSON
		[]byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","pa`),
		// Missing method
		[]byte(`{"jsonrpc":"2.0","params":[],"id":1}`),
		// Null method
		[]byte(`{"jsonrpc":"2.0","method":null,"params":[],"id":1}`),
		// Params as string instead of array
		[]byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":"[]","id":1}`),
		// Empty body
		[]byte(``),
		// Not JSON at all
		[]byte(`this is not json`),
		// Array instead of object
		[]byte(`[1,2,3]`),
		// Very large ID
		[]byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":999999999999999}`),
		// Method as number
		[]byte(`{"jsonrpc":"2.0","method":123,"params":[],"id":1}`),
		// Duplicate keys
		[]byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","method":"eth_chainId","params":[],"id":1}`),
	}

	chosen := cases[rngIntn(len(cases))]
	respBody, status, err := chain.RawRPCBytes(url, chosen, rpcTimeout)
	if err != nil {
		debugLog("[eth-rpc] malformed JSON transport error: %v", err)
		return
	}

	debugLog("[eth-rpc] malformed JSON: status=%d body_len=%d", status, len(respBody))
}
