package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// request is one inbound JSON-RPC 2.0 message: a request when ID is set, a
// notification otherwise.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is a successful reply; Result is always serialized, null
// included, as the protocol requires.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// errorResponse is a failed reply.
type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   respError       `json:"error"`
}

// respError is the error member of a failed reply.
type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// The JSON-RPC error codes the server emits.
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// notification is one outbound server-to-client notification.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// readMessage reads one Content-Length-framed JSON-RPC message.
func readMessage(r *bufio.Reader) (*request, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			length, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("parsing Content-Length: %w", err)
			}
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("reading %d-byte body: %w", length, err)
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("decoding message: %w", err)
	}
	return &req, nil
}

// writeMessage writes one Content-Length-framed JSON-RPC message.
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("writing body: %w", err)
	}
	return nil
}
