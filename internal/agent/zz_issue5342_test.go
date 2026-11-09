package agent

import "testing"

// #5342: firstRmCommandSegment required fields[0]=="rm", so wrapper
// prefixes (sudo/nohup/xargs/env/timeout ...) hid the rm word behind a
// leading token and the rm_rf critical gate went blind exactly when
// privilege (sudo) or batching (xargs) was involved.

func hasRmRf5342(cmd string) bool {
	for _, p := range detectDestructiveInShellCommand(cmd) {
		if p.name == "rm_rf" {
			return true
		}
	}
	return false
}

// 漏报对照组 (OBS-046 criterion): wrapper-prefixed real invocations
// MUST fire - this is the regression the fix closes.
func TestIssue5342_WrapperPrefixedInvocationsFire(t *testing.T) {
	positives := []string{
		"sudo rm -rf /tmp/x",                        // privilege wrapper
		"nohup rm -rf /tmp/x",                       // nohup
		"xargs rm -rf < list",                       // batching wrapper
		"env rm -rf /tmp/x",                         // env wrapper
		"timeout 30 rm -rf /tmp/x",                  // timeout wrapper
		"sudo -u root rm -rf /tmp/x",                // sudo with args
		"FOO=1 sudo rm -rf /tmp/x",                  // env-assign + wrapper chain
		"cd x && sudo rm -rf tmp",                   // chain segment
		"cat > a <<'EOF'\nhi\nEOF\nsudo rm -rf tmp", // after closed heredoc
	}
	for i, cmd := range positives {
		if !hasRmRf5342(cmd) {
			t.Errorf("positive %d did NOT fire (wrapper blind spot): %q", i, cmd)
		}
	}
}

// 负向钉: text mentions still do not fire (#5315 negative pins must not
// regress).
func TestIssue5342_TextMentionsStillDoNotFire(t *testing.T) {
	negatives := []string{
		"echo 'sudo rm -rf /tmp/x'",
		"grep 'nohup rm -rf' build.log",
		"cat >> ledger.md <<'EOF'\nsudo rm -rf /\nlog note\nEOF",
		"git commit -m \"fix: drop xargs rm -rf usage\"",
	}
	for i, cmd := range negatives {
		if hasRmRf5342(cmd) {
			t.Errorf("negative %d fired on text-only mention: %q", i, cmd)
		}
	}
}

// 非wrapper词不被剥离: "echo rm -rf ..." must NOT fire (echo is not in
// the wrapper table - stripping it would regress the #5315 FP fix).
func TestIssue5342_EchoNotStripped(t *testing.T) {
	if hasRmRf5342("echo rm -rf /tmp/x") {
		t.Error("echo must not be treated as a wrapper: text mention fired")
	}
}
