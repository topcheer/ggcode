package permission

import (
	"encoding/json"
	"testing"
)

func TestIssue4290_WindowsShellNamesHitCommandFloors(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{`C:\proj`}, BypassMode)

	d, err := p.Check("Bash", json.RawMessage(`{"command":"sh -c 'echo ssh-ed25519 AAA > /c/Users/dev/.ssh/authorized_keys'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "Bash" sensitive redirect must Ask in bypass, got %v`, d)
	}

	d, err = p.Check("PowerShell", json.RawMessage(`{"command":"rm -rf --no-preserve-root /"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "PowerShell" extremely-dangerous must Ask in bypass, got %v`, d)
	}

	d, err = p.Check("powershell", json.RawMessage(`{"command":"sh -c 'echo x > /root/.ssh/authorized_keys'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf(`tool "powershell" sensitive redirect must Ask in bypass, got %v`, d)
	}
}

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
