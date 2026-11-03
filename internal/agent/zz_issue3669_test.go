package agent

import "testing"

func Test3669_Bug1_FindExecVoidsReadOnlyExemption(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{

		{`grep -rn "t.Skip(" .`, true},
		{`find . -name "*.go"`, true},
		{`git grep -n t.Skip`, true},
		{`git log -S "t.Skip("`, true},

		{`find . -name "*.go" -exec sed -i "s/t.Skip(.*if testing.Short()//" {} +`, false},
		{`find . -name "*_test.go" -execdir sed -i "s/t.Skip//" {} \;`, false},
		{`find . -name "*.tmp" -delete`, false},
		{`git grep -l t.Skip | xargs sed -i "s/t.Skip/t.Log/"`, false},
		{`git grep -l t.Skip | /usr/bin/xargs sed -i s/t.Skip//`, false},
	}
	for _, c := range cases {
		if got := isReadOnlySearchCommand(c.cmd); got != c.want {
			t.Errorf("isReadOnlySearchCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func Test3669_Bug2_TestPrefixFiles(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"pkg/test_parser.py", true},
		{"pkg/test-helper.rb", true},
		{"pkg/foo_test.py", true},
		{"pkg/foo_test.go", true},
		{"pkg/test_utils.go", false},
		{"pkg/testing_helpers.py", false},
	}
	for _, c := range tests {
		if got := specGamingIsTestFile(c.path); got != c.want {
			t.Errorf("specGamingIsTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func Test3669_Bug3_GoldenAndTestdataAreTestFiles(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"pkg/testdata/golden.txt", true},
		{"testdata/input.txt", true},
		{"pkg/golden/expected.out", true},
		{"pkg/snap/__snapshots__/main.snap", true},
		{"pkg/out.expected", true},
		{"pkg/out.golden", true},
		{"pkg/fixture_helper.go", false},
		{"pkg/golden_helper.go", false},
	}
	for _, c := range tests {
		if got := specGamingIsTestFile(c.path); got != c.want {
			t.Errorf("specGamingIsTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func Test3669_Bug4_GNUMakefileVariants(t *testing.T) {
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile", "BSDmakefile"} {
		if !isMakefileName(name) {
			t.Errorf("isMakefileName(%q) = false, want true", name)
		}
	}
	if isMakefileName("makefileish.go") {
		t.Error("makefileish.go must not match")
	}
}

func Test3669_Bug4b_AwkMultiGsubMasking(t *testing.T) {

	if !isAwkSkipRemoval(`awk '{gsub(/t\.skip\(/, "t.log(")}'`) {
		t.Error("single clean removal must stay exempt")
	}

	if isAwkSkipRemoval(`awk "{gsub(/x/,\"\");gsub(/assert/,\"t.Skip(\")}"`) {
		t.Error("multi-gsub marker injection must NOT be exempt")
	}

	if isAwkSkipRemoval(`awk "{gsub(/assert/,\"t.Skip(\");gsub(/x/,\"\")}"`) {
		t.Error("any injecting gsub disqualifies regardless of order")
	}
}
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
