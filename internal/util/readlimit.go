package util

import (
	"fmt"
	"io"
)

const (
	ReadLimitAuth int64 = 1 * 1024 * 1024

	ReadLimitAPI int64 = 50 * 1024 * 1024

	ReadLimitMCP int64 = 100 * 1024 * 1024

	ReadLimitWebSocket int64 = 10 * 1024 * 1024

	ReadLimitGeneral int64 = 10 * 1024 * 1024
)

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
