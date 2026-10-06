package agent

import (
	"strings"
	"testing"
)

// zz_issue2734_test.go — jsClassRe must match inheritance forms
// (extends/implements), the dominant class shape in real JS/TS. The old
// `[\{<]`-only pattern silently skipped them, making the duplicate-class
// check dead code for those forms.

func TestIssue2734JsClassReMatchesInheritanceForms(t *testing.T) {
	cases := []string{
		"class Foo {",
		"class Foo extends Bar {",
		"class Foo extends Bar implements Baz {",
		"export default class Foo extends React.Component {",
		"export abstract class Foo extends Base {",
		"export class Foo extends Bar {",
		"class Foo extends NS.Base {",
		"class Foo<T> {",
	}
	for _, src := range cases {
		if !jsClassRe.MatchString(src) {
			t.Errorf("jsClassRe should match %q", src)
		}
		m := jsClassRe.FindStringSubmatch(src)
		if len(m) <= 1 || m[1] != "Foo" {
			t.Errorf("jsClassRe captured wrong name for %q: %v", src, m)
		}
	}
}

func TestIssue2734JsClassReStillExcludesNonClasses(t *testing.T) {
	nonClasses := []string{
		"  class Foo {",    // indented: nested/block-scoped, not top-level
		"const classy = {", // not a class decl
		"// class Foo extends Bar {",
		"let x = MyClass.class Foo", // not line-start class keyword
	}
	for _, src := range nonClasses {
		if jsClassRe.MatchString(src) {
			t.Errorf("jsClassRe should NOT match %q", src)
		}
	}
}

func TestIssue2734DuplicateClassWithExtendsDetected(t *testing.T) {
	// The exact #2734 scenario: agent pastes a second copy of an
	// existing extends-form class -- must now be flagged at write time.
	old := `export class Foo extends Base {
  run() {}
}
`
	new := old + `
export class Foo extends Base {
  run() {}
}
`
	warn := checkDuplicateDeclarations("svc.ts", old, new)
	if warn == "" {
		t.Fatal("duplicate extends-form class not detected")
	}
	if !strings.Contains(warn, `class "Foo"`) {
		t.Errorf("warning should name the duplicated class, got: %s", warn)
	}
}

func TestIssue2734SingleClassWithExtendsNoWarning(t *testing.T) {
	// Legitimate refactor that switches heritage must not warn (count 1 → 1).
	old := "export class Foo extends Base {\n}\n"
	new := "export class Foo extends Other implements Iface {\n}\n"
	if warn := checkDuplicateDeclarations("svc.ts", old, new); warn != "" {
		t.Errorf("no duplicate expected on heritage change, got: %s", warn)
	}
}
