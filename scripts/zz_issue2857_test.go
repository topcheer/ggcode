package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestIssue2857GLMVisionHeuristicSuffixOnly(t *testing.T) {
	src, err := os.ReadFile("sync-model-caps.go")
	if err != nil {
		t.Fatalf("read generator: %v", err)
	}
	code := string(src)

	if regexp.MustCompile(`strings\.Contains\(m, "v"\)`).MatchString(code) {
		t.Errorf("#2857 bare Contains(m,\"v\") still present in generator")
	}

	const pattern = "glm-[0-9.]+v"
	if !strings.Contains(code, "`"+pattern+"`") {
		t.Fatalf("#2857 glmVisionSuffixRe backtick pattern %q not found in generator source", pattern)
	}
	re := regexp.MustCompile(pattern)

	cases := []struct {
		model string
		want  bool
	}{
		{"glm-4.5v", true},
		{"glm-4.6v", true},
		{"glm-5.1.5v", true},
		{"zai-org/glm-4.5v", true},
		{"glm-4.6", false},
		{"novita/glm-4.6", false},
		{"glm-4.6-preview", false},
		{"glm-4.6-voice", false},
		{"glm-4.6v-preview", true},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.model); got != tc.want {
			t.Errorf("#2857 pattern %q on %q = %v, want %v", pattern, tc.model, got, tc.want)
		}
	}

	if !regexp.MustCompile(`glmVisionSuffixRe\.MatchString\(m\)`).MatchString(code) {
		t.Errorf("#2857 glm switch case does not use glmVisionSuffixRe")
	}
}
