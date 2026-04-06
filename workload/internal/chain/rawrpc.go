package chain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RawRPCResult holds a parsed JSON-RPC response.
type RawRPCResult struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
	ID     int             `json:"id"`
}

// RPCError represents a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RawRPC sends a JSON-RPC request over HTTP POST and returns the parsed response.
// The params are sent as-is (caller controls the exact JSON).
// Returns (result, httpStatus, error). Error is only for transport failures;
// an RPC-level error is in result.Error.
func RawRPC(url, method string, params json.RawMessage, timeout time.Duration) (*RawRPCResult, int, error) {
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}
	return doPost(url, body, timeout)
}

// RawRPCBytes sends completely raw bytes as the POST body.
// Use this for malformed JSON tests where RawRPC's envelope construction
// would fix the very thing we want to break.
func RawRPCBytes(url string, body []byte, timeout time.Duration) ([]byte, int, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	// Cap read to 1MB to avoid OOM from pathological responses
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// NodeHTTPURL returns the HTTP RPC URL for a node.
func NodeHTTPURL(name, port string) string {
	return fmt.Sprintf("http://%s:%s/rpc/v1", name, port)
}

func doPost(url string, body []byte, timeout time.Duration) (*RawRPCResult, int, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	var result RawRPCResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Response wasn't valid JSON-RPC — return raw body in Result field
		return &RawRPCResult{Result: respBody}, resp.StatusCode, nil
	}
	return &result, resp.StatusCode, nil
}
