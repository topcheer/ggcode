package agentruntime

import (
	"testing"
)

// r441 probes: first-success auction close (yieldLaggards).

func TestYieldLaggards(t *testing.T) {
	cases := []struct {
		name     string
		leader   int
		laggards map[string]int
		want     []string
	}{
		{"no laggards", 10, nil, nil},
		{"zero leader spend", 0, map[string]int{"a": 100}, nil},
		{"all cheap - no yield", 10, map[string]int{"a": 15}, nil},
		{"one lost cause", 10, map[string]int{"a": 15, "b": 20}, []string{"b"}}, // 20 > 1.5*10
		{"all lost causes - sorted", 2, map[string]int{"c": 30, "a": 10, "b": 20}, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		got := yieldLaggards(c.leader, c.laggards)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v (order matters)", c.name, got, c.want)
			}
		}
	}
}
