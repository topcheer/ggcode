package agent

import (
	"strings"
	"testing"
)

func TestCheckPanicSafety_BarePanicInLibraryCode(t *testing.T) {
	oldContent := `package mypkg

func validate(x int) int {
	return x
}
`
	newContent := `package mymypkg

func validate(x int) int {
	if x < 0 {
		panic("negative value")
	}
	return x
}
`
	warnings := checkPanicSafety("src/mypkg/validate.go", oldContent, newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for bare panic() in library code")
	}
	if !strings.Contains(warnings[0], "panic()") {
		t.Errorf("warning should mention panic(), got: %s", warnings[0])
	}
}

func TestCheckPanicSafety_PanicInMainSkipped(t *testing.T) {
	newContent := `package main

func main() {
	panic("boom")
}
`
	warnings := checkPanicSafety("src/main.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for panic in main(), got %d", len(warnings))
	}
}

func TestCheckPanicSafety_PanicInInitSkipped(t *testing.T) {
	newContent := `package mypkg

func init() {
	panic("init failure")
}
`
	warnings := checkPanicSafety("src/mypkg/init.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for panic in init(), got %d", len(warnings))
	}
}

func TestCheckPanicSafety_TestFileSkipped(t *testing.T) {
	newContent := `package mypkg

func helper() {
	panic("test helper panic")
}
`
	warnings := checkPanicSafety("src/mypkg/helper_test.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings in test file, got %d", len(warnings))
	}
}

func TestCheckPanicSafety_CmdDirSkipped(t *testing.T) {
	newContent := `package main

func runThing() {
	panic("should be fine in cmd/")
}
`
	warnings := checkPanicSafety("cmd/myapp/run.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings in cmd/ dir, got %d", len(warnings))
	}
}

func TestCheckPanicSafety_PanicWithRecoverSkipped(t *testing.T) {
	newContent := `package mypkg

func safeCall() {
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()
	panic("caught by recover")
}
`
	warnings := checkPanicSafety("src/mypkg/safe.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when recover() is present, got %d", len(warnings))
	}
}

func TestCheckPanicSafety_DeltaAware(t *testing.T) {
	oldContent := `package mypkg

func check(x int) {
	if x < 0 {
		panic("negative")
	}
}
`
	newContent := `package mypkg

func check(x int) {
	if x < 0 {
		panic("negative")
	}
}

func check2(x int) {
	if x > 100 {
		panic("too big")
	}
}
`
	warnings := checkPanicSafety("src/mypkg/check.go", oldContent, newContent)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 new warning (delta-aware), got %d", len(warnings))
	}
}

func TestCheckPanicSafety_NoPanicNoWarning(t *testing.T) {
	newContent := `package mypkg

func process(x int) error {
	if x < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}
`
	warnings := checkPanicSafety("src/mypkg/process.go", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for code without panic(), got %d", len(warnings))
	}
}

func TestCheckPanicSafety_NonGoFileSkipped(t *testing.T) {
	newContent := `function foo() { throw new Error("panic"); }`
	warnings := checkPanicSafety("src/foo.js", "", newContent)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckPanicSafety_PanicInClosure(t *testing.T) {
	newContent := `package mypkg

var handler = func() {
	panic("closure panic")
}
`
	warnings := checkPanicSafety("src/mypkg/handler.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for panic in closure")
	}
}

func TestCheckPanicSafety_MultipleNewPanics(t *testing.T) {
	newContent := `package mypkg

func a() { panic("a") }
func b() { panic("b") }
func c() { panic("c") }
`
	warnings := checkPanicSafety("src/mypkg/multi.go", "", newContent)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings for 3 new panics, got %d", len(warnings))
	}
}

// fix #239: recover only works within the function frame it is deferred in.
// A panic inside a goroutine is NOT protected by the outer function's recover.
func TestCheckPanicSafety_GoroutinePanicNotCoveredByOuterRecover(t *testing.T) {
	newContent := `package mypkg

func worker() {
	defer func() { recover() }()
	go func() {
		panic("x") // BUG: outer recover does not protect this goroutine
	}()
}
`
	warnings := checkPanicSafety("src/mypkg/worker.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for panic in goroutine not protected by outer recover")
	}
}

// fix #239: a recover inside a goroutine does not protect the outer frame.
func TestCheckPanicSafety_OuterPanicNotCoveredByGoroutineRecover(t *testing.T) {
	newContent := `package mypkg

func worker() {
	go func() {
		defer func() { recover() }()
	}()
	panic("naked") // BUG: goroutine's recover does not protect this frame
}
`
	warnings := checkPanicSafety("src/mypkg/worker.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected warning for outer panic not protected by goroutine recover")
	}
}

// fix #2721: the delta key was the position string (line:col). Inserting
// unrelated lines above a pre-existing, untouched panic shifted its
// posStr, so every such edit re-warned on old panics - violating the
// "only flags panics INTRODUCED by this edit" contract. The key is now
// a content fingerprint (function name + normalized call text, same
// approach as the #1142 pcFingerprint); position is display-only.
func TestCheckPanicSafety_LineShiftDoesNotRewarn(t *testing.T) {
	old := `package mypkg

func f() {
	panic("boom")
}
`
	// One comment line inserted above: the panic shifts from line 4 to 5
	// but its content is untouched - must NOT re-warn.
	new := `package mypkg

// unrelated comment inserted above
func f() {
	panic("boom")
}
`
	warnings := checkPanicSafety("src/mypkg/f.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for line-shifted untouched panic, got %d: %v", len(warnings), warnings)
	}
}

// fix #2721 companion: a genuinely new panic must still warn even when
// lines also shifted.
func TestCheckPanicSafety_NewPanicWarnsDespiteShift(t *testing.T) {
	old := `package mypkg

func f() {
	panic("boom")
}
`
	new := `package mypkg

// unrelated comment
func f() {
	panic("boom")
}

func g() {
	panic("new") // introduced by THIS edit
}
`
	warnings := checkPanicSafety("src/mypkg/f.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning for the newly introduced panic, got %d: %v", len(warnings), warnings)
	}
	// The pre-existing f() panic (line-shifted, same content) must be the
	// suppressed one; the single warning fires on the new g() site. The
	// advisory text carries position only (not argument text), so we
	// assert on the warning count and the f-panic's absence from the key.
	if strings.Contains(warnings[0], "boom") {
		t.Fatalf("warning should not reference the untouched f() panic, got: %v", warnings[0])
	}
}

// fix #2721 companion: changing a panic's argument content (not just its
// position) is a new fingerprint and must warn.
func TestCheckPanicSafety_ChangedArgsRewarn(t *testing.T) {
	old := `package mypkg

func f() {
	panic("boom")
}
`
	new := `package mypkg

func f() {
	panic("boom-rewritten")
}
`
	warnings := checkPanicSafety("src/mypkg/f.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for changed panic argument, got %d: %v", len(warnings), warnings)
	}
}
