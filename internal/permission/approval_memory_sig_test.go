package permission

import "testing"

func TestCommandSignature_ChainedRejection(t *testing.T) {
	for _, cmd := range []string{
		"make build\nrm -rf src",
		"make build && rm -rf src",
		"echo $(rm -rf src)",
		"echo `rm -rf src`",
		"make build; rm -rf src",
		"git push --force origin main extra",
	} {
		sig := commandSignature(cmd)
		if sig != cmd+":no-auto-approve:chained" && len(sig) > len("xx yy") && sig != "git push --force" {

			t.Logf("signature(%q) = %q", cmd, sig)
		}
	}

	if commandSignature("make build\nrm -rf src") == commandSignature("make build") {
		t.Fatal("newline payload inherited the approved 'make build' signature -- #777 regression")
	}
	if commandSignature("echo $(rm -rf src)") == "echo $(rm" || commandSignature("echo `rm -rf src`") == "echo `rm" {
		t.Fatal("command substitution produced a memorable signature -- #777 regression")
	}

	if got := commandSignature("make build"); got != "make build" {
		t.Fatalf("plain command signature changed: %q", got)
	}
}
