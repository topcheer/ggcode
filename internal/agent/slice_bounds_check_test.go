package agent

import (
	"strings"
	"testing"
)

func TestSliceBoundsRiskyFindSubmatch(t *testing.T) {
	src := "package main\n\nimport \"regexp\"\n\nfunc extract(s string) string {\n" +
		"\tre := regexp.MustCompile(`(\\w+)=(\\w+)`)\n" +
		"\tmatch := re.FindStringSubmatch(s)\n" +
		"\treturn match[1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for FindStringSubmatch[1] without guard")
	}
	if !strings.Contains(warnings[0], "FindStringSubmatch") {
		t.Errorf("warning should mention FindStringSubmatch, got: %s", warnings[0])
	}
}

func TestSliceBoundsFindSubmatchWithGuard(t *testing.T) {
	src := "package main\n\nimport \"regexp\"\n\nfunc extract(s string) string {\n" +
		"\tre := regexp.MustCompile(`(\\w+)=(\\w+)`)\n" +
		"\tmatch := re.FindStringSubmatch(s)\n" +
		"\tif len(match) > 1 {\n" +
		"\t\treturn match[1]\n" +
		"\t}\n" +
		"\treturn \"\"\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with len guard, got: %v", warnings)
	}
}

func TestSliceBoundsRiskySplitIndex1(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc secondPart(s string) string {\n" +
		"\tparts := strings.Split(s, \",\")\n" +
		"\treturn parts[1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for strings.Split parts[1] without guard")
	}
	if !strings.Contains(warnings[0], "strings.Split") {
		t.Errorf("warning should mention strings.Split, got: %s", warnings[0])
	}
}

func TestSliceBoundsSplitIndex0Safe(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc firstPart(s string) string {\n" +
		"\tparts := strings.Split(s, \",\")\n" +
		"\treturn parts[0]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for parts[0] (safe for Split), got: %v", warnings)
	}
}

func TestSliceBoundsSplitWithGuard(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc secondPart(s string) string {\n" +
		"\tparts := strings.Split(s, \",\")\n" +
		"\tif len(parts) > 1 {\n" +
		"\t\treturn parts[1]\n" +
		"\t}\n" +
		"\treturn \"\"\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with len guard, got: %v", warnings)
	}
}

func TestSliceBoundsFieldsIndex0(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc firstField(s string) string {\n" +
		"\tfields := strings.Fields(s)\n" +
		"\treturn fields[0]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for strings.Fields fields[0] without guard")
	}
}

func TestSliceBoundsNonGoFile(t *testing.T) {
	warnings := checkSliceBoundsRisk("test.py", "", "parts = s.split(',')")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestSliceBoundsEmptyContent(t *testing.T) {
	warnings := checkSliceBoundsRisk("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestSliceBoundsDeltaAware(t *testing.T) {
	src := "package main\n\nimport \"regexp\"\n\nfunc extract(s string) string {\n" +
		"\tre := regexp.MustCompile(`(\\w+)=(\\w+)`)\n" +
		"\tmatch := re.FindStringSubmatch(s)\n" +
		"\treturn match[1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", src, src)
	if len(warnings) != 0 {
		t.Errorf("expected no delta warnings for unchanged code, got: %v", warnings)
	}
}

func TestSliceBoundsReassignmentRemovesTracking(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc process(s string) string {\n" +
		"\tparts := strings.Split(s, \",\")\n" +
		"\tparts = []string{\"a\", \"b\"}\n" +
		"\treturn parts[1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings after reassignment, got: %v", warnings)
	}
}

func TestSliceBoundsNonLiteralIndex(t *testing.T) {
	src := "package main\n\nimport \"strings\"\n\nfunc process(s string, i int) string {\n" +
		"\tparts := strings.Split(s, \",\")\n" +
		"\treturn parts[i]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-literal index, got: %v", warnings)
	}
}

func TestSliceBoundsFindAllStringSubmatch(t *testing.T) {
	src := "package main\n\nimport \"regexp\"\n\nfunc extract(s string) string {\n" +
		"\tre := regexp.MustCompile(`(\\w+)`)\n" +
		"\tmatches := re.FindAllStringSubmatch(s, -1)\n" +
		"\treturn matches[0][1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for FindAllStringSubmatch without guard")
	}
}

func TestSliceBoundsNoRiskyFunc(t *testing.T) {
	src := "package main\n\nfunc process(items []string) string {\n" +
		"\treturn items[0]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-risky source, got: %v", warnings)
	}
}

func TestSliceBoundsFilepathSplitList(t *testing.T) {
	src := "package main\n\nimport \"path/filepath\"\n\nfunc secondPath(s string) string {\n" +
		"\tpaths := filepath.SplitList(s)\n" +
		"\treturn paths[1]\n" +
		"}\n"
	warnings := checkSliceBoundsRisk("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for filepath.SplitList paths[1] without guard")
	}
}
