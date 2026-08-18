package permission

import (
	"encoding/json"
	"testing"
)

// zz_issue711_test.go — Auto mode must gate shell-redirect write targets the
// same way #573-C gates bypass/autopilot. Auto was the only mode silently
// allowing `> ~/.docker/config.json` or a LaunchAgents plist write.

func TestIssue711AutoModeRedirectWriteTargetDenied(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, AutoMode)

	dockerWrite := json.RawMessage(`{"command":"echo {\"credsStore\":\"evil-proxy\"} > ~/.docker/config.json"}`)
	d, err := policy.Check("run_command", dockerWrite)
	if err != nil || d != Deny {
		t.Errorf("#711: auto mode redirect write to ~/.docker/config.json should be Deny, got %v err=%v", d, err)
	}

	launchAgents := json.RawMessage(`{"command":"cat /tmp/x > ~/Library/LaunchAgents/com.evil.plist"}`)
	d, err = policy.Check("run_command", launchAgents)
	if err != nil || d != Deny {
		t.Errorf("#711: auto mode redirect write to LaunchAgents should be Deny, got %v err=%v", d, err)
	}

	sshWrite := json.RawMessage(`{"command":"echo ssh-ed25519 AAAA evil >> ~/.ssh/authorized_keys"}`)
	d, err = policy.Check("run_command", sshWrite)
	if err != nil || d != Deny {
		t.Errorf("#711: auto mode redirect append to authorized_keys should be Deny, got %v err=%v", d, err)
	}

	// Also for start_command (same command-tool family)
	d, err = policy.Check("start_command", dockerWrite)
	if err != nil || d != Deny {
		t.Errorf("#711: auto mode start_command redirect write should be Deny, got %v err=%v", d, err)
	}
}

func TestIssue711AutoModeInSandboxRedirectStillAllowed(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, AutoMode)

	// Redirect inside the workspace sandbox and to harmless sinks stays Allow —
	// the gate must not break normal auto-mode workflows.
	inSandbox := json.RawMessage(`{"command":"go test ./... > test-output.txt 2>&1"}`)
	d, err := policy.Check("run_command", inSandbox)
	if err != nil || d != Allow {
		t.Errorf("#711: auto mode in-sandbox redirect should be Allow, got %v err=%v", d, err)
	}

	devNull := json.RawMessage(`{"command":"noisy-cmd > /dev/null 2>&1"}`)
	d, err = policy.Check("run_command", devNull)
	if err != nil || d != Allow {
		t.Errorf("#711: auto mode redirect to /dev/null should be Allow, got %v err=%v", d, err)
	}

	plain := json.RawMessage(`{"command":"ls -la"}`)
	d, err = policy.Check("run_command", plain)
	if err != nil || d != Allow {
		t.Errorf("#711: auto mode plain command should be Allow, got %v err=%v", d, err)
	}
}
