package agent

// #3669 probe: spec_gaming detection bypass family.
// Bug 1: find -exec / xargs downstream mutators must void the read-only
// exemption. Bug 2: test_ prefix files are test files. Bug 3: golden/
// testdata files are test files. Bug 4: GNUmakefile variants + multi-gsub
// awk masking.

import "testing"

func Test3669_Bug1_FindExecVoidsReadOnlyExemption(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool // want exemption (true) or NOT exempt (false)
	}{
		// Pure investigation stays exempt.
		{`grep -rn "t.Skip(" .`, true},
		{`find . -name "*.go"`, true},
		{`git grep -n t.Skip`, true},
		{`git log -S "t.Skip("`, true},
		// Downstream mutators void the exemption (#3669 Bug 1).
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
		{"pkg/test_parser.py", true},      // Python standard layout
		{"pkg/test-helper.rb", true},      // kebab variant
		{"pkg/foo_test.py", true},         // suffix form still works
		{"pkg/foo_test.go", true},         // Go untouched
		{"pkg/test_utils.go", false},      // Go source starting with test_ NOT classified
		{"pkg/testing_helpers.py", false}, // prefix is "testing", not "test_"
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
		{"testdata/input.txt", true}, // bare relative form
		{"pkg/golden/expected.out", true},
		{"pkg/snap/__snapshots__/main.snap", true},
		{"pkg/out.expected", true},
		{"pkg/out.golden", true},
		{"pkg/fixture_helper.go", false}, // keyword in a SOURCE filename stays source
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
	// Single legitimate removal stays exempt (lowercase: callers pass the
	// lowercased command, matching the function's existing contract).
	if !isAwkSkipRemoval(`awk '{gsub(/t\.skip\(/, "t.log(")}'`) {
		t.Error("single clean removal must stay exempt")
	}
	// First gsub masks a marker INJECTION in the second (#3669 Bug 4b).
	if isAwkSkipRemoval(`awk "{gsub(/x/,\"\");gsub(/assert/,\"t.Skip(\")}"`) {
		t.Error("multi-gsub marker injection must NOT be exempt")
	}
	// First injects, second removes - also not exempt.
	if isAwkSkipRemoval(`awk "{gsub(/assert/,\"t.Skip(\");gsub(/x/,\"\")}"`) {
		t.Error("any injecting gsub disqualifies regardless of order")
	}
}
