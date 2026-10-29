package permission

import (
	"encoding/json"
	"testing"
)

// #4290 regression: the Windows shell tools reach ConfigPolicy as "Bash"
// (PowerShell main shell; display-name-sourced) and as lowercase
// "bash"/"powershell" from the agent layer. Before the fix isCommandTool was
// an exact case-sensitive list without those names, so ALL command floors
// (extremely-dangerous, credential-payload exfil, sensitive redirect) were
// skipped on Windows in every mode.
func TestIssue4290_WindowsShellNamesHitCommandFloors(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{`C:\proj`}, BypassMode)

	// "Bash" + sensitive-path redirect (persistence-backdoor floor #573-C):
	// the exact pattern that silently passed pre-fix.
	d, err := p.Check("Bash", json.RawMessage(`{"command":"sh -c 'echo ssh-ed25519 AAA > /c/Users/dev/.ssh/authorized_keys'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "Bash" sensitive redirect must Ask in bypass, got %v`, d)
	}

	// "PowerShell" (EqualFold variant) + extremely-dangerous floor.
	d, err = p.Check("PowerShell", json.RawMessage(`{"command":"rm -rf --no-preserve-root /"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "PowerShell" extremely-dangerous must Ask in bypass, got %v`, d)
	}

	// lowercase variants (agent-layer registration names) keep the same floors.
	d, err = p.Check("powershell", json.RawMessage(`{"command":"sh -c 'echo x > /root/.ssh/authorized_keys'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "powershell" sensitive redirect must Ask in bypass, got %v`, d)
	}
}

// #4290 companion to #4273: activating the floors on Windows shells must not
// resurrect the #4273 false-positive family. Ordinary PowerShell substitution
// syntax without any sensitive target stays Allow in bypass - the deleted
// #4273 tool-layer rules were syntax-primitive based; these policy floors are
// narrow semantic gates.
func TestIssue4290_OrdinaryPowerShellStaysAllowInBypass(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{`C:\proj`}, BypassMode)
	cmd := `$files = Get-ChildItem $(Split-Path $PROFILE); $files | ForEach-Object { Write-Host "$($_.Name): $($_.Length)" }; $env:PATH -split ';' | Select-Object -First 3`
	d, err := p.Check("Bash", json.RawMessage(`{"command":`+mustJSONStr4273(cmd)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Allow {
		t.Fatalf("ordinary PS substitution chain must stay Allow in bypass, got %v", d)
	}
}
