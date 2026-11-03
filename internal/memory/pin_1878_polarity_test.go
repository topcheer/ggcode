package memory

import "testing"

func TestPolarityConflictShortCircuit(t *testing.T) {

	if claimsConflict("use git", "~use git rebase") {
		t.Fatal(`"use git" vs "~use git rebase" is compatible (different domains), must not conflict`)
	}

	if !claimsConflict("use git", "~use git") {
		t.Fatal(`"use git" vs "~use git" is a true polarity flip, must conflict`)
	}
	if !claimsConflict("always run tests", "~always run tests") {
		t.Fatal("identical-base flip must conflict")
	}

	if !claimsConflict("use gin framework", "use echo framework") {
		t.Fatal("same-polarity moderate-overlap assignment conflict must still fire")
	}
}
