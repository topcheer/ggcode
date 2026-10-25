//go:build goolm

package agent

// #4398 regression tests: inline-quote escape. The #4284 line-shape strip
// only catches line-leading quote shapes; inline-quoted discussion
// ("don't touch config" wrapped in quotes mid-sentence, backtick regex
// source, English negate keywords inside CJK prose) escaped it and
// entered the ledger as user-direct (production data: 13/15 entries
// polluted). Such excerpts must record as the advisory-only fragment
// kind; real direct directives keep user-direct.

import (
	"strings"
	"testing"
)

func TestIssue4398_InlineQuoteEscapeDowngraded(t *testing.T) {
	if !excerptLooksDiscussed(`touch config" 这类拒绝 wording`) {
		t.Fatalf("inline double-quoted discussion must be detected (#4398)")
	}
	if !excerptLooksDiscussed("t touch `don'?t|do not|never` source") {
		t.Fatalf("backtick regex material must be detected (#4398)")
	}
	if !excerptLooksDiscussed("touch config.yaml 之后不要继续修改") {
		t.Fatalf("English negate inside CJK prose must be detected (#4398)")
	}
	// Real directives stay direct.
	for _, real := range []string{
		"don't touch config.yaml",
		"never modify the production database",
		"don't run git push --force on main",
	} {
		if excerptLooksDiscussed(real) {
			t.Fatalf("real directive must not be flagged as discussed: %q (#4398)", real)
		}
	}
}

// End-to-end through recordUserRefusals: discussion excerpt lands as
// fragment (advisory-only); real directive lands as direct and still
// blocks write tools.
func TestIssue4398_RecordUserRefusalsKindSplit(t *testing.T) {
	a := &Agent{}
	a.refusalLedger = newRefusalTestLedger(t)

	a.recordUserRefusals(`我会讨论: 行内说 "don't touch configs/app.go" 这类拒绝模式很常见`)
	a.recordUserRefusals("please don't touch configs/deploy.go anymore")

	var discussed, direct bool
	for _, e := range a.refusalLedger.data.Entries {
		if strings.Contains(strings.ToLower(e.Excerpt), "configs/app") && e.Kind == refusalKindFragment {
			discussed = true
		}
		if strings.Contains(strings.ToLower(e.Excerpt), "configs/deploy") && e.Kind == refusalKindDirect {
			direct = true
		}
	}
	if !discussed {
		t.Fatalf("inline-quoted excerpt must be recorded as fragment, entries: %+v (#4398)", a.refusalLedger.data.Entries)
	}
	if !direct {
		t.Fatalf("real directive must still be recorded as direct, entries: %+v (#4398)", a.refusalLedger.data.Entries)
	}

	// Kind split in enforcement: only the direct entry may hard-block.
	if got := a.refusalLedger.checkBlocked("edit_file", `{"file_path":"/x/configs/deploy.go"}`); got == "" {
		t.Fatalf("direct configs/deploy entry must block edit_file (#4398)")
	}
	if got := a.refusalLedger.checkBlocked("edit_file", `{"file_path":"/x/configs/app.go"}`); got != "" {
		t.Fatalf("fragment (discussed) configs/app entry must NOT hard-block, got %q (#4398)", got)
	}
}

// Dedup upgrade (#4398): a discussion entry followed by the user's REAL
// directive with the same key must upgrade to direct, never stay
// advisory-only.
func TestIssue4398_DedupUpgradeDiscussionToDirect(t *testing.T) {
	a := &Agent{}
	a.refusalLedger = newRefusalTestLedger(t)

	a.recordUserRefusals(`we often see "don't touch config.yaml" in reviews`)
	a.recordUserRefusals("don't touch config.yaml")

	for _, e := range a.refusalLedger.data.Entries {
		if strings.Contains(strings.ToLower(e.Excerpt), "config") {
			if e.Kind != refusalKindDirect {
				t.Fatalf("real directive must upgrade the duplicate discussion entry to direct, got %q kind=%s (#4398)", e.Excerpt, e.Kind)
			}
		}
	}
}
