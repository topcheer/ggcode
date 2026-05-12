package util

import (
	"errors"
	"strings"
	"testing"
)

func TestReadAll_WithinLimit(t *testing.T) {
	input := "hello world"
	data, err := ReadAll(strings.NewReader(input), ReadLimitGeneral)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != input {
		t.Fatalf("got %q, want %q", string(data), input)
	}
}

func TestReadAll_ExceedsLimit(t *testing.T) {
	input := strings.Repeat("x", 1000)
	_, err := ReadAll(strings.NewReader(input), 100)
	if err == nil {
		t.Fatal("expected error when exceeding limit, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("error should mention limit: %v", err)
	}
}

func TestReadAll_ExactlyAtLimit(t *testing.T) {
	input := strings.Repeat("x", 100)
	data, err := ReadAll(strings.NewReader(input), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 100 {
		t.Fatalf("got %d bytes, want 100", len(data))
	}
}

func TestReadAll_EmptyReader(t *testing.T) {
	data, err := ReadAll(strings.NewReader(""), ReadLimitGeneral)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty data, got %d bytes", len(data))
	}
}

func TestReadAll_ReaderError(t *testing.T) {
	errReader := &errorReader{err: errors.New("read failed")}
	_, err := ReadAll(errReader, ReadLimitGeneral)
	if err == nil {
		t.Fatal("expected error from failing reader")
	}
	if !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected underlying error, got: %v", err)
	}
}

func TestReadAll_LimitConstants(t *testing.T) {
	tests := []struct {
		name   string
		limit  int64
		wantMB int
	}{
		{"ReadLimitAuth", ReadLimitAuth, 1},
		{"ReadLimitAPI", ReadLimitAPI, 50},
		{"ReadLimitMCP", ReadLimitMCP, 100},
		{"ReadLimitWebSocket", ReadLimitWebSocket, 10},
		{"ReadLimitGeneral", ReadLimitGeneral, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := int64(tt.wantMB) * 1024 * 1024
			if tt.limit != want {
				t.Errorf("%s = %d, want %d", tt.name, tt.limit, want)
			}
		})
	}
}

func TestReadAll_OneByteOverLimit(t *testing.T) {
	// 101 bytes into a 100-byte limit
	input := strings.Repeat("x", 101)
	_, err := ReadAll(strings.NewReader(input), 100)
	if err == nil {
		t.Fatal("expected error when 1 byte over limit")
	}
}

func TestReadAll_OneByteUnderLimit(t *testing.T) {
	// 99 bytes into a 100-byte limit
	input := strings.Repeat("x", 99)
	data, err := ReadAll(strings.NewReader(input), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 99 {
		t.Fatalf("got %d bytes, want 99", len(data))
	}
}

type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
