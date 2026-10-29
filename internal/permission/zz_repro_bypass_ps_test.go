package permission

import (
	"encoding/json"
	"testing"
)

// #4273 regression, updated contract after #4290: the deploy chain itself
// no longer trips the deleted syntax-primitive rules (the #4273 fix), and
// the policy layer's narrow semantic floors now apply to Windows shells
// ("Bash" joined isCommandTool with case-insensitive matching). The chain's
// quoted body contains `> /src/home/.npmrc` - a single-quoted absolute-path
// redirect to a credential file, exactly the conservative subset
// extractRedirectTargets captures for run_command too (sh -c 'echo x > tgt'
// PoC). The pre-#4290 pin (Allow) existed only because the tool name was
// missing from isCommandTool; the conscious decision recorded there is now
// taken: Windows shells get the SAME floors as run_command - one Ask (not
// Deny) on a genuinely sensitive-target write, approving once proceeds.
func TestIssue4273_BypassPowerShellDeployChainAllow(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{`C:\proj`}, BypassMode)
	cmd := `$p = [IO.File]::ReadAllText("$env:TEMP\fullchain_prompt7.txt"); $p = $p -replace ' - node: npm install --legacy-peer-deps --no-audit --no-fund;node_modules/ 是检出主体\(裸 package-lock 镜像模式无效!\)'; $p = $p -replace ' - node: 先 printf "$NPMRC" > /src/home/.npmrc 且 chmod 600(registry=dev-nexus,勿用 /etc/npmrc 的 wxnex 源—axios 等老包只在 dev-nexus);npm install --legacy-peer-deps --no-audit --no-fund;node_modules/ 是检出主体(裸 package-lock 镜像模式无效!)'; $b64p = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($p)); $k="$env:USERPROFILE\.kube\vks-mgmt"; $cmd = "echo $b64p | base64 -d | mysql -uroot -pb15ca3dd k8s --sh -c $cmd 2>&1 | Select-Object -Last 1"; kubectl --kubeconfig $k -n codeinsight exec codeinsight-mysql-0 -- sh -c $cmd`
	d, err := p.Check("Bash", json.RawMessage(`{"command":`+mustJSONStr4273(cmd)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if d != Ask {
		t.Fatalf("post-#4290: quoted-body sensitive redirect (.npmrc) must Ask in bypass (Windows parity with run_command floors), got %v", d)
	}
}

func mustJSONStr4273(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
