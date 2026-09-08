package memory

import "testing"

// Regression for #1878 (residual of #1777 case 2): the polarity gate
// raised its threshold 0.3 -> 0.85 but lacked the return-false short
// circuit, so mixed-polarity pairs fell through to the two-affirmative
// band (>= 0.3) and still conflicted - zero behavioral change. A
// mixed-polarity pair that is NOT near-equivalent is compatible by
// design and must never reach the positive band.
func TestPolarityConflictShortCircuit(t *testing.T) {
	// The exact scenario from #1777's commit message: still must NOT
	// conflict (jaccard 2/3 < 0.85, and '~' is dropped by tokenize so the
	// positive band would see the same 2/3 without the short-circuit).
	if claimsConflict("use git", "~use git rebase") {
		t.Fatal(`"use git" vs "~use git rebase" is compatible (different domains), must not conflict`)
	}
	// Near-equivalent flip still conflicts.
	if !claimsConflict("use git", "~use git") {
		t.Fatal(`"use git" vs "~use git" is a true polarity flip, must conflict`)
	}
	if !claimsConflict("always run tests", "~always run tests") {
		t.Fatal("identical-base flip must conflict")
	}
	// Two-affirmative band still works for same-polarity pairs.
	if !claimsConflict("use gin framework", "use echo framework") {
		t.Fatal("same-polarity moderate-overlap assignment conflict must still fire")
	}
}
