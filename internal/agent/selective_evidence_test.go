package agent

import (
	"testing"
)

func TestScanEvidenceClaims_Empty(t *testing.T) {
	claims := scanEvidenceClaims("")
	if len(claims) != 0 {
		t.Fatalf("expected 0 claims, got %d", len(claims))
	}
}

func TestScanEvidenceClaims_PositiveOnly(t *testing.T) {
	text := "The test passes successfully and the build is correct. This works as expected."
	claims := scanEvidenceClaims(text)
	posCount := 0
	for _, c := range claims {
		if c.category == "positive" {
			posCount++
		}
	}
	if posCount == 0 {
		t.Fatalf("expected positive claims, got 0: %+v", claims)
	}
}

func TestScanEvidenceClaims_DismissiveOnly(t *testing.T) {
	text := "That error can be safely ignored. It's a false positive and not related to this issue."
	claims := scanEvidenceClaims(text)
	disCount := 0
	for _, c := range claims {
		if c.category == "dismissive" {
			disCount++
		}
	}
	if disCount == 0 {
		t.Fatalf("expected dismissive claims, got 0: %+v", claims)
	}
}

func TestScanEvidenceClaims_BothPresent(t *testing.T) {
	text := `The test passes successfully. The code works correctly and handles this properly.
	That error can be safely ignored, it's a false positive and not related to our fix.
	Don't worry about that warning, it doesn't actually affect anything.`
	claims := scanEvidenceClaims(text)
	posCount, disCount := 0, 0
	for _, c := range claims {
		switch c.category {
		case "positive":
			posCount++
		case "dismissive":
			disCount++
		}
	}
	if posCount == 0 {
		t.Fatalf("expected positive claims, got 0")
	}
	if disCount == 0 {
		t.Fatalf("expected dismissive claims, got 0")
	}
}

func TestMaybeWarnSelectiveEvidence_NoWarningWhenOnlyPositive(t *testing.T) {
	a := &Agent{
		selectiveEvidence: newSelectiveEvidenceTrackerState(),
	}
	text := "The test passes successfully. The code is correct and working as expected."
	// Only positive claims, no dismissive -> should NOT warn
	result := a.maybeWarnSelectiveEvidence(text)
	if result != "" {
		t.Fatalf("expected no warning with only positive claims, got: %s", result)
	}
}

func TestMaybeWarnSelectiveEvidence_NoWarningWhenOnlyDismissive(t *testing.T) {
	a := &Agent{
		selectiveEvidence: newSelectiveEvidenceTrackerState(),
	}
	text := "That error can be safely ignored. It's a false positive, don't worry about it."
	result := a.maybeWarnSelectiveEvidence(text)
	if result != "" {
		t.Fatalf("expected no warning with only dismissive claims, got: %s", result)
	}
}

func TestMaybeWarnSelectiveEvidence_WarnsWhenBothPresent(t *testing.T) {
	a := &Agent{
		selectiveEvidence: newSelectiveEvidenceTrackerState(),
	}
	text := `The test passes successfully and this works correctly.
	That error can be safely ignored, it's a false positive.
	It doesn't actually affect our fix and is not related to this issue.`
	result := a.maybeWarnSelectiveEvidence(text)
	if result == "" {
		t.Fatal("expected warning when both positive and dismissive claims present")
	}
}

func TestMaybeWarnSelectiveEvidence_WarnsContainsConfirmationBias(t *testing.T) {
	a := &Agent{
		selectiveEvidence: newSelectiveEvidenceTrackerState(),
	}
	text := `The build passes and the code is correct. The test passes as expected.
	That error can be safely ignored, it's a pre-existing false positive.`
	result := a.maybeWarnSelectiveEvidence(text)
	if result == "" {
		t.Fatal("expected warning")
	}
	if !contains(result, "confirmation-bias") {
		t.Fatalf("expected 'confirmation-bias' tag in warning, got: %s", result)
	}
}

func TestMaybeWarnSelectiveEvidence_RespectsMaxWarnings(t *testing.T) {
	a := &Agent{
		selectiveEvidence: &selectiveEvidenceTrackerState{warnings: selectiveEvidenceMaxWarnings},
	}
	text := "The test passes correctly. That error can be safely ignored."
	result := a.maybeWarnSelectiveEvidence(text)
	if result != "" {
		t.Fatal("expected no warning when max warnings reached")
	}
}

func TestMaybeWarnSelectiveEvidence_NilTracker(t *testing.T) {
	a := &Agent{}
	result := a.maybeWarnSelectiveEvidence("anything")
	if result != "" {
		t.Fatal("expected empty string with nil tracker")
	}
}

func TestMaybeWarnSelectiveEvidence_ResetClearsState(t *testing.T) {
	s := newSelectiveEvidenceTrackerState()
	s.warnings = 5
	s.reset()
	if s.warnings != 0 {
		t.Fatalf("expected warnings=0 after reset, got %d", s.warnings)
	}
}

func TestMaybeWarnSelectiveEvidence_CountsUp(t *testing.T) {
	a := &Agent{
		selectiveEvidence: newSelectiveEvidenceTrackerState(),
	}
	text := `The test passes successfully and is correct. The build works as expected.
	That error can be safely ignored, it's a false positive. Don't worry about it,
	it doesn't actually affect anything.`
	_ = a.maybeWarnSelectiveEvidence(text)
	if a.selectiveEvidence.warnings != 1 {
		t.Fatalf("expected warnings=1 after first trigger, got %d", a.selectiveEvidence.warnings)
	}
}

func TestScanEvidenceClaims_Deduplication(t *testing.T) {
	text := "The test passes successfully. The test passes successfully."
	claims := scanEvidenceClaims(text)
	// Deduplicate by excerpt
	seen := make(map[string]bool)
	for _, c := range claims {
		key := c.category + ":" + c.excerpt
		if seen[key] {
			t.Fatal("found duplicate claim excerpt")
		}
		seen[key] = true
	}
}

// contains is already defined in reflection_test.go
