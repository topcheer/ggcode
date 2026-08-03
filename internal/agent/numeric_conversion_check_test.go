package agent

import (
	"strings"
	"testing"
)

func TestCheckUnsafeNumericConversion_NonGoFile(t *testing.T) {
	result := checkUnsafeNumericConversion("test.py", "", "x = 1")
	if result != "" {
		t.Errorf("expected empty result for non-Go file, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_EmptyContent(t *testing.T) {
	result := checkUnsafeNumericConversion("test.go", "", "")
	if result != "" {
		t.Errorf("expected empty result for empty content")
	}
}

func TestCheckUnsafeNumericConversion_SyntaxError(t *testing.T) {
	result := checkUnsafeNumericConversion("test.go", "", "package main\n\nfunc {")
	if result != "" {
		t.Errorf("expected empty result for syntax error")
	}
}

func TestCheckUnsafeNumericConversion_NarrowingFromLen(t *testing.T) {
	src := `package main

func process(data []byte) {
	size := int32(len(data))
	_ = size
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for int32(len(...)) conversion")
	}
	if !strings.Contains(result, "truncates") {
		t.Errorf("expected truncation warning, got: %s", result)
	}
	if !strings.Contains(result, "int32") {
		t.Errorf("expected int32 mention, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_Uint8FromCount(t *testing.T) {
	src := `package main

func process(itemCount int) {
	b := uint8(itemCount)
	_ = b
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for uint8(count-like variable)")
	}
	if !strings.Contains(result, "uint8") {
		t.Errorf("expected uint8 mention, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DurationBareLiteral(t *testing.T) {
	src := `package main

import "time"

func wait() {
	time.Sleep(5)
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for time.Sleep(5)")
	}
	if !strings.Contains(result, "nanoseconds") {
		t.Errorf("expected nanoseconds warning, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DurationVariable(t *testing.T) {
	src := `package main

import "time"

func wait(timeoutMs int) {
	time.Sleep(timeoutMs)
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for time.Sleep(timeoutMs)")
	}
	if !strings.Contains(result, "timeoutMs") {
		t.Errorf("expected timeoutMs mention, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DurationCorrectUsage(t *testing.T) {
	src := `package main

import "time"

func wait() {
	time.Sleep(5 * time.Second)
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for correct Duration usage, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_LiteralOverflow(t *testing.T) {
	src := `package main

func process() {
	x := uint8(300)
	_ = x
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for uint8(300)")
	}
	if !strings.Contains(result, "truncates") {
		t.Errorf("expected truncation warning, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_LiteralFits(t *testing.T) {
	src := `package main

func process() {
	x := uint8(200)
	_ = x
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for uint8(200) (fits), got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DeltaAware(t *testing.T) {
	old := `package main

func process(data []byte) {
	size := int32(len(data))
	_ = size
}
`
	// Same content - delta should suppress warning
	result := checkUnsafeNumericConversion("test.go", old, old)
	if result != "" {
		t.Errorf("expected no warning for pre-existing conversion (delta-aware), got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_NoNarrowing(t *testing.T) {
	src := `package main

func process(x int) {
	y := int64(x)
	_ = y
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for widening conversion, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_BinaryExpr(t *testing.T) {
	src := `package main

func process(a, b int) {
	result := int32(a * b)
	_ = result
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for int32(arithmetic)")
	}
	if !strings.Contains(result, "arithmetic") {
		t.Errorf("expected arithmetic warning, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DurationArithmetic(t *testing.T) {
	src := `package main

import "time"

func poll(seconds int) {
	time.Tick(seconds * 1000)
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result == "" {
		t.Fatal("expected warning for time.Tick(non-Duration arithmetic)")
	}
	if !strings.Contains(result, "Duration") {
		t.Errorf("expected Duration suggestion, got: %s", result)
	}
}

func TestCheckUnsafeNumericConversion_DurationArithmeticWithDurationType(t *testing.T) {
	src := `package main

import "time"

func poll() {
	time.Sleep(time.Duration(5) * time.Second)
}
`
	result := checkUnsafeNumericConversion("test.go", "", src)
	if result != "" {
		t.Errorf("expected no warning for correct Duration arithmetic, got: %s", result)
	}
}

func TestFitsInTargetType(t *testing.T) {
	tests := []struct {
		literal    string
		targetType string
		fits       bool
	}{
		{"255", "uint8", true},
		{"256", "uint8", false},
		{"127", "int8", true},
		{"128", "int8", false},
		{"32767", "int16", true},
		{"32768", "int16", false},
		{"65535", "uint16", true},
		{"65536", "uint16", false},
		{"2147483647", "int32", true},
		{"2147483648", "int32", false},
		{"0xFF", "uint8", true},
		{"0xFFF", "uint8", false},
		{"100", "int64", true},
	}
	for _, tt := range tests {
		got := fitsInTargetType(tt.literal, tt.targetType)
		if got != tt.fits {
			t.Errorf("fitsInTargetType(%q, %q) = %v, want %v", tt.literal, tt.targetType, got, tt.fits)
		}
	}
}

func TestIsLargeValueIdentifier(t *testing.T) {
	positive := []string{"count", "totalCount", "byteCount", "offset", "index", "portNum", "timestamp"}
	for _, name := range positive {
		if !isLargeValueIdentifier(name) {
			t.Errorf("isLargeValueIdentifier(%q) = false, want true", name)
		}
	}
	negative := []string{"x", "y", "tmp", "foo", "bar", "result"}
	for _, name := range negative {
		if isLargeValueIdentifier(name) {
			t.Errorf("isLargeValueIdentifier(%q) = true, want false", name)
		}
	}
}

func TestIsLikelyDurationMismatch(t *testing.T) {
	positive := []string{"timeout", "delay", "seconds", "delayMs", "poll_interval", "wait"}
	for _, name := range positive {
		if !isLikelyDurationMismatch(name) {
			t.Errorf("isLikelyDurationMismatch(%q) = false, want true", name)
		}
	}
	negative := []string{"duration", "d", "x", "foo"}
	for _, name := range negative {
		if isLikelyDurationMismatch(name) {
			t.Errorf("isLikelyDurationMismatch(%q) = true, want false", name)
		}
	}
}
