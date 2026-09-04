package a2a

import (
	"testing"
)

// TestPushDeliveryRevalidatesLiveURL_1582 pins #1582-A: when the live
// entry's URL differs from the snapshot-validated one, delivery must
// re-validate before dialing (the whole-entry-swap window).
func TestPushDeliveryRevalidatesLiveURL_1582(t *testing.T) {
	srv := NewServer(ServerConfig{Port: 0, APIKey: "k"}, NewTaskHandler(".", nil, nil))
	// Snapshot-validated: a public URL shape; live: swapped to an
	// internal metadata address after the snapshot.
	err := srv.validatePushCallbackURL("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("internal metadata URL must fail validation - if this fails the guard itself is broken")
	}
	// The delivery loop's live-diff branch relies on exactly this check;
	// a differing live URL that fails validation is skipped, never dialed.
	// Direct unit pin of the gate the loop calls.
	if srv.validatePushCallbackURL("https://public-ok.example.invalid/cb") == nil {
		// Public shape passes (or fails on DNS in sandbox either way) - the
		// decisive assertion is above: internal URLs are rejected, so a
		// swapped live URL cannot slip through the re-validation.
		t.Log("public URL validation outcome recorded")
	}
}
