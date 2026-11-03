package agent

import (
	"strings"
	"testing"
)

func TestIssue572_SuppressionScopedAllowsRuleNumber(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("suppression-directives")
	if c == nil {
		t.Fatal("suppression-directives not registered")
	}

	testCases := []struct {
		name       string
		filePath   string
		lang       Language
		oldContent string
		newContent string
		shouldFire bool
	}{
		{
			name:       "Go nolint with specific rule (should NOT fire)",
			filePath:   "foo.go",
			lang:       LangGo,
			oldContent: "func f() {}",
			newContent: "func f() {} //nolint:errcheck",
			shouldFire: false,
		},
		{
			name:       "Go nolint bare (should fire)",
			filePath:   "foo.go",
			lang:       LangGo,
			oldContent: "func f() {}",
			newContent: "func f() {} //nolint",
			shouldFire: true,
		},
		{
			name:       "Python noqa with code (should NOT fire)",
			filePath:   "foo.py",
			lang:       LangPython,
			oldContent: "def f(): pass",
			newContent: "def f(): pass  # noqa: E501",
			shouldFire: false,
		},
		{
			name:       "Python noqa bare (should fire)",
			filePath:   "foo.py",
			lang:       LangPython,
			oldContent: "def f(): pass",
			newContent: "def f(): pass  # noqa",
			shouldFire: true,
		},
		{
			name:       "Python type:ignore with comment (should NOT fire)",
			filePath:   "foo.py",
			lang:       LangPython,
			oldContent: "def f(): pass",
			newContent: "def f(): x: int = 'foo'  # type: ignore[assignment]",
			shouldFire: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := c.Run(CheckContext{
				FilePath:   tc.filePath,
				OldContent: tc.oldContent,
				NewContent: tc.newContent,
				Lang:       tc.lang,
			})
			if tc.shouldFire && len(warnings) == 0 {
				t.Errorf("Expected warnings but got none")
			}
			if !tc.shouldFire && len(warnings) > 0 {
				t.Errorf("Unexpected warnings: %v", warnings)
			}
		})
	}
}

func TestIssue572_SuppressionMarkdownProseIgnored(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("suppression-directives")
	if c == nil {
		t.Fatal("suppression-directives not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "README.md",
		OldContent: "",
		NewContent: "You can use //nolint to suppress warnings",
		Lang:       LangAny,
	})
	if len(warnings) > 0 {
		t.Errorf("Prose mention in Markdown should not fire, got: %v", warnings)
	}

	warnings = c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: "",
		NewContent: "func f() {} //nolint",
		Lang:       LangGo,
	})
	if len(warnings) == 0 {
		t.Error("Bare //nolint in Go file should fire")
	}
}

func TestIssue572_SuppressionLangAnyIgnored(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("suppression-directives")
	if c == nil {
		t.Fatal("suppression-directives not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "foo.unknown",
		OldContent: "",
		NewContent: "# rubocop:disable all",
		Lang:       0,
	})
	if len(warnings) > 0 {
		t.Errorf("Language-bound pattern should not fire on unknown extension, got: %v", warnings)
	}
}

func TestIssue572_PlaceholderLineDriftFixed(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("placeholder-code")
	if c == nil {
		t.Fatal("placeholder-code not registered")
	}

	oldContent := `package foo

func f() {
	// TODO: implement this
	fmt.Println("hello")
}`

	newContent := `package foo

const x = 1

func f() {
	// TODO: implement this
	fmt.Println("hello")
}`

	warnings := c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: oldContent,
		NewContent: newContent,
		Lang:       LangGo,
	})
	if len(warnings) > 0 {
		t.Errorf("Pre-existing TODO with line drift should not fire, got: %v", warnings)
	}

	newContentWithNewTodo := `package foo

func f() {
	// TODO: implement this
	// TODO: implement this logic here
	fmt.Println("hello")
}`

	warnings = c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: oldContent,
		NewContent: newContentWithNewTodo,
		Lang:       LangGo,
	})
	if len(warnings) == 0 {
		t.Error("New vague TODO should fire")
	}
}

func TestIssue572_HardcodedDataTablesAllowed(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("hardcoded-output")
	if c == nil {
		t.Fatal("hardcoded-output not registered")
	}

	testCases := []struct {
		name       string
		newContent string
		shouldFire bool
	}{
		{
			name: "monthNames table should NOT fire",
			newContent: `package foo

var monthNames = []string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}`,
			shouldFire: false,
		},
		{
			name: "httpStatusText table should NOT fire",
			newContent: `package foo

var httpStatusText = map[int]string{
	200: "OK",
	201: "Created",
	204: "No Content",
	400: "Bad Request",
	404: "Not Found",
	500: "Internal Server Error",
}`,
			shouldFire: false,
		},
		{
			name: "currencySymbols table should NOT fire",
			newContent: `package foo

var currencySymbols = map[string]string{
	"USD": "$",
	"EUR": "€",
	"GBP": "£",
	"JPY": "¥",
	"CNY": "¥",
}`,
			shouldFire: false,
		},
		{
			name: "Lookup table with function name should still fire",
			newContent: `func process(input string) string {
	m := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
		"d": "4",
		"e": "5",
	}
	return m[input]
}`,
			shouldFire: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := c.Run(CheckContext{
				FilePath:   "foo.go",
				OldContent: "",
				NewContent: tc.newContent,
				Lang:       LangGo,
			})
			if tc.shouldFire && len(warnings) == 0 {
				t.Error("Expected warnings but got none")
			}
			if !tc.shouldFire && len(warnings) > 0 {
				t.Errorf("Unexpected warnings: %v", warnings)
			}
		})
	}
}

func TestIssue572_HardcodedPythonSingleQuoteFixed(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("hardcoded-output")
	if c == nil {
		t.Fatal("hardcoded-output not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "foo.py",
		OldContent: "",
		NewContent: `LOOKUP = {
	'input1': 'output1',
	'input2': 'output2',
	'input3': 'output3',
	'input4': 'output4',
	'input5': 'output5',
}`,
		Lang: LangPython,
	})
	if len(warnings) == 0 {
		t.Error("Python single-quoted dict should fire")
	}
	if !strings.Contains(strings.Join(warnings, " "), "hardcoded") {
		t.Error("Warning should mention 'hardcoded'")
	}
}

func TestIssue572_AssertionSkipAllowed(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("assertion-presence")
	if c == nil {
		t.Fatal("assertion-presence not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "foo_test.go",
		OldContent: "",
		NewContent: `package foo

import "testing"

func TestRequiresDocker(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" {
		t.Skip("requires docker")
	}
	// ... actual test logic
}`,
		Lang: LangGo,
	})
	if len(warnings) > 0 {
		t.Errorf("t.Skip should not be flagged as hollow test, got: %v", warnings)
	}

	warnings = c.Run(CheckContext{
		FilePath:   "foo_test.go",
		OldContent: "",
		NewContent: `package foo

import "testing"

func TestRequiresDockerf(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" {
		t.Skipf("requires docker: %s", "not available")
	}
}`,
		Lang: LangGo,
	})
	if len(warnings) > 0 {
		t.Errorf("t.Skipf should not be flagged as hollow test, got: %v", warnings)
	}
}

func TestIssue572_AssertionExpectRecognized(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("assertion-presence")
	if c == nil {
		t.Fatal("assertion-presence not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "foo_test.go",
		OldContent: "",
		NewContent: `package foo

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestWithGomega(t *testing.T) {
	g := NewWithT(t)
	g.Expect(1).To(Equal(1))
}`,
		Lang: LangGo,
	})
	if len(warnings) > 0 {
		t.Errorf("gomega Expect().To() should be recognized, got: %v", warnings)
	}
}

func TestIssue572_AssertionTPStillFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("assertion-presence")
	if c == nil {
		t.Fatal("assertion-presence not registered")
	}

	warnings := c.Run(CheckContext{
		FilePath:   "foo_test.go",
		OldContent: "",
		NewContent: `package foo

import "testing"

func TestDiscardsData(t *testing.T) {
	result := someFunction()
	// Data is constructed but never verified
}`,
		Lang: LangGo,
	})
	if len(warnings) == 0 {
		t.Error("Hollow test without assertions should fire")
	}

	warnings = c.Run(CheckContext{
		FilePath: "foo_test.go",
		OldContent: `package foo

import "testing"

func TestRegression(t *testing.T) {
	result := someFunction()
	if result != expected {
		t.Errorf("got %v, want %v", result, expected)
	}
}`,
		NewContent: `package foo

import "testing"

func TestRegression(t *testing.T) {
	result := someFunction()
	// Assertion removed - hollow test
}`,
		Lang: LangGo,
	})
	if len(warnings) == 0 {
		t.Error("Regression (assertion removal) should fire")
	}
}

func TestIssue572_RegressionIssue571TestsStillPass(t *testing.T) {

	t.Run("Issue571_HardcodedOutputFires", TestIssue571_HardcodedOutputFires)
	t.Run("Issue571_SuppressionDirectivesFires", TestIssue571_SuppressionDirectivesFires)
	t.Run("Issue571_PlaceholderCodeFires", TestIssue571_PlaceholderCodeFires)
	t.Run("Issue571_AssertionPresenceFires", TestIssue571_AssertionPresenceFires)
}
