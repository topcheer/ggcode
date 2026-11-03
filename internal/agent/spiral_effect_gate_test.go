package agent

import "testing"

func TestSpiralUnverifiedBrowserClickDoesNotClearTopics(t *testing.T) {
	a := &Agent{spiralState: newSpiralHallucinationState()}
	a.recordSpiralTurn("I assume the login button uses id=submit-btn on this page.")
	if len(a.spiralState.topics) == 0 {
		t.Fatal("sanity: topic must be tracked")
	}
	if a.spiralState.topics[0].verified {
		t.Fatal("sanity: topic starts unverified")
	}

	a.recordSpiralVerification("browser", "Clicked: #submit-btn\nCurrent URL: https://example.com/")
	if a.spiralState.topics[0].verified {
		t.Fatal("bare browser ok cleared spiral protection - only effect-confirmed results may verify")
	}

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

	a.recordSpiralVerification("run_command", "exit 0")
	if !a.spiralState.topics[0].verified {
		t.Fatal("run_command success must still verify (non-gated path unchanged)")
	}
}
