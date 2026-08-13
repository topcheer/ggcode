package agent

import (
	"regexp"
	"testing"
)

// Test a11yHasAttr word boundary fix: (?:^|\s)alt= does NOT match data-alt=
func TestA11yHasAttrWordBoundaryBug(t *testing.T) {
	// Test the FIXED regex pattern used in a11yHasAttr
	re := regexp.MustCompile(`(?i)(?:^|\s)alt\s*=`)

	testCases := []struct {
		attrs    string
		expected bool
		reason   string
	}{
		{
			attrs:    ` alt="description"`,
			expected: true,
			reason:   "should match real alt attribute (after whitespace)",
		},
		{
			attrs:    `alt="description"`,
			expected: true,
			reason:   "should match real alt attribute (at start)",
		},
		{
			attrs:    ` data-alt="description"`,
			expected: false,
			reason:   "FIXED: (?:^|\\s) requires whitespace or start, not hyphen",
		},
		{
			attrs:    `data-alt="description"`,
			expected: false,
			reason:   "FIXED: 'data-' is not a word boundary, no match",
		},
		{
			attrs:    ` src="photo.jpg" data-alt="desc"`,
			expected: false,
			reason:   "FIXED: space before 'data', not before 'alt'",
		},
		{
			attrs:    ` aria-label="button"`,
			expected: false,
			reason:   "should not match alt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.attrs, func(t *testing.T) {
			actual := re.MatchString(tc.attrs)
			if actual != tc.expected {
				t.Errorf("attrs=%q: expected=%v, got=%v. Reason: %s", tc.attrs, tc.expected, actual, tc.reason)
			}
		})
	}
}

// Test a11yHasAttr with role attribute - using FIXED pattern
func TestA11yHasAttrRoleWordBoundaryBug(t *testing.T) {
	re := regexp.MustCompile(`(?i)(?:^|\s)role\s*=`)

	testCases := []struct {
		attrs    string
		expected bool
	}{
		{` role="button"`, true},
		{` data-role="button"`, false}, // BUG
		{`data-role="button"`, false},  // BUG
		{` onclick="do()"`, false},
	}

	for _, tc := range testCases {
		if actual := re.MatchString(tc.attrs); actual != tc.expected {
			t.Errorf("attrs=%q: expected=%v, got=%v", tc.attrs, tc.expected, actual)
		}
	}
}

// Test the actual a11yHasAttr function
func TestA11yHasAttrActualFunction(t *testing.T) {
	testCases := []struct {
		name     string
		attrs    string
		attrName string
		expected bool
	}{
		{
			name:     "real alt attribute",
			attrs:    ` alt="text"`,
			attrName: "alt",
			expected: true,
		},
		{
			name:     "BUG: data-alt should not match alt",
			attrs:    ` data-alt="text"`,
			attrName: "alt",
			expected: false, // This will fail - demonstrating the bug
		},
		{
			name:     "real role attribute",
			attrs:    ` role="button"`,
			attrName: "role",
			expected: true,
		},
		{
			name:     "BUG: data-role should not match role",
			attrs:    ` data-role="button"`,
			attrName: "role",
			expected: false, // This will fail - demonstrating the bug
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := a11yHasAttr(tc.attrs, tc.attrName)
			if actual != tc.expected {
				t.Errorf("a11yHasAttr(%q, %q): expected=%v, got=%v", tc.attrs, tc.attrName, tc.expected, actual)
			}
		})
	}
}

// Demonstrate the actual impact: checkMissingAlt bypassed by data-alt
func TestCheckMissingAltBypassedByDataAlt(t *testing.T) {
	// HTML with data-alt but no real alt attribute
	content := `<img data-alt="description" src="photo.jpg">`

	warnings := checkMissingAlt(content)

	// The BUG: checkMissingAlt will return nil (no warnings) because
	// a11yHasAttr thinks data-alt="description" is a valid alt attribute
	if len(warnings) == 0 {
		t.Error("BUG: checkMissingAlt did not detect missing alt attribute because data-alt= was treated as alt=")
	} else {
		t.Log("No bug detected - warnings correctly issued:", warnings)
	}
}

// Demonstrate the actual impact: checkClickableDiv bypassed by data-role
func TestCheckClickableDivBypassedByDataRole(t *testing.T) {
	// HTML with data-role but no real role attribute
	content := `<div onclick="do()" data-role="button">Click me</div>`

	warnings := checkClickableDiv(content)

	// The BUG: checkClickableDiv will return nil (no warnings) because
	// a11yHasAttr thinks data-role="button" is a valid role attribute
	if len(warnings) == 0 {
		t.Error("BUG: checkClickableDiv did not detect missing role/tabindex because data-role= was treated as role=")
	} else {
		t.Log("No bug detected - warnings correctly issued:", warnings)
	}
}
