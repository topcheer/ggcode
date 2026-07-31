package agent

import (
	"strings"
	"testing"
)

func TestCheckDelimiterBalance_BalancedTypeScript(t *testing.T) {
	content := `
import { foo } from "bar";

function hello(name: string): void {
	const obj = { a: 1, b: [2, 3] };
	console.log(obj);
}
`
	result := checkDelimiterBalance("test.ts", content)
	if result != "" {
		t.Errorf("expected no warning for balanced TS, got: %s", result)
	}
}

func TestCheckDelimiterBalance_UnclosedBrace(t *testing.T) {
	content := `
function broken() {
	if (true) {
		doSomething();
	// missing closing braces
`
	result := checkDelimiterBalance("test.js", content)
	if result == "" {
		t.Fatal("expected warning for unclosed braces, got empty")
	}
	if !strings.Contains(result, "unclosed") {
		t.Errorf("warning should mention 'unclosed', got: %s", result)
	}
}

func TestCheckDelimiterBalance_ExtraClosingBrace(t *testing.T) {
	content := `
function broken() {
	return 1;
}}
`
	result := checkDelimiterBalance("test.js", content)
	if result == "" {
		t.Fatal("expected warning for extra closing brace, got empty")
	}
	if !strings.Contains(result, "unexpected") {
		t.Errorf("warning should mention 'unexpected', got: %s", result)
	}
}

func TestCheckDelimiterBalance_MismatchedBrackets(t *testing.T) {
	content := `
function broken() {
	const arr = [1, 2, 3);
}
`
	result := checkDelimiterBalance("test.ts", content)
	if result == "" {
		t.Fatal("expected warning for mismatched brackets, got empty")
	}
	if !strings.Contains(result, "does not match") {
		t.Errorf("warning should mention mismatch, got: %s", result)
	}
}

func TestCheckDelimiterBalance_BracketsInStrings(t *testing.T) {
	content := `
const msg = "function() { not real [brackets] }";
const url = "http://example.com/path";
const template = "/* not a comment */";
`
	result := checkDelimiterBalance("test.js", content)
	if result != "" {
		t.Errorf("expected no warning for brackets inside strings, got: %s", result)
	}
}

func TestCheckDelimiterBalance_BracketsInComments(t *testing.T) {
	content := `
// This comment has (unbalanced [brackets
/* Block comment with { unclosed brace */
function real() {
	return 42;
}
`
	result := checkDelimiterBalance("test.ts", content)
	if result != "" {
		t.Errorf("expected no warning for brackets inside comments, got: %s", result)
	}
}

func TestCheckDelimiterBalance_PythonTripleQuotedStrings(t *testing.T) {
	content := `
def foo():
	"""
	This docstring has (unbalanced [brackets and {braces.
	It should NOT trigger a delimiter imbalance warning.
	"""
	return {
		'key': [1, 2, 3],
	}
`
	result := checkDelimiterBalance("test.py", content)
	if result != "" {
		t.Errorf("expected no warning for brackets inside Python triple-quoted strings, got: %s", result)
	}
}

func TestCheckDelimiterBalance_PythonRealImbalance(t *testing.T) {
	content := `
def broken():
	x = [1, 2, 3
	return x
`
	result := checkDelimiterBalance("test.py", content)
	if result == "" {
		t.Fatal("expected warning for unclosed [ in Python, got empty")
	}
}

func TestCheckDelimiterBalance_JSON(t *testing.T) {
	// Valid JSON should pass.
	content := `{"name": "test", "values": [1, 2, {"nested": true}]}`
	result := checkDelimiterBalance("config.json", content)
	if result != "" {
		t.Errorf("expected no warning for valid JSON, got: %s", result)
	}

	// Invalid JSON with unclosed array.
	content = `{"name": "test", "values": [1, 2, 3}`
	result = checkDelimiterBalance("config.json", content)
	if result == "" {
		t.Fatal("expected warning for unclosed array in JSON, got empty")
	}
}

func TestCheckDelimiterBalance_YAML(t *testing.T) {
	content := `
# YAML config
server:
  port: 8080
  routes:
    - path: /api
      handlers: [auth, cors, rate_limit]
`
	result := checkDelimiterBalance("config.yaml", content)
	if result != "" {
		t.Errorf("expected no warning for valid YAML, got: %s", result)
	}
}

func TestCheckDelimiterBalance_Rust(t *testing.T) {
	content := `
fn main() {
	let v = vec![1, 2, 3];
	for i in &v {
		println!("{}", i);
	}
}
`
	result := checkDelimiterBalance("main.rs", content)
	if result != "" {
		t.Errorf("expected no warning for balanced Rust, got: %s", result)
	}
}

func TestCheckDelimiterBalance_RustBroken(t *testing.T) {
	content := `
fn broken() {
	let v = vec![1, 2, 3;
}
`
	result := checkDelimiterBalance("main.rs", content)
	if result == "" {
		t.Fatal("expected warning for Rust with unclosed [, got empty")
	}
}

func TestCheckDelimiterBalance_Dart(t *testing.T) {
	content := `
class Widget {
	final List<Map<String, dynamic>> items;
	
	Widget(this.items);
	
	void build() {
		for (final item in items) {
			print(item);
		}
	}
}
`
	result := checkDelimiterBalance("widget.dart", content)
	if result != "" {
		t.Errorf("expected no warning for balanced Dart, got: %s", result)
	}
}

func TestCheckDelimiterBalance_CSS(t *testing.T) {
	content := `
.container {
	display: flex;
	flex-direction: column;
	@media (max-width: 768px) {
		flex-direction: row;
	}
}
`
	result := checkDelimiterBalance("styles.css", content)
	if result != "" {
		t.Errorf("expected no warning for balanced CSS, got: %s", result)
	}
}

func TestCheckDelimiterBalance_EscapedCharsInStrings(t *testing.T) {
	content := `
const s1 = "It\\'s a test";
const s2 = 'She said "hello"';
const s3 = "Path: C:\\\\Users\\\\test";
const arr = [1, 2, 3];
`
	result := checkDelimiterBalance("test.js", content)
	if result != "" {
		t.Errorf("expected no warning for escaped chars in strings, got: %s", result)
	}
}

func TestCheckDelimiterBalance_BacktickStrings(t *testing.T) {
	content := "const msg = `Hello ${name}`;\nconst obj = { value: 42 };\n"
	result := checkDelimiterBalance("test.ts", content)
	if result != "" {
		t.Errorf("expected no warning for template literals, got: %s", result)
	}
}

func TestCheckDelimiterBalance_GoFileSkipped(t *testing.T) {
	// Go files should be skipped (go/parser handles them).
	content := `package main

func broken() {
	if true {
`
	result := checkDelimiterBalance("main.go", content)
	if result != "" {
		t.Errorf("expected no delimiter check for Go files, got: %s", result)
	}
}

func TestCheckDelimiterBalance_EmptyFile(t *testing.T) {
	result := checkDelimiterBalance("empty.js", "")
	if result != "" {
		t.Errorf("expected no warning for empty file, got: %s", result)
	}
}

func TestCheckDelimiterBalance_UnknownExtension(t *testing.T) {
	content := "This has { unclosed [ brackets"
	result := checkDelimiterBalance("readme.md", content)
	if result != "" {
		t.Errorf("expected no check for .md files, got: %s", result)
	}
}

func TestCheckDelimiterBalance_LineNumberInError(t *testing.T) {
	content := `line1;
line2;
function broken( {
	// missing )
}
`
	result := checkDelimiterBalance("test.js", content)
	if result == "" {
		t.Fatal("expected warning, got empty")
	}
	if !strings.Contains(result, "line 3") {
		t.Errorf("warning should reference line 3, got: %s", result)
	}
}

func TestScanDelimiters_NestedBrackets(t *testing.T) {
	content := `const x = [[[{ "a": [1, 2] }]]];`
	result := scanDelimiters(content, commentStyle{lineComments: []string{"//"}, blockOpen: "/*", blockClose: "*/"})
	if result != "" {
		t.Errorf("expected no warning for deeply nested balanced brackets, got: %s", result)
	}
}

func TestScanDelimiters_BlockCommentWithBrackets(t *testing.T) {
	style := commentStyle{lineComments: []string{"//"}, blockOpen: "/*", blockClose: "*/"}
	content := `/* ( { [ */ const x = [1]; /* } ) ] */`
	result := scanDelimiters(content, style)
	if result != "" {
		t.Errorf("expected no warning for brackets in block comments, got: %s", result)
	}
}

func TestCheckDelimiterBalance_PythonHashInString(t *testing.T) {
	content := `
x = "not a # comment"
y = [1, 2, 3]
`
	result := checkDelimiterBalance("test.py", content)
	if result != "" {
		t.Errorf("expected no warning for # inside Python string, got: %s", result)
	}
}

func TestCheckDelimiterBalance_JavaComplex(t *testing.T) {
	content := `
public class Example {
	private Map<String, List<Integer>> data;
	
	public void process(List<String> items) {
		for (String item : items) {
			if (item.startsWith("test")) {
				data.computeIfAbsent(item, k -> new ArrayList<>()).add(1);
			}
		}
	}
}
`
	result := checkDelimiterBalance("Example.java", content)
	if result != "" {
		t.Errorf("expected no warning for balanced Java, got: %s", result)
	}
}
