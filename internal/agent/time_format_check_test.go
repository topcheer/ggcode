package agent

import (
	"strings"
	"testing"
)

func TestCheckTimeFormat_DetectsYYYYMMDD(t *testing.T) {
	newCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("YYYY-MM-DD")
}
`
	warnings := checkTimeFormat("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for YYYY-MM-DD layout in Format()")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "YYYY-MM-DD") && strings.Contains(w, "2006") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning about YYYY-MM-DD with 2006 suggestion, got: %v", warnings)
	}
}

func TestCheckTimeFormat_DetectsStrftimeTokens(t *testing.T) {
	newCode := `package main

import "time"

func parseTime(s string) (time.Time, error) {
	return time.Parse("%Y-%m-%d %H:%M:%S", s)
}
`
	warnings := checkTimeFormat("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for strftime tokens in Parse()")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "%Y") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warning mentioning %%Y strftime token, got: %v", warnings)
	}
}

func TestCheckTimeFormat_NoWarningForCorrectGoLayout(t *testing.T) {
	newCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("2006-01-02")
}
`
	warnings := checkTimeFormat("test.go", "", newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for correct Go layout, got: %v", warnings)
	}
}

func TestCheckTimeFormat_DetectsJavaStyleLayout(t *testing.T) {
	newCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("yyyy/MM/dd HH:mm:ss")
}
`
	warnings := checkTimeFormat("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for Java-style date layout")
	}
}

func TestCheckTimeFormat_DeltaAware(t *testing.T) {
	oldCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("YYYY-MM-DD")
}
`
	newCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("YYYY-MM-DD")
}
`
	warnings := checkTimeFormat("test.go", oldCode, newCode)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for pre-existing wrong layout (delta), got: %v", warnings)
	}
}

func TestCheckTimeFormat_DetectsNewWrongLayoutInDelta(t *testing.T) {
	oldCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("YYYY-MM-DD")
}
`
	newCode := `package main

import "time"

func format(t time.Time) string {
	return t.Format("YYYY-MM-DD")
}

func formatTime(t time.Time) string {
	return t.Format("yyyy/MM/dd")
}
`
	warnings := checkTimeFormat("test.go", oldCode, newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warning for newly introduced wrong layout")
	}
}

func TestCheckTimeFormat_NonGoFileSkipped(t *testing.T) {
	content := `const layout = "YYYY-MM-DD";
`
	warnings := checkTimeFormat("test.js", "", content)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckTimeFormat_EmptyContent(t *testing.T) {
	warnings := checkTimeFormat("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckTimeFormat_CorrectGoLayoutNotFlagged(t *testing.T) {
	cases := []string{
		`package main
import "time"
func f(t time.Time) string { return t.Format("2006-01-02T15:04:05Z07:00") }
`,
		`package main
import "time"
func f(t time.Time) string { return t.Format(time.RFC3339) }
`,
		`package main
import "time"
func f(t time.Time) string { return t.Format("Jan _2 15:04:05") }
`,
	}
	for i, code := range cases {
		warnings := checkTimeFormat("test.go", "", code)
		if len(warnings) != 0 {
			t.Fatalf("case %d: expected no warnings for correct Go layout, got: %v", i, warnings)
		}
	}
}

func TestCheckTimeFormat_DetectsMultipleIssues(t *testing.T) {
	newCode := `package main

import "time"

func formatAll(t time.Time) string {
	s1 := t.Format("YYYY-MM-DD")
	s2 := t.Format("HH:mm:ss")
	s3 := t.Format("%Y/%m/%d")
	return s1 + s2 + s3
}
`
	warnings := checkTimeFormat("test.go", "", newCode)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for multiple wrong layouts")
	}
	// Should cap at 2 detailed + 1 summary
	if len(warnings) > 3 {
		t.Fatalf("expected at most 3 warnings (2 detailed + 1 summary), got %d", len(warnings))
	}
}

func TestConvertLayout_Basic(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"YYYY-MM-DD", "2006-01-02"},
		{"%Y-%m-%d", "2006-01-02"},
		{"HH:MM:SS", "15:01:05"},
		{"yyyy/DD", "2006/02"},
	}
	for _, tc := range tests {
		got := convertLayout(tc.input)
		if got != tc.expected {
			t.Errorf("convertLayout(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
