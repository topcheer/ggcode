package agent

import (
	"strings"
	"testing"
)

func TestPolicyVerifierService_VerifyDestructiveShell(t *testing.T) {
	pv := NewPolicyVerifierService()

	tests := []struct {
		name       string
		toolName   string
		params     map[string]interface{}
		wantComply bool
		wantReason string
	}{
		{
			name:       "safe command",
			toolName:   "run_command",
			params:     map[string]interface{}{"command": "ls -la"},
			wantComply: true,
		},
		{
			name:       "rm -rf root",
			toolName:   "run_command",
			params:     map[string]interface{}{"command": "rm -rf /"},
			wantComply: false,
			wantReason: "destructive",
		},
		{
			name:       "fork bomb",
			toolName:   "start_command",
			params:     map[string]interface{}{"command": ":(){:|:&};:"},
			wantComply: false,
			wantReason: "destructive",
		},
		{
			name:       "non-command tool",
			toolName:   "read_file",
			params:     map[string]interface{}{"path": "/etc/passwd"},
			wantComply: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complies, _, reason := pv.VerifyAction(tt.toolName, tt.params)
			if complies != tt.wantComply {
				t.Errorf("VerifyAction() comply = %v, want %v", complies, tt.wantComply)
			}
			if !tt.wantComply && reason == "" {
				t.Errorf("VerifyAction() expected non-empty reason for violation")
			}
			if !tt.wantComply && tt.wantReason != "" && !strings.Contains(reason, tt.wantReason) {
				t.Errorf("VerifyAction() reason = %v, want to contain %v", reason, tt.wantReason)
			}
		})
	}
}

func TestPolicyVerifierService_VerifyCriticalPathWrites(t *testing.T) {
	pv := NewPolicyVerifierService()

	tests := []struct {
		name       string
		toolName   string
		params     map[string]interface{}
		wantComply bool
	}{
		{
			name:       "write to safe path",
			toolName:   "write_file",
			params:     map[string]interface{}{"path": "/tmp/file.txt", "content": "test"},
			wantComply: true,
		},
		{
			name:       "write to /etc",
			toolName:   "edit_file",
			params:     map[string]interface{}{"path": "/etc/hosts"},
			wantComply: false,
		},
		{
			name:       "write to /usr",
			toolName:   "multi_file_write",
			params:     map[string]interface{}{"files": []map[string]interface{}{{"path": "/usr/local/bin/test"}}},
			wantComply: false,
		},
		{
			name:       "read operation is allowed",
			toolName:   "read_file",
			params:     map[string]interface{}{"path": "/etc/passwd"},
			wantComply: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complies, _, _ := pv.VerifyAction(tt.toolName, tt.params)
			if complies != tt.wantComply {
				t.Errorf("VerifyAction() comply = %v, want %v", complies, tt.wantComply)
			}
		})
	}
}

func TestPolicyVerifierService_VerifySecretExclusion(t *testing.T) {
	pv := NewPolicyVerifierService()

	tests := []struct {
		name       string
		toolName   string
		params     map[string]interface{}
		wantComply bool
	}{
		{
			name:       "safe content",
			toolName:   "write_file",
			params:     map[string]interface{}{"content": "print('hello world')"},
			wantComply: true,
		},
		{
			name:       "api_key in content",
			toolName:   "write_file",
			params:     map[string]interface{}{"content": "api_key = 'sk-1234567890abcdef1234567890abcdef'"},
			wantComply: false,
		},
		{
			name:       "password in content",
			toolName:   "write_file",
			params:     map[string]interface{}{"content": "password = 'supersecret123'"},
			wantComply: false,
		},
		{
			name:       "non-write tool",
			toolName:   "run_command",
			params:     map[string]interface{}{"command": "echo 'secret'"},
			wantComply: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			complies, _, _ := pv.VerifyAction(tt.toolName, tt.params)
			if complies != tt.wantComply {
				t.Errorf("VerifyAction() comply = %v, want %v", complies, tt.wantComply)
			}
		})
	}
}

func TestPolicyVerifierService_Stats(t *testing.T) {
	pv := NewPolicyVerifierService()

	// Initial stats
	stats := pv.Stats()
	if stats.TotalVerifications != 0 {
		t.Errorf("Initial TotalVerifications = %v, want 0", stats.TotalVerifications)
	}

	// Perform some verifications
	pv.VerifyAction("run_command", map[string]interface{}{"command": "ls"})
	pv.VerifyAction("write_file", map[string]interface{}{"path": "/tmp/test", "content": "test"})
	pv.VerifyAction("write_file", map[string]interface{}{"path": "/etc/test", "content": "test"})

	stats = pv.Stats()
	if stats.TotalVerifications != 3 {
		t.Errorf("TotalVerifications = %v, want 3", stats.TotalVerifications)
	}
	if stats.Compliant != 2 {
		t.Errorf("Compliant = %v, want 2", stats.Compliant)
	}
	if stats.Violated != 1 {
		t.Errorf("Violated = %v, want 1", stats.Violated)
	}
}

func TestPolicyVerifierService_RegisterContract(t *testing.T) {
	pv := NewPolicyVerifierService()

	initialCount := len(pv.contracts)

	// Register a custom contract
	pv.RegisterContract(&SafetyContract{
		Name:        "test-contract",
		Description: "Test contract",
		Verifiers: []PolicyVerifier{
			func(toolName string, params map[string]interface{}) (bool, string) {
				return toolName != "forbidden_tool", "forbidden tool"
			},
		},
	})

	if len(pv.contracts) != initialCount+1 {
		t.Errorf("contract count = %v, want %v", len(pv.contracts), initialCount+1)
	}

	// Test the new contract
	complies, violation, _ := pv.VerifyAction("forbidden_tool", map[string]interface{}{})
	if complies {
		t.Errorf("VerifyAction() should reject forbidden_tool")
	}
	if violation != "test-contract" {
		t.Errorf("violation = %v, want test-contract", violation)
	}
}

func TestPolicyVerifierService_VerifyToolCall(t *testing.T) {
	pv := NewPolicyVerifierService()

	// Compliant call should not return error
	err := pv.VerifyToolCall("run_command", map[string]interface{}{"command": "ls"})
	if err != nil {
		t.Errorf("VerifyToolCall() unexpected error = %v", err)
	}

	// Violation should return PolicyViolationError
	err = pv.VerifyToolCall("run_command", map[string]interface{}{"command": "rm -rf /"})
	if err == nil {
		t.Errorf("VerifyToolCall() expected error for violation, got nil")
	}
	if _, ok := err.(*PolicyViolationError); !ok {
		t.Errorf("VerifyToolCall() error type = %T, want *PolicyViolationError", err)
	}
}
