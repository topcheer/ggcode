package agent

import (
	"strings"
	"testing"
)

// zz_issue2821_test.go - probe for #2821: TypeScript top-level overload
// signatures (no body, ';' terminator) are legal and must not be counted as
// duplicate declarations; .d.ts files (all-overload idiom) are skipped
// entirely. Only 2+ implementations (both with bodies) are real duplicates.
func TestIssue2821TSOverloadsNotFlagged(t *testing.T) {
	// Agent adds overloads to an existing function: 1 impl before, 2 sigs +
	// 1 impl after. Legal TS - must NOT warn.
	old := "export function parse(s: string): Date {\n\treturn new Date(s)\n}\n"
	new1 := "export function parse(s: string): Date;\n" +
		"export function parse(y: number, m: number, d: number): Date;\n" +
		"export function parse(...args: any[]): Date {\n\treturn new Date(args.join(\"-\"))\n}\n"
	if msg := checkDuplicateDeclarations("src/parse.ts", old, new1); msg != "" {
		t.Errorf("#2821 legal overload signatures flagged as duplicates:\n%s", msg)
	}

	// .d.ts: overload pairs are the only legal form - never warn.
	dts := "export function parse(s: string): Date;\nexport function parse(y: number, m: number, d: number): Date;\n"
	if msg := checkDuplicateDeclarations("src/parse.d.ts", "", dts); msg != "" {
		t.Errorf("#2821 .d.ts overload pairs flagged:\n%s", msg)
	}

	// Real duplicate: two implementations with bodies - must still warn.
	dupOld := "export function run(): void {\n\tconsole.log(1)\n}\n"
	dupNew := "export function run(): void {\n\tconsole.log(1)\n}\nexport function run(): void {\n\tconsole.log(2)\n}\n"
	msg := checkDuplicateDeclarations("src/run.ts", dupOld, dupNew)
	if msg == "" || !strings.Contains(msg, "run") {
		t.Errorf("#2821 real duplicate implementations (both with bodies) not flagged")
	}

	// Plain JS duplicate (no return-type annotation) still flagged.
	jsNew := "function go() {\n\treturn 1\n}\nfunction go() {\n\treturn 2\n}\n"
	if msg := checkDuplicateDeclarations("src/go.js", "", jsNew); msg == "" {
		t.Errorf("#2821 plain JS duplicate functions no longer flagged")
	}

	// Overload + impl with nested parens in params (default values) still parses.
	nested := "export function f(a = g(1, 2)): void {\n\treturn\n}\n"
	if msg := checkDuplicateDeclarations("src/n.ts", "", nested); msg != "" {
		t.Errorf("#2821 single impl with nested-paren default param misparsed as duplicate:\n%s", msg)
	}
}
