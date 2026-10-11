package agent

// #3669 probe: the read-only search exemption must not fire when the
// command carries a downstream mutation - `find -exec sed -i`, `-ok`, a
// pipe into xargs, a redirect, or a chained command. The verb lookup alone
// exempted `find . -name '*.go' -exec sed -i "s/t.Skip(//"` wholesale.

import "testing"

func TestIssue3669_SearchExemptionMutationCarriers(t *testing.T) {
	mutating := []string{
		`find . -name "*.go" -exec sed -i "s/t.Skip(.*if testing.Short()//" {} +`,
		`find . -type f -ok rm {} \;`,
		`grep -rl "t.Skip(" . | xargs sed -i "s/t.Skip(/t.Log(/"`,
		`grep foo bar.txt > out.txt`,
		`grep foo bar.txt && sed -i s/a/b/ f`,
		`find . -name x -execdir sed -i s/a/b/ {} ;`,
	}
	for _, cmd := range mutating {
		if isReadOnlySearchCommand(cmd) {
			t.Fatalf("mutation-carrying command must NOT get the read-only exemption: %s", cmd)
		}
	}
	benign := []string{
		`grep -rn "t.Skip(" internal/`,
		`rg "foo(bar)" .`,
		`git grep -n "t.Skip("`,
		`git log -S"t.Skip(" --oneline`,
		`find . -name "*.go"`,
		`find . -type f -print`,
		"FOO=1 grep foo bar",
	}
	for _, cmd := range benign {
		if !isReadOnlySearchCommand(cmd) {
			t.Fatalf("plain read-only search must keep the exemption: %s", cmd)
		}
	}
}
