package config

import (
	"strings"
	"testing"
)

func TestNormalizeOutputStyle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"concise", "concise"},
		{"CONCISE", "concise"},
		{"  detailed  ", "detailed"},
		{"socratic", "socratic"},
		{"unknown", ""},
		{"verbose", ""},
	}
	for _, tc := range tests {
		got := NormalizeOutputStyle(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeOutputStyle(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNextOutputStyle(t *testing.T) {
	// Cycle: "" -> concise -> detailed -> socratic -> ""
	tests := []struct {
		current string
		want    string
	}{
		{"", "concise"},
		{"concise", "detailed"},
		{"detailed", "socratic"},
		{"socratic", ""},
		{"unknown", "concise"}, // unknown normalizes to start of cycle
	}
	for _, tc := range tests {
		got := NextOutputStyle(tc.current)
		if got != tc.want {
			t.Errorf("NextOutputStyle(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
}

func TestOutputStyleGuidance(t *testing.T) {
	// Default style returns no guidance
	if g := OutputStyleGuidance(""); g != "" {
		t.Errorf("OutputStyleGuidance(\"\") should be empty, got %q", g)
	}

	// Known styles return non-empty guidance
	for _, style := range []string{"concise", "detailed", "socratic"} {
		g := OutputStyleGuidance(style)
		if g == "" {
			t.Errorf("OutputStyleGuidance(%q) should not be empty", style)
		}
		if !strings.Contains(g, "Output Style") {
			t.Errorf("OutputStyleGuidance(%q) should contain 'Output Style' header", style)
		}
	}

	// Unknown style returns empty
	if g := OutputStyleGuidance("unknown"); g != "" {
		t.Errorf("OutputStyleGuidance(\"unknown\") should be empty, got %q", g)
	}
}

func TestDisplayOutputStyle(t *testing.T) {
	if d := DisplayOutputStyle(""); d != "default" {
		t.Errorf("DisplayOutputStyle(\"\") = %q, want \"default\"", d)
	}
	if d := DisplayOutputStyle("concise"); d != "concise" {
		t.Errorf("DisplayOutputStyle(\"concise\") = %q, want \"concise\"", d)
	}
	if d := DisplayOutputStyle("unknown"); d != "default" {
		t.Errorf("DisplayOutputStyle(\"unknown\") = %q, want \"default\"", d)
	}
}

func TestBuildSystemPromptWithOutputStyle(t *testing.T) {
	// BuildSystemPrompt doesn't directly use OutputStyle; the prompt builder
	// in agentruntime adds it. But we can verify the guidance is injectable.
	base := BuildSystemPrompt("", "/tmp", "en", []string{"read_file"}, "", nil, nil)
	if strings.Contains(base, "Output Style: Concise") {
		t.Error("base prompt should not contain output style guidance")
	}

	guidance := OutputStyleGuidance("concise")
	if guidance == "" {
		t.Fatal("expected non-empty guidance for concise")
	}
	full := base + "\n\n" + guidance
	if !strings.Contains(full, "Output Style: Concise") {
		t.Error("full prompt should contain output style guidance")
	}
}
