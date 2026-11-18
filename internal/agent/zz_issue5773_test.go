package agent

import "testing"

// #5773: ghApiArgsAreReadOnly missed gh's real -f long form --raw-field
// (gh 2.x flag pairs are -f/--raw-field and -F/--field), while carrying
// the kubectl-style phantom --field-file. gh api semantics: "The default
// HTTP request method is GET normally and POST if any parameters were
// added", so any field parameter without -X implies a POST. The missing
// long form let implicit-POST remote writes (issue/comment/release
// creation, GraphQL mutations) pass as read-only -> tier None, skipping
// grounding entirely. Space, equals and graphql shapes must all gate.
func TestIrrevGhApiRawFieldLongFormGated(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want bool // true = read-only
	}{
		// the #5773 bypass: --raw-field in every shape must be mutating
		{"raw-field space form creates issue", "gh api repos/OWNER/REPO/issues --raw-field title=hello", false},
		{"raw-field equals form creates issue", "gh api repos/OWNER/REPO/issues --raw-field=title=hello", false},
		{"raw-field graphql mutation space form", "gh api graphql --raw-field query='mutation { addStar(input: {starrableId: \"S_01\"}) { starrable { id } } }'", false},
		{"raw-field graphql mutation equals form", "gh api graphql --raw-field=query='mutation { createIssue(input: {repositoryId: \"R_1\", title: \"t\"}) { issue { number } } }'", false},
		{"raw-field bare flag still carries a payload slot", "gh api repos/OWNER/REPO --raw-field", false},
		{"raw-field with explicit GET stays gated (conservative rule)", "gh api repos/OWNER/REPO -X GET --raw-field k=v", false},
		{"raw-field leading position", "gh api --raw-field title=x repos/OWNER/REPO/issues", false},
		// equivalent shorthand / typed forms stay gated (pair regression lock with #5289)
		{"shorthand -f space form", "gh api repos/OWNER/REPO/issues -f title=hello", false},
		{"typed -F space form", "gh api repos/OWNER/REPO/issues -F title=hello", false},
		{"typed long --field space form", "gh api repos/OWNER/REPO/issues --field title=hello", false},
		{"graphql -f mutation", "gh api graphql -f query='mutation { addStar(input: {starrableId: \"S_01\"}) { starrable { id } } }'", false},
		// read-only baselines must stay read-only
		{"GET default plain path", "gh api repos/OWNER/REPO", true},
		{"explicit GET", "gh api -X GET repos/OWNER/REPO", true},
		{"graphql without any field flag", "gh api -X GET graphql", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := irrevSegmentIsReadOnly(tc.seg); got != tc.want {
				t.Fatalf("irrevSegmentIsReadOnly(%q) = %v, want %v", tc.seg, got, tc.want)
			}
		})
	}
}

// Direct unit pin on the flag table itself so a future reordering cannot
// reintroduce the phantom-only pairing without failing this test.
func TestIrrevGhApiRawFieldInMutatingFlagTable(t *testing.T) {
	for _, seg := range []string{
		"gh api --raw-field",
		"gh api --raw-field=",
		"gh api --slurp",
		"gh api --input",
		"gh api --field-file",
	} {
		if irrevSegmentIsReadOnly(seg) {
			t.Fatalf("irrevSegmentIsReadOnly(%q) = true, want false (field/body flag must gate)", seg)
		}
	}
}
