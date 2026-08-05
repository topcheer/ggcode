package agent

// Unsafe Numeric Type Conversion Detection in Go Code
//
// Problem: AI coding agents frequently write unsafe numeric type conversions
// that silently truncate data, cause runtime panics, or produce incorrect
// results. The most common patterns are:
//
//  1. Narrowing conversions without bounds checking:
//       x := int32(len(slice))     // truncates if len > 2^31-1
//       b := uint8(byteCount)      // wraps if byteCount > 255
//       port := int16(portNum)     // wraps if portNum > 32767
//
//  2. Arithmetic overflow without overflow check:
//       result := a * b            // can overflow silently for int32/uint32
//       sum := x + y               // can overflow
//
//  3. Duration / time unit confusion:
//       time.Sleep(timeout)        // timeout is int, not time.Duration
//       d := 5                      // should be 5 * time.Second
//       time.After(delay)          // int treated as nanoseconds
//
//  4. Float-to-integer truncation without rounding:
//       n := int(floatVal)         // truncates toward zero, not rounds
//
// These bugs are particularly insidious because:
//   - Go does NOT panic on integer overflow (it wraps silently)
//   - Narrowing conversions silently truncate (no runtime error)
//   - These bugs pass compilation and basic tests, only manifesting
//     with large inputs, long uptimes, or specific edge cases
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on gosec/go vet integration)
//   - Cursor: golangci-lint may catch some via gosec G109 (int → int32)
//   - Cline/OpenHands: reactive only (caught by production failures)
//   - Aider: no automatic detection
//   - SafeSQL/gosec: G109 catches some int-to-string conversions, but NOT
//     general narrowing conversions or duration confusion
//
// go vet does NOT detect unsafe numeric conversions. gosec G109 only covers
// int → string via strconv, not general narrowing. This check provides
// zero-dependency AST-based detection at write time.
//
// Approach: AST-based analysis of Go files. For each function body:
//  - Find TypeAssert / CallExpr that convert to narrower integer types
//  - Detect time.Sleep/After/Tick calls with non-Duration-typed arguments
//  - Detect float-to-int conversions without explicit rounding
//  - Flag narrowing conversions in contexts where the source could be large
//    (len(), cap(), runtime metrics, etc.)
//
// Delta-aware: only flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// numericConvInstance represents a detected unsafe numeric conversion.
type numericConvInstance struct {
	posStr     string // human-readable position
	pattern    string // description of the unsafe pattern
	suggestion string // safe alternative
}

// floatTypes are Go floating-point types.
var floatTypes = map[string]bool{
	"float32": true,
	"float64": true,
}

// unsignedTypes maps unsigned integer type names.
var unsignedTypes = map[string]bool{
	"uint":    true,
	"uint8":   true,
	"uint16":  true,
	"uint32":  true,
	"uint64":  true,
	"uintptr": true,
	"byte":    true,
}

// signedTypes maps signed integer type names.
var signedTypes = map[string]bool{
	"int":   true,
	"int8":  true,
	"int16": true,
	"int32": true,
	"int64": true,
}

// narrowerTypes defines integer types that lose bits compared to int/int64.
var narrowerIntTypes = map[string]bool{
	"int8":   true,
	"int16":  true,
	"int32":  true,
	"uint8":  true,
	"uint16": true,
	"uint32": true,
	"byte":   true, // alias for uint8
}

// sizeFuncs are functions that return potentially large values.
// Converting their results to narrower types is always suspicious.
var sizeFuncs = map[string]bool{
	"len":   true,
	"cap":   true,
	"count": true,
	"size":  true,
	"Size":  true,
}

// collectAllConvChecks runs all numeric conversion checks on a single CallExpr.
func collectAllConvChecks(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var result []numericConvInstance
	result = append(result, checkNarrowingConversion(fset, call)...)
	result = append(result, checkDurationConfusion(fset, call)...)
	result = append(result, checkStringIntConversion(fset, call)...)
	result = append(result, checkSignedUnsignedConversion(fset, call)...)
	result = append(result, checkFloatToIntConversion(fset, call)...)
	return result
}

// buildOldConversionSet parses oldContent and returns a set of pre-existing
// conversion patterns to suppress in delta-aware mode.
func buildOldConversionSet(filePath, oldContent string) map[string]bool {
	if strings.TrimSpace(oldContent) == "" {
		return nil
	}
	oldFset := token.NewFileSet()
	oldFile, err := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
	if err != nil {
		return nil
	}
	var oldInstances []numericConvInstance
	ast.Inspect(oldFile, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			oldInstances = append(oldInstances, collectAllConvChecks(oldFset, call)...)
		}
		return true
	})
	oldSet := make(map[string]bool, len(oldInstances))
	for _, oi := range oldInstances {
		oldSet[oi.pattern] = true
	}
	return oldSet
}

// checkUnsafeNumericConversion detects unsafe numeric type conversions in Go code.
// Delta-aware: only flags patterns newly introduced by this edit.
func checkUnsafeNumericConversion(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	var instances []numericConvInstance

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			// Check for type conversion expressions like int32(x), uint8(x)
			instances = append(instances, checkNarrowingConversion(fset, node)...)
			// Check for string(int) - produces rune bytes, not number string
			instances = append(instances, checkStringIntConversion(fset, node)...)
			// Check for signed<->unsigned conversions
			instances = append(instances, checkSignedUnsignedConversion(fset, node)...)
			// Check for float-to-int truncation without rounding
			instances = append(instances, checkFloatToIntConversion(fset, node)...)
		case *ast.SelectorExpr:
			// Check for time.Sleep(x), time.After(x) with non-Duration args
			// handled at the call level
		}
		return true
	})

	// Check time package calls separately for duration confusion
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			instances = append(instances, checkDurationConfusion(fset, call)...)
		}
		return true
	})

	if len(instances) == 0 {
		return ""
	}

	// Delta check: suppress pre-existing warnings.
	oldSet := buildOldConversionSet(filePath, oldContent)

	var newInstances []numericConvInstance
	for _, inst := range instances {
		if oldSet == nil || !oldSet[inst.pattern] {
			newInstances = append(newInstances, inst)
		}
	}

	if len(newInstances) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Unsafe numeric conversion] The following conversion(s) may silently truncate, overflow, or produce incorrect results:\n")
	for _, inst := range newInstances {
		b.WriteString(fmt.Sprintf("  - %s: %s %s\n", inst.posStr, inst.pattern, inst.suggestion))
	}
	b.WriteString("Note: Go does NOT panic on integer overflow (wraps silently). Use math.MaxInt32 etc. for bounds checks before narrowing conversions.")
	return b.String()
}

// checkNarrowingConversion inspects a CallExpr for narrowing type conversions.
func checkNarrowingConversion(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var instances []numericConvInstance

	// A type conversion in Go looks like: DestType(arg)
	// In AST: CallExpr with Fun = *ast.Ident (type name) and one arg.
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	if !narrowerIntTypes[ident.Name] {
		return nil
	}
	if len(call.Args) != 1 {
		return nil
	}

	posStr := fset.Position(call.Pos()).String()
	arg := call.Args[0]

	pattern, suggestion := analyzeConversionArg(ident.Name, arg)
	if pattern == "" {
		return nil
	}

	instances = append(instances, numericConvInstance{
		posStr:     posStr,
		pattern:    pattern,
		suggestion: suggestion,
	})
	return instances
}

// analyzeConversionArg determines if the conversion argument is risky.
func analyzeConversionArg(targetType string, arg ast.Expr) (pattern, suggestion string) {
	switch a := arg.(type) {
	case *ast.CallExpr:
		// Converting result of len(), cap(), etc. to narrower type
		if fnIdent, ok := a.Fun.(*ast.Ident); ok {
			if sizeFuncs[fnIdent.Name] {
				return fmt.Sprintf("%s(%s(...)) silently truncates if the size exceeds %s range", targetType, fnIdent.Name, targetType),
					fmt.Sprintf("-> Add bounds check: if v := %s(...); v > math.Max%s { ... }", fnIdent.Name, targetType)
			}
		}
		// Converting result of a function call - potentially large
		return fmt.Sprintf("%s(funcResult) may truncate if the value exceeds %s range", targetType, targetType),
			"-> Add a bounds check before conversion, or use the wider type directly."

	case *ast.Ident:
		// Converting a variable - check if it's likely large.
		// Flag identifiers that suggest large values (count, total, size, etc.)
		lname := strings.ToLower(a.Name)
		if isLargeValueIdentifier(lname) {
			return fmt.Sprintf("%s(%s) may truncate if '%s' exceeds %s range", targetType, a.Name, a.Name, targetType),
				fmt.Sprintf("-> Add bounds check before conversion: if %s > math.Max%s { return error }", a.Name, targetType)
		}

	case *ast.BinaryExpr:
		// Converting arithmetic expression result - potential overflow
		return fmt.Sprintf("%s(arithmetic expression) may truncate if the result exceeds %s range", targetType, targetType),
			"-> Check for overflow before narrowing conversion."

	case *ast.BasicLit:
		// Converting a literal - check if it fits
		if a.Kind == token.INT {
			if !fitsInTargetType(a.Value, targetType) {
				return fmt.Sprintf("%s(%s) truncates: literal does not fit in %s range", targetType, a.Value, targetType),
					"-> Use a wider type or reduce the value."
			}
		}
	}

	return "", ""
}

// isLargeValueIdentifier checks if a variable name suggests it may hold large values.
func isLargeValueIdentifier(name string) bool {
	largeHints := []string{"count", "total", "size", "len", "offset", "index", "num", "byte", "port", "sequence", "seq", "timestamp", "ts", "counter"}
	for _, lh := range largeHints {
		if strings.Contains(name, lh) {
			return true
		}
	}
	return false
}

// fitsInTargetType checks if an integer literal string fits in the target type.
func fitsInTargetType(literal, targetType string) bool {
	cleaned := strings.ReplaceAll(literal, "_", "")

	var maxVal int64
	switch targetType {
	case "int8":
		maxVal = 127
	case "uint8", "byte":
		maxVal = 255
	case "int16":
		maxVal = 32767
	case "uint16":
		maxVal = 65535
	case "int32":
		maxVal = 2147483647
	case "uint32":
		maxVal = 4294967295
	default:
		return true
	}

	// Parse the literal value and compare against maxVal.
	var val int64
	if strings.HasPrefix(cleaned, "0x") || strings.HasPrefix(cleaned, "0X") {
		for _, ch := range cleaned[2:] {
			d := hexDigitVal(ch)
			if d < 0 {
				return true // unparseable, skip
			}
			val = val*16 + d
			if val > maxVal {
				return false
			}
		}
	} else {
		for _, ch := range cleaned {
			if ch < '0' || ch > '9' {
				return true // unparseable, skip
			}
			val = val*10 + int64(ch-'0')
			if val > maxVal {
				return false
			}
		}
	}
	return true
}

// hexDigitVal converts a hex character to its integer value, or -1 if invalid.
func hexDigitVal(ch rune) int64 {
	switch {
	case ch >= '0' && ch <= '9':
		return int64(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int64(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int64(ch-'A') + 10
	default:
		return -1
	}
}

// checkStringIntConversion detects string(int) conversions.
// In Go, string(int) produces a UTF-8 encoded byte sequence for the given
// code point, NOT the string representation of the number. For example,
// string(65) produces "A", not "65". This is the #1 Go conversion pitfall
// flagged by staticcheck SA9004. The correct forms are:
//   - strconv.Itoa(n)  for int to decimal string
//   - fmt.Sprintf("%d", n)  for any integer type
//   - strconv.FormatInt(int64(n), 10)  for explicit integer types
//
// Note: string(rune) and string(byte) are valid and NOT flagged.
func checkStringIntConversion(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var instances []numericConvInstance

	// Must be string(...)
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "string" {
		return nil
	}
	if len(call.Args) != 1 {
		return nil
	}

	arg := call.Args[0]
	posStr := fset.Position(call.Pos()).String()

	// Check if the argument is an integer type or integer expression
	if isIntegerExpr(arg) {
		instances = append(instances, numericConvInstance{
			posStr:     posStr,
			pattern:    fmt.Sprintf("string(%s) converts integer to UTF-8 rune bytes, NOT to the decimal string representation", numExprString(arg)),
			suggestion: `-> Use strconv.Itoa() or fmt.Sprintf("%d", ...) instead of string(int)`,
		})
	}

	return instances
}

// checkSignedUnsignedConversion detects signed-to-unsigned and
// unsigned-to-signed integer conversions that can produce surprising results.
//   - int→uint: negative values wrap to huge unsigned values
//   - uint→int: values with MSB set become negative
//
// This is gosec G109 territory but at write-time.
func checkSignedUnsignedConversion(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var instances []numericConvInstance

	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	targetIsUnsigned := unsignedTypes[ident.Name]
	targetIsSigned := signedTypes[ident.Name]
	if !targetIsUnsigned && !targetIsSigned {
		return nil
	}
	if len(call.Args) != 1 {
		return nil
	}

	arg := call.Args[0]
	posStr := fset.Position(call.Pos()).String()

	// Signed→Unsigned: negative values wrap to huge unsigned
	if targetIsUnsigned && isSignedExpr(arg) {
		instances = append(instances, numericConvInstance{
			posStr:     posStr,
			pattern:    fmt.Sprintf("%s(%s) signed-to-unsigned conversion: negative values wrap to huge unsigned values", ident.Name, numExprString(arg)),
			suggestion: "-> Verify the value is non-negative before conversion, or handle the wrap-around case.",
		})
	}

	// Unsigned→Signed: MSB-set values become negative
	if targetIsSigned && isUnsignedExpr(arg) {
		instances = append(instances, numericConvInstance{
			posStr:     posStr,
			pattern:    fmt.Sprintf("%s(%s) unsigned-to-signed conversion: values exceeding max signed range become negative", ident.Name, numExprString(arg)),
			suggestion: "-> Add bounds check: if v > math.MaxInt32 { return error } before conversion.",
		})
	}

	return instances
}

// checkFloatToIntConversion detects float-to-integer conversions that
// silently truncate (toward zero) instead of rounding. This causes
// off-by-one errors when converting measurements, durations, or indices.
//   - int(3.9)  produces 3, not 4
//   - int(-1.9) produces -1, not -2
//
// The safe alternative is math.Round() before truncating, or fmt.Sprint
// when string output is needed.
func checkFloatToIntConversion(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var instances []numericConvInstance

	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil
	}
	if !signedTypes[ident.Name] && !unsignedTypes[ident.Name] {
		return nil
	}
	if len(call.Args) != 1 {
		return nil
	}

	arg := call.Args[0]

	// Check if the argument is a float expression
	if !isFloatExpr(arg) {
		return nil
	}

	// Skip if already wrapped in math.Round
	if innerCall, ok := arg.(*ast.CallExpr); ok {
		if sel, ok := innerCall.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "math" && sel.Sel.Name == "Round" {
				return nil // already rounded, safe
			}
		}
	}

	posStr := fset.Position(call.Pos()).String()
	instances = append(instances, numericConvInstance{
		posStr:     posStr,
		pattern:    fmt.Sprintf("%s(%s) truncates float toward zero, does not round", ident.Name, numExprString(arg)),
		suggestion: "-> Use math.Round() before converting: int(math.Round(floatVal))",
	})

	return instances
}

// isIntegerExpr checks if an expression is likely an integer type.
func isIntegerExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.INT
	case *ast.Ident:
		// Common integer variable name hints
		lname := strings.ToLower(e.Name)
		return isLargeValueIdentifier(lname) || isLikelyIntVarName(lname)
	case *ast.CallExpr:
		// Result of integer-returning function: len, cap, etc.
		if fnIdent, ok := e.Fun.(*ast.Ident); ok {
			if sizeFuncs[fnIdent.Name] || integerFuncs[fnIdent.Name] {
				return true
			}
			// Integer type conversions like int(x), int64(x)
			return signedTypes[fnIdent.Name] || unsignedTypes[fnIdent.Name]
		}
	case *ast.BinaryExpr:
		// Arithmetic on integers is likely integer
		return true
	}
	return false
}

// integerFuncs maps built-in functions known to return integers.
var integerFuncs = map[string]bool{
	"len": true, "cap": true, "copy": true,
}

// isLikelyIntVarName checks if a variable name is commonly used for integers.
func isLikelyIntVarName(name string) bool {
	commonIntPrefixes := []string{"n", "i", "j", "k"}
	for _, p := range commonIntPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	singleLetterInts := map[string]bool{"x": true, "y": true, "z": true}
	return singleLetterInts[name]
}

// isSignedExpr checks if an expression is likely a signed integer type
// that could hold negative values. Positive integer literals are excluded
// since they cannot be negative.
func isSignedExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return false // positive int literals can't be negative
	case *ast.UnaryExpr:
		if e.Op == token.SUB {
			return true // negative literal like -1
		}
		return isSignedExpr(e.X)
	case *ast.Ident:
		return true // bare identifiers are most commonly int (signed)
	case *ast.CallExpr:
		if fnIdent, ok := e.Fun.(*ast.Ident); ok {
			if signedTypes[fnIdent.Name] {
				return true
			}
			if unsignedTypes[fnIdent.Name] {
				return false
			}
			if sizeFuncs[fnIdent.Name] || integerFuncs[fnIdent.Name] {
				return true // len(), cap() return int (signed)
			}
		}
	case *ast.BinaryExpr:
		return true // arithmetic on ints is signed
	}
	return true // default: assume signed (safe to flag)
}

// isUnsignedExpr checks if an expression is likely an unsigned integer type.
func isUnsignedExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if fnIdent, ok := e.Fun.(*ast.Ident); ok {
			if unsignedTypes[fnIdent.Name] {
				return true
			}
		}
		// uint64(unixNano()), etc.
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			return isUnsignedSel(sel)
		}
	case *ast.SelectorExpr:
		return isUnsignedSel(e)
	}
	return false
}

// isUnsignedSel checks if a selector expression returns unsigned.
func isUnsignedSel(sel *ast.SelectorExpr) bool {
	// time.Now().Unix() returns int64, not unsigned. But UnixNano() also int64.
	// Common unsigned selectors: .Size(), .Len(), .Addr(), .Pointer()
	unsignedMethodHints := []string{"Pointer", "Addr", "Bits", "TrailingZeros", "LeadingZeros", "OnesCount"}
	for _, h := range unsignedMethodHints {
		if sel.Sel.Name == h {
			return true
		}
	}
	return false
}

// isFloatExpr checks if an expression is likely a float type.
func isFloatExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.FLOAT
	case *ast.Ident:
		lname := strings.ToLower(e.Name)
		floatHints := []string{"float", "ratio", "percent", "rate", "val", "score", "amount", "price", "delta", "diff", "avg", "average", "mean"}
		for _, fh := range floatHints {
			if strings.Contains(lname, fh) {
				return true
			}
		}
	case *ast.CallExpr:
		if fnIdent, ok := e.Fun.(*ast.Ident); ok {
			if floatTypes[fnIdent.Name] {
				return true
			}
			if mathFloatFuncs[fnIdent.Name] {
				return true
			}
		}
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "math" {
				return true // math.Sqrt, math.Sin, etc. return float64
			}
		}
	case *ast.BinaryExpr:
		// If either side is float, the result is float
		return isFloatExpr(e.X) || isFloatExpr(e.Y)
	}
	return false
}

// mathFloatFuncs maps math package functions (called without math. prefix
// when imported as dot-import, or locally) that return float64.
var mathFloatFuncs = map[string]bool{
	"Sqrt": true, "Pow": true, "Sin": true, "Cos": true, "Tan": true,
	"Log": true, "Log2": true, "Log10": true, "Exp": true,
	"Floor": true, "Ceil": true, "Trunc": true, "Mod": true,
	"Abs": true, "Max": true, "Min": true, "Hypot": true,
	"Modf": true, "Dim": true,
}

// numExprString returns a minimal string representation of an expression
// for use in warning messages.
func numExprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.CallExpr:
		if fnIdent, ok := e.Fun.(*ast.Ident); ok {
			args := make([]string, 0, len(e.Args))
			for _, a := range e.Args {
				args = append(args, numExprString(a))
			}
			return fmt.Sprintf("%s(%s)", fnIdent.Name, strings.Join(args, ", "))
		}
		return "..."
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s %s %s", numExprString(e.X), e.Op.String(), numExprString(e.Y))
	case *ast.ParenExpr:
		return "(" + numExprString(e.X) + ")"
	default:
		return "..."
	}
}

// checkDurationConfusion detects time.Sleep/After/Tick calls with integer
// literals instead of time.Duration values.
func checkDurationConfusion(fset *token.FileSet, call *ast.CallExpr) []numericConvInstance {
	var instances []numericConvInstance

	// Match time.Sleep(x), time.After(x), time.Tick(x)
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "time" {
		return nil
	}

	durationFuncs := map[string]bool{"Sleep": true, "After": true, "Tick": true, "NewTimer": true, "NewTicker": true}
	if !durationFuncs[sel.Sel.Name] {
		return nil
	}
	if len(call.Args) < 1 {
		return nil
	}

	arg := call.Args[0]
	posStr := fset.Position(call.Pos()).String()

	switch a := arg.(type) {
	case *ast.BasicLit:
		// time.Sleep(5) - bare integer literal, treated as 5 nanoseconds
		if a.Kind == token.INT || a.Kind == token.FLOAT {
			instances = append(instances, numericConvInstance{
				posStr:     posStr,
				pattern:    fmt.Sprintf("time.%s(%s) uses a bare numeric literal interpreted as nanoseconds", sel.Sel.Name, a.Value),
				suggestion: fmt.Sprintf("-> Use a time.Duration unit: time.%s(%s * time.Second) or time.%s(%s * time.Millisecond)", sel.Sel.Name, a.Value, sel.Sel.Name, a.Value),
			})
		}
	case *ast.Ident:
		// time.Sleep(timeout) where timeout is likely an int, not Duration
		name := a.Name
		lname := strings.ToLower(name)
		if isLikelyDurationMismatch(lname) {
			instances = append(instances, numericConvInstance{
				posStr:     posStr,
				pattern:    fmt.Sprintf("time.%s(%s) - '%s' may be an int (nanoseconds) instead of time.Duration", sel.Sel.Name, name, name),
				suggestion: fmt.Sprintf("-> Ensure '%s' is typed time.Duration, or multiply by the appropriate unit", name),
			})
		}
	case *ast.BinaryExpr:
		// time.Sleep(seconds * 1000) - integer arithmetic, likely wrong unit
		// This is OK only if one operand is a Duration type, which we can't fully verify
		// Flag only if both sides are BasicLit or Ident (no Duration selector)
		if !hasDurationType(arg) {
			instances = append(instances, numericConvInstance{
				posStr:     posStr,
				pattern:    fmt.Sprintf("time.%s(arithmetic) uses non-Duration arithmetic", sel.Sel.Name),
				suggestion: "-> Use time.Duration arithmetic: e.g., time.Duration(ms) * time.Millisecond",
			})
		}
	}

	return instances
}

// isLikelyDurationMismatch checks if a variable name suggests it holds a
// non-Duration value (seconds, ms, minutes as plain int/float).
func isLikelyDurationMismatch(name string) bool {
	fullHints := []string{"timeout", "delay", "interval", "wait", "seconds", "milliseconds", "minutes", "hours"}
	for _, dh := range fullHints {
		if name == dh || strings.Contains(name, dh) {
			return true
		}
	}
	suffixHints := []string{"ms", "secs", "sec", "msec", "mins", "hr"}
	for _, sh := range suffixHints {
		if strings.HasSuffix(name, sh) {
			return true
		}
	}
	return false
}

// hasDurationType checks if an expression contains a time.Duration or time unit reference.
func hasDurationType(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
				unit := sel.Sel.Name
				if unit == "Duration" || unit == "Second" || unit == "Millisecond" ||
					unit == "Microsecond" || unit == "Nanosecond" || unit == "Minute" || unit == "Hour" {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}
