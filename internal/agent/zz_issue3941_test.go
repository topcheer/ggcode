package agent

import "testing"

// TestClassifyVerificationCommandHeadScan (#3941): the npm/cargo/make
// branches must use #3812 head-scan semantics, not bare substring matching.
// Everyday agent commands (git commit messages, echo, grep) that merely
// *mention* a test command must NOT classify as verification commands.
func TestClassifyVerificationCommandHeadScan(t *testing.T) {
	cases := []struct {
		name     string
		cmd      string
		category string // "" = must not classify
	}{
		// --- false positives from the issue (natural, non-adversarial) ---
		{"git commit mentions npm", `git commit -m "npm test passing"`, ""},
		{"echo cargo", `echo cargo test ok`, ""},
		{"grep cargo", `grep -rn "cargo test" docs/`, ""},
		{"grep make", `grep -rn "make verify" .github/`, ""},
		{"echo make", `echo make check done`, ""},
		{"git commit mentions make", `git commit -m "make verify green"`, ""},
		// --- true positives must still classify ---
		{"npm test", "npm test", "npm-test"},
		{"yarn test", "yarn test", "npm-test"},
		{"pnpm test", "pnpm test -- --grep auth", "npm-test"},
		{"npm run test", "npm run test", "npm-test"},
		{"cargo test", "cargo test", "cargo-test"},
		{"cargo test pkg", "cargo test --lib", "cargo-test"},
		{"make test", "make test", "make-test"},
		{"make check", "make check", "make-test"},
		{"make verify", "make verify", "make-test"},
		{"sudo npm test", "sudo npm test", "npm-test"}, // env prefix stripped by caller
		// --- head word is a different tool: must not classify ---
		{"npmtest word", "npmtest foo", ""},
		{"cargo-build not test", "cargo build", ""},
		{"make build", "make build", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			category, _ := classifyVerificationCommand(tc.cmd)
			if category != tc.category {
				t.Fatalf("classifyVerificationCommand(%q) category = %q, want %q", tc.cmd, category, tc.category)
			}
		})
	}
}
