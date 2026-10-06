package agent

import "testing"

// r34 (computer-use postmortems): effect-gated GUI tools must NOT clear
// hallucination-spiral protection on a bare success - a browser click
// returns ok even when an overlay swallowed it. Only the tool layer's
// post-action "effect: confirmed" marker counts as verification.

func TestSpiralUnverifiedBrowserClickDoesNotClearTopics(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the login button uses id=submit-btn on this page.")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("sanity: topic must be tracked")
	}
	if a.spiralState.topics[0].verified {
		t.Fatal("sanity: topic starts unverified")
	}

	// Bare browser success (no effect marker) must NOT verify the topic.
	a.recordSpiralVerification("browser", "Clicked: #submit-btn\nCurrent URL: https://example.com/")
	if a.spiralState.topics[0].verified {
		t.Fatal("bare browser ok cleared spiral protection - only effect-confirmed results may verify")
	}

	// A subsequent effect-confirmed result DOES verify it.
	a.recordSpiralVerification("browser", "Clicked: #submit-btn\neffect: confirmed (navigation to https://example.com/dashboard)")
	if !a.spiralState.topics[0].verified {
		t.Fatal("effect-confirmed browser result must verify the topic")
	}
}

func TestSpiralDesktopControlBareSuccessDoesNotClearTopics(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the save dialog is frontmost for the find_and_click target.")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("sanity: topic must be tracked")
	}
	a.recordSpiralVerification("desktop_control", "clicked center (512, 384)")
	if a.spiralState.topics[0].verified {
		t.Fatal("bare desktop_control ok must not clear spiral protection")
	}
}

func TestSpiralNonGatedToolsUnaffected(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the port is 5432.")
	// run_command has direct observable exit status - no effect marker needed.
	a.recordSpiralVerification("run_command", "exit 0")
	if !a.spiralState.topics[0].verified {
		t.Fatal("run_command success must still verify (non-gated path unchanged)")
	}
}
