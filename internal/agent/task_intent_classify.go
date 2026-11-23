package agent

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// Task-intent classification + small helpers recovered from removed
// guidance injectors: classifyTaskType/randomID came with the strategy
// playbook, indexFoldASCII with its sibling detector. Non-guidance callers
// (playbook_failures, autopilot_strategist) still need them.

func classifyTaskType(userPrompt string) string {
	p := strings.ToLower(userPrompt)
	switch {
	case containsAnyWord(p, "test", "spec", "coverage", "mock"):
		return "test"
	case containsAnyWord(p, "build", "compile", "make ", "ci ", "deploy", "release", "publish"):
		return "build"
	case containsAnyWord(p, "fix", "bug", "error", "crash", "broken", " fail", "panic", "traceback"):
		return "bugfix"
	case containsAnyWord(p, "refactor", "clean", "rename", "reorganize", "simplify", "extract"):
		return "refactor"
	case containsAnyWord(p, "review", "check", "audit", "inspect", "scan", "analyze"):
		return "review"
	case containsAnyWord(p, "add", "implement", "create", "new ", "support"):
		return "feature"
	default:
		return "other"
	}
}

func randomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func indexFoldASCII(s, needle string) int {
	if needle == "" {
		return 0
	}
	first := needle[0]
	lo, hi := lowerASCII(first), upperASCII(first)
	for i := 0; i+len(needle) <= len(s); i++ {
		c := s[i]
		if c != lo && c != hi {
			continue
		}
		if equalFoldASCII(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func containsAnyWord(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.ContainsAny(kw, " \t") {
			if strings.Contains(s, kw) {
				return true
			}
			continue
		}
		start := 0
		for {
			i := strings.Index(s[start:], kw)
			if i < 0 {
				break
			}
			at := start + i
			end := at + len(kw)
			if wordBoundaryAt(s, at) && wordBoundaryAt(s, end) {
				return true
			}
			start = at + 1
		}
	}
	return false
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func upperASCII(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := lowerASCII(a[i]), lowerASCII(b[i])
		if ca != cb {
			return false
		}
	}
	return true
}

func wordBoundaryAt(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	return !isWordByte(s[i-1]) || !isWordByte(s[i])
}
