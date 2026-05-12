package util

import (
	"fmt"
	"io"
)

const (
	// ReadLimitAuth is the max bytes for OAuth/token/OIDC HTTP responses.
	ReadLimitAuth int64 = 1 * 1024 * 1024 // 1 MB

	// ReadLimitAPI is the max bytes for non-streaming LLM API responses.
	ReadLimitAPI int64 = 50 * 1024 * 1024 // 50 MB

	// ReadLimitMCP is the max bytes for MCP JSON-RPC messages.
	ReadLimitMCP int64 = 100 * 1024 * 1024 // 100 MB

	// ReadLimitWebSocket is the max bytes for WebSocket messages.
	ReadLimitWebSocket int64 = 10 * 1024 * 1024 // 10 MB

	// ReadLimitGeneral is the default max bytes for generic HTTP responses.
	ReadLimitGeneral int64 = 10 * 1024 * 1024 // 10 MB
)

// ReadAll reads from r with a size limit. Returns the data and an error if
// the reader exceeds limit. This prevents unbounded memory allocation from
// malicious or broken HTTP responses.
//
// Usage:
//
//	data, err := util.ReadAll(resp.Body, util.ReadLimitAuth)
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(r, limit+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeded %d byte limit", limit)
	}
	return data, nil
}
