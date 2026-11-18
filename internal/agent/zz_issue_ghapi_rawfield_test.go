package agent

// Regression test: ghApiArgsAreReadOnly must treat every gh api field/body
// flag as mutating, because gh switches the request method to POST-by-default
// as soon as any field parameter is present (gh 2.x: -f/--raw-field,
// -F/--field, --input, --slurp). The mutating list previously carried a
// phantom "--field-file" (not a gh flag) and missed the real long form
// --raw-field, so `gh api <endpoint> --raw-field k=v` - a state-changing
// POST - was classified read-only and escaped the irreversibility gate
// with irrevTierNone.

import "testing"

func TestGhApiRawFieldLongFormGates(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want bool
	}{
		{"plain GET is read-only", "gh api repos/o/r/issues", true},
		{"jq GET is read-only", "gh api repos/o/r/issues --jq .[].title", true},
		{"-f space form gates", "gh api repos/o/r/issues -f title=hello", false},
		{"-f glued form gates", "gh api repos/o/r/issues -ftitle=hello", false},
		{"--field long form gates", "gh api repos/o/r/issues --field title=hello", false},
		{"-F typed field gates", "gh api repos/o/r/issues -F title=hello", false},
		{"--raw-field space form gates", "gh api repos/o/r/issues --raw-field title=hello", false},
		{"--raw-field equals form gates", "gh api repos/o/r/issues --raw-field=title=hello", false},
		{"--raw-field graphql write gates", "gh api graphql --raw-field query=MUTATION", false},
		{"--input body gates", "gh api repos/o/r/rulesets --input file.json", false},
		{"--slurp gates", "gh api --paginate repos/o/r/issues --slurp", false},
		{"explicit -X GET with fields stays conservative-gated", "gh api -X GET search/issues -f q=repo:o/r", false},
		{"-X GET without fields is read-only", "gh api -X GET repos/o/r/issues", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := irrevSegmentIsReadOnly(tc.seg); got != tc.want {
				t.Errorf("irrevSegmentIsReadOnly(%q) = %v, want %v", tc.seg, got, tc.want)
			}
		})
	}
}
