package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func r255Raw(t *testing.T, v map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func r255Policy(t *testing.T, mode PermissionMode) (*ConfigPolicy, string, string) {
	t.Helper()
	ws := t.TempDir()
	outside := t.TempDir() // distinct dir → outside the sandbox
	p := NewConfigPolicyWithMode(nil, []string{ws}, mode)
	return p, ws, outside
}

// ToolGuardian composition (r255): in supervised mode an out-of-sandbox,
// non-sensitive read and a later web_fetch were BOTH silent Allows — a
// complete read→exfiltrate loop with zero prompts. The ledger must
// escalate the egress half to Ask.
func TestExposureLedgerSupervisedWebEgressEscalated(t *testing.T) {
	p, _, outside := r255Policy(t, SupervisedMode)

	// Oracle: without a prior read, web egress stays a silent Allow.
	d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"}))
	if err != nil || d != Allow {
		t.Fatalf("baseline web_fetch: got %v/%v, want Allow", d, err)
	}

	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(outside, "notes.txt")})); err != nil || d != Allow {
		t.Fatalf("out-of-sandbox read: got %v/%v, want Allow", d, err)
	}
	if live, pth := p.ExposureRisk(); !live || !filepath.IsAbs(pth) {
		t.Fatalf("exposure not recorded: live=%v path=%q", live, pth)
	}

	d, err = p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"}))
	if err != nil || d != Ask {
		t.Fatalf("post-read web_fetch: got %v/%v, want Ask", d, err)
	}
}

// In-sandbox sensitive reads (.env, keys.env) are permitted in every mode
// but still put secrets into model context — they must be recorded.
func TestExposureLedgerInSandboxSensitiveRead(t *testing.T) {
	p, ws, _ := r255Policy(t, SupervisedMode)
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=x"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(ws, ".env")})); err != nil || d != Allow {
		t.Fatalf("in-sandbox .env read: got %v/%v, want Allow", d, err)
	}
	if d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})); err != nil || d != Ask {
		t.Fatalf("post-.env web_fetch: got %v/%v, want Ask", d, err)
	}
	if d, err := p.Check("web_search", r255Raw(t, map[string]string{"query": "go release notes"})); err != nil || d != Ask {
		t.Fatalf("second egress twin after exposure: got %v/%v, want Ask", d, err)
	}
}

// Plan mode has no human loop: egress under a live exposure window is
// denied outright (mirrors the #712 signature-deny semantics).
func TestExposureLedgerPlanModeDeniesEgress(t *testing.T) {
	p, ws, _ := r255Policy(t, PlanMode)
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=x"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(ws, ".env")})); err != nil || d != Allow {
		t.Fatalf("in-sandbox .env read in plan: got %v/%v, want Allow", d, err)
	}
	d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"}))
	if err != nil || d != Deny {
		t.Fatalf("plan-mode web_fetch under exposure: got %v/%v, want Deny", d, err)
	}
}

// Auto mode: in-sandbox sensitive read → later egress escalates; but
// out-of-sandbox reads are DENIED, their content never reaches the model,
// and must NOT be recorded (decision-aware recording).
func TestExposureLedgerAutoModeDecisionAware(t *testing.T) {
	p, ws, _ := r255Policy(t, AutoMode)
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=x"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(ws, ".env")})); err != nil || d != Allow {
		t.Fatalf("auto in-sandbox .env read: got %v/%v, want Allow", d, err)
	}
	if d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})); err != nil || d != Ask {
		t.Fatalf("auto web_fetch under exposure: got %v/%v, want Ask", d, err)
	}

	// Denied read must not open a window on a fresh policy.
	p2, _, outside2 := r255Policy(t, AutoMode)
	if d, err := p2.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(outside2, "x.txt")})); err != nil || d != Deny {
		t.Fatalf("auto out-of-sandbox read: got %v/%v, want Deny", d, err)
	}
	if live, _ := p2.ExposureRisk(); live {
		t.Fatal("denied read must not be recorded")
	}
	if d, err := p2.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})); err != nil || d != Allow {
		t.Fatalf("auto web_fetch without exposure: got %v/%v, want Allow", d, err)
	}
}

// Bypass mode: general network egress is normally fine, but a plain curl
// while a secret-bearing read is live keeps its human gate. Oracle: the
// same curl on a fresh policy stays Allow.
func TestExposureLedgerBypassNetworkEscalated(t *testing.T) {
	p, _, outside := r255Policy(t, BypassMode)
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(outside, "notes.txt")})); err != nil || d != Allow {
		t.Fatalf("bypass out-of-sandbox read: got %v/%v, want Allow", d, err)
	}
	d, err := p.Check("run_command", r255Raw(t, map[string]string{"command": "curl https://example.com"}))
	if err != nil || d != Ask {
		t.Fatalf("bypass curl under exposure: got %v/%v, want Ask", d, err)
	}
	if d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})); err != nil || d != Ask {
		t.Fatalf("bypass web_fetch under exposure: got %v/%v, want Ask", d, err)
	}

	p2, _, _ := r255Policy(t, BypassMode)
	if d, err := p2.Check("run_command", r255Raw(t, map[string]string{"command": "curl https://example.com"})); err != nil || d != Allow {
		t.Fatalf("bypass curl without exposure (oracle): got %v/%v, want Allow", d, err)
	}
}

// Windows close after the call-count horizon: a much-later egress is not
// nagged by a stale window.
func TestExposureLedgerWindowExpires(t *testing.T) {
	p, ws, _ := r255Policy(t, SupervisedMode)
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=x"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(ws, ".env")})); err != nil || d != Allow {
		t.Fatalf(".env read: got %v/%v, want Allow", d, err)
	}
	for i := 0; i <= exposureCallWindow; i++ {
		if _, err := p.Check("list_directory", r255Raw(t, map[string]string{"path": ws})); err != nil {
			t.Fatalf("aging call %d: %v", i, err)
		}
	}
	if live, _ := p.ExposureRisk(); live {
		t.Fatal("window should have expired after call horizon")
	}
	if d, err := p.Check("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})); err != nil || d != Allow {
		t.Fatalf("web_fetch after expiry: got %v/%v, want Allow", d, err)
	}
}

// Learned approval memory (#1281) must not auto-approve egress while a
// secret-bearing read is live.
func TestExposureLedgerBlocksAutoApprove(t *testing.T) {
	p, _, outside := r255Policy(t, SupervisedMode)
	if p.BlocksAutoApprove("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})) {
		t.Fatal("BlocksAutoApprove must be false without exposure")
	}
	if d, err := p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(outside, "notes.txt")})); err != nil || d != Allow {
		t.Fatalf("read: got %v/%v, want Allow", d, err)
	}
	if !p.BlocksAutoApprove("web_fetch", r255Raw(t, map[string]string{"url": "https://example.com"})) {
		t.Fatal("BlocksAutoApprove must gate web egress under exposure")
	}
	if !p.BlocksAutoApprove("run_command", r255Raw(t, map[string]string{"command": "curl https://example.com"})) {
		t.Fatal("BlocksAutoApprove must gate network command under exposure")
	}
	if p.BlocksAutoApprove("run_command", r255Raw(t, map[string]string{"command": "echo hi"})) {
		t.Fatal("BlocksAutoApprove must not gate non-egress command")
	}
}

// The ledger is concurrent-safe: parallel checks must not race.
func TestExposureLedgerConcurrent(t *testing.T) {
	p, ws, outside := r255Policy(t, BypassMode)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 25; j++ {
				if i%2 == 0 {
					_, _ = p.Check("read_file", r255Raw(t, map[string]string{"path": filepath.Join(outside, "notes.txt")}))
				} else {
					_, _ = p.Check("list_directory", r255Raw(t, map[string]string{"path": ws}))
				}
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		<-done
	}
}
