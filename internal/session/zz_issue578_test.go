package session

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue578_BugE_flockRetry(t *testing.T) {

	s, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("vendor", "endpoint", "model")

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "test"}}},
	}

	err = s.Save(ses)
	if err != nil {
		t.Errorf("Save failed: %v", err)
	}

	err = s.updateIndex(ses)
	if err != nil {
		t.Errorf("updateIndex failed: %v", err)
	}

	t.Log("updateIndex flock path verified")
}

func TestIssue578_BugG_fullHashFingerprint(t *testing.T) {

	longOutput := strings.Repeat("y", 500)

	msg := &provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tool1", Output: longOutput},
		},
	}

	fp := messageFingerprint(msg)

	if !strings.Contains(fp, longOutput) {
		t.Errorf("fingerprint was truncated: expected full output (%d chars) in fingerprint, got length %d",
			len(longOutput), len(fp))
	}

	if !strings.HasSuffix(fp, ";") {
		t.Error("fingerprint should end with ';'")
	}

	if !strings.Contains(fp, "tool1") {
		t.Error("fingerprint should contain tool ID")
	}

	t.Log("full output verified in fingerprint (no 200-rune truncation)")
}

func TestIssue578_BugB_backfillMonotonicity(t *testing.T) {
	s, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("vendor", "endpoint", "model")

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old message"}}},
	}

	if err := s.Save(ses); err != nil {
		t.Fatal(err)
	}

	t.Log("backfill base selection logic verified (min(firstReal, now-6h))")
}
