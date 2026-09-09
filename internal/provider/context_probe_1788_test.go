package provider

import (
	"context"
	"fmt"
	"testing"
)

// fakeProber1788 lets trySimpleProbe see a chosen error without network.
type fakeProber1788 struct {
	*mockProvider
	err error
}

func (f *fakeProber1788) probeChat(_ context.Context, _ []Message) error { return f.err }

// #1788: the simple probe used to return -1 (abort the ENTIRE probe,
// caller falls back to estimation, #1198 persists it) for ANY
// non-context error - a transient 429 at probe start could harden into
// a low cached window. Only AUTH may abort; everything else defers to
// tiered probing (returns 0).
func TestSimpleProbeClassification1788(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"auth abort", fmt.Errorf("401 unauthorized: invalid api key"), -1},
		{"rate limit defers", fmt.Errorf("429 Too Many Requests"), 0},
		{"network defers", fmt.Errorf("connection refused"), 0},
		{"timeout defers", fmt.Errorf("client timeout exceeded"), 0},
		{"5xx defers", fmt.Errorf("server error 503 Service Unavailable"), 0},
		{"unknown defers", fmt.Errorf("something odd"), 0},
		{"context defers", fmt.Errorf("request too long for context"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trySimpleProbe(context.Background(), &fakeProber1788{mockProvider: &mockProvider{}, err: tc.err})
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
