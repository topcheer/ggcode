package permission

import (
	"encoding/json"
	"testing"
)

// Regression for the second #4279 screenshot case: write_file to an
// out-of-sandbox /tmp path (an airgap image-push script embedding the
// user's own Harbor credentials) must not gate in either mode. The content
// carrying credentials is normal workflow, not a persistence backdoor.
func TestIssue4279AirgapWrite(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/tmp/airgap-test/push_scan.py","content":"#!/usr/bin/env python3\nUSER, PW = \"admin\", \"Harbor12345\"\nREG = \"harbor.iot2.win\"\nauth = base64.b64encode(f\"{USER}:{PW}\".encode()).decode()\n"}`)
	for _, mode := range []PermissionMode{BypassMode, SupervisedMode} {
		p := NewConfigPolicyWithMode(nil, []string{"/Volumes/new/ggai/ggcode"}, mode)
		d, err := p.Check("write_file", input)
		if err != nil {
			t.Fatalf("[%v] err: %v", mode, err)
		}
		if d != Allow {
			t.Errorf("[%v] out-of-sandbox /tmp write gated: got %v", mode, d)
		}
	}
}
