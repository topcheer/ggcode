package agent

import (
	"strings"
	"testing"
)

func TestCheckRangeNilPtr_SelectorGuardExempt(t *testing.T) {
	src := `package main

type Config struct{ Items *[]int }

func process(cfg *Config) {
	if cfg.Items != nil {
		for _, v := range *cfg.Items {
			_ = v
		}
	}
}
`
	if w := checkRangeNilPtr("test.go", "", src); w != "" {
		t.Fatalf("guarded selector deref must be exempt, got: %s", w)
	}
}

func TestCheckRangeNilPtr_SelectorUnguardedStillWarns(t *testing.T) {
	src := `package main

type Config struct{ Items *[]int }

func process(cfg *Config) {
	for _, v := range *cfg.Items {
		_ = v
	}
}
`
	w := checkRangeNilPtr("test.go", "", src)
	if w == "" {
		t.Fatal("unguarded selector deref must still warn")
	}
	if !strings.Contains(w, "cfg.Items") {
		t.Fatalf("warning should name cfg.Items, got: %s", w)
	}
}
