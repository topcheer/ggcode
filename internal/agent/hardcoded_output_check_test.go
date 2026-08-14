package agent

import (
	"testing"
)

func TestCheckHardcodedOutput_GoMapMemorization(t *testing.T) {
	// A function that returns hardcoded outputs from a large map
	src := `package main

func Lookup(key string) string {
	return outputs[key]
}

var outputs = map[string]string{
	"test1": "expected1",
	"test2": "expected2",
	"test3": "expected3",
	"test4": "expected4",
	"test5": "expected5",
	"test6": "expected6",
}
`
	warnings := checkHardcodedOutput("main.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for hardcoded map memorization pattern")
	}
}

func TestCheckHardcodedOutput_GoSwitchMemorization(t *testing.T) {
	src := `package main

func Process(input string) string {
	switch input {
	case "a":
		return "output_a"
	case "b":
		return "output_b"
	case "c":
		return "output_c"
	case "d":
		return "output_d"
	case "e":
		return "output_e"
	case "f":
		return "output_f"
	}
	return ""
}
`
	warnings := checkHardcodedOutput("main.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for hardcoded switch memorization")
	}
}

func TestCheckHardcodedOutput_NoWarningOnRealLogic(t *testing.T) {
	src := `package main

import "strings"

func Process(input string) string {
	cleaned := strings.TrimSpace(input)
	lowered := strings.ToLower(cleaned)
	result := transform(lowered)
	return result + "_processed"
}

func transform(s string) string {
	return strings.ReplaceAll(s, " ", "_")
}
`
	warnings := checkHardcodedOutput("main.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for real logic, got: %v", warnings)
	}
}

func TestCheckHardcodedOutput_NoWarningOnConfigGetter(t *testing.T) {
	src := `package main

var defaultConfig = map[string]string{
	"timeout": "30s",
	"retries": "3",
	"port":    "8080",
	"host":    "localhost",
	"mode":    "production",
	"region":  "us-east-1",
}

func DefaultConfig(key string) string {
	return defaultConfig[key]
}
`
	warnings := checkHardcodedOutput("main.go", "", src)
	// Config getters with known/default prefixes should not warn
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for config getter, got: %v", warnings)
	}
}

func TestCheckHardcodedOutput_SkipTestFiles(t *testing.T) {
	src := `package main

func Process(input string) string {
	return table[input]
}

var table = map[string]string{
	"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6",
}
`
	warnings := checkHardcodedOutput("handler_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestCheckHardcodedOutput_PythonDictMemorization(t *testing.T) {
	src := `LOOKUP = {
    "input1": "output1",
    "input2": "output2",
    "input3": "output3",
    "input4": "output4",
    "input5": "output5",
    "input6": "output6",
    "input7": "output7",
}

def solve(input_str):
    return LOOKUP.get(input_str, "")
`
	warnings := checkHardcodedOutput("solver.py", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for Python dict memorization")
	}
}

func TestCheckHardcodedOutput_PythonIfElseChain(t *testing.T) {
	src := `def solve(input_str):
    if input_str == "case1":
        return "result1"
    if input_str == "case2":
        return "result2"
    if input_str == "case3":
        return "result3"
    if input_str == "case4":
        return "result4"
    if input_str == "case5":
        return "result5"
    if input_str == "case6":
        return "result6"
    return ""
`
	warnings := checkHardcodedOutput("solver.py", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for Python if/elif chain")
	}
}

func TestCheckHardcodedOutput_JSObjectLiteral(t *testing.T) {
	src := `const lookup = {
    "test_input_1": "expected_output_1",
    "test_input_2": "expected_output_2",
    "test_input_3": "expected_output_3",
    "test_input_4": "expected_output_4",
    "test_input_5": "expected_output_5",
    "test_input_6": "expected_output_6",
};

function solve(input) {
    return lookup[input];
}
`
	warnings := checkHardcodedOutput("solver.js", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for JS object literal memorization")
	}
}

func TestCheckHardcodedOutput_JSSwitchCases(t *testing.T) {
	src := `function solve(input) {
    switch (input) {
        case "case1": return "output1";
        case "case2": return "output2";
        case "case3": return "output3";
        case "case4": return "output4";
        case "case5": return "output5";
        case "case6": return "output6";
    }
    return "";
}
`
	warnings := checkHardcodedOutput("solver.js", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for JS switch memorization")
	}
}

func TestCheckHardcodedOutput_EmptyFile(t *testing.T) {
	warnings := checkHardcodedOutput("main.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty file, got: %v", warnings)
	}
}

func TestCheckHardcodedOutput_RealComputationNoFalsePositive(t *testing.T) {
	// A function with a switch that computes results, not just returns literals
	src := `package main

func calculate(input int) int {
	switch input {
	case 1:
		return input * 2 + 1
	case 2:
		return input * 3 + 5
	case 3:
		return input * 4 + 10
	case 4:
		return input * 5 + 15
	case 5:
		return input * 6 + 20
	default:
		return input
	}
}
`
	warnings := checkHardcodedOutput("calc.go", "", src)
	// Cases return computed expressions, not literals - should not fire
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for computed switch, got: %v", warnings)
	}
}

// TestHardcodedOutput_DeltaSuppress pins fix #174: a pre-existing large
// lookup map must not re-report on unrelated edits.
func TestHardcodedOutput_DeltaSuppress(t *testing.T) {
	old := "package main\nfunc lookupCode(s string) string {\n\tm := map[string]string{\n\t\t\"a\": \"1\", \"b\": \"2\", \"c\": \"3\", \"d\": \"4\", \"e\": \"5\", \"f\": \"6\",\n\t}\n\treturn m[s]\n}\n"
	newC := "package main\n// touched\nfunc lookupCode(s string) string {\n\tm := map[string]string{\n\t\t\"a\": \"1\", \"b\": \"2\", \"c\": \"3\", \"d\": \"4\", \"e\": \"5\", \"f\": \"6\",\n\t}\n\treturn m[s]\n}\n"
	w := checkHardcodedOutput("a.go", old, newC)
	if len(w) != 0 {
		t.Fatalf("pre-existing lookup table must not re-report: %v", w)
	}
}

// TestHardcodedOutput_PythonEntryCount pins fix #174: a 3-pair dict must NOT
// fire (threshold is 5) and counts must reflect real pairs, not quote chars.
func TestHardcodedOutput_PythonEntryCount(t *testing.T) {
	src := "LOOKUP = {\"a\": \"b\", \"c\": \"d\", \"e\": \"f\"}\n"
	w := checkHardcodedOutput("a.py", "", src)
	if len(w) != 0 {
		t.Fatalf("3-pair dict must not fire with 2x-inflated count: %v", w)
	}
}
