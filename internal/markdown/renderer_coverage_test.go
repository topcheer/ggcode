package markdown

import (
	"reflect"
	"strings"
	"testing"

	glamourstyles "charm.land/glamour/v2/styles"
)

func TestWarmup(t *testing.T) {
	// Must not panic; seeds the 80 and 120 renderers.
	Warmup()
	if Renderer(80) == nil {
		t.Fatal("Warmup should leave a usable renderer for width 80")
	}
	if Renderer(120) == nil {
		t.Fatal("Warmup should leave a usable renderer for width 120")
	}
}

func TestRendererWrapFallbackAndCache(t *testing.T) {
	r1 := Renderer(0) // wrap <= 0 falls back to 80
	if r1 == nil {
		t.Fatal("Renderer(0) should fall back to width 80 and succeed")
	}
	r2 := Renderer(-5)
	if r2 == nil {
		t.Fatal("Renderer(-5) should fall back to width 80 and succeed")
	}
	r3 := Renderer(80) // cache hit
	if r3 != r1 {
		t.Fatalf("Renderer(80) should return the cached instance, got %p vs %p", r3, r1)
	}
	r4 := Renderer(97)
	if r4 == nil || r4 == r1 {
		t.Fatal("Renderer(97) should create a distinct non-nil renderer")
	}
}

func TestRenderNormalizesCarriageReturns(t *testing.T) {
	out := Render("line1\r\nline2\rline3\ttabbed", 60)
	if strings.ContainsRune(out, '\r') {
		t.Fatalf("rendered output must not contain bare CR, got %q", out)
	}
	if strings.ContainsRune(out, '\t') {
		t.Fatalf("rendered output must not contain tabs, got %q", out)
	}
	for _, want := range []string{"line1", "line2", "line3", "tabbed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered output %q should contain %q", out, want)
		}
	}
}

func TestRenderRealOutput(t *testing.T) {
	out := Render("# Title\n\nsome text", 80)
	if out == "" {
		t.Fatal("Render should produce non-empty output for simple markdown")
	}
	if !strings.Contains(out, "Title") {
		t.Fatalf("Render output should keep heading text, got %q", out)
	}
	// Leading/trailing newlines added by glamour must be trimmed.
	if strings.HasPrefix(out, "\n") || strings.HasSuffix(out, "\n") {
		t.Fatalf("Render output must be trimmed of leading/trailing newlines, got %q", out)
	}
}

func TestHasDarkBackground(t *testing.T) {
	tests := []struct {
		name string
		fgbg string
		want bool
	}{
		{"empty defaults to dark", "", true},
		{"single component", "15", true},
		{"classic dark fg/bg", "0;15", false},
		{"light bg 7", "0;7", false},
		{"light bg 231", "15;231", false},
		{"light bg 255", "0;255", false},
		{"light bg 253", "0;253", false},
		{"dark bg 0", "15;0", true},
		{"dark bg 8", "0;8", true},
		{"dark bg 16", "0;16", true},
		{"dark bg 232", "0;232", true},
		{"dark bg 239", "0;239", true},
		{"unknown code defaults dark", "0;9", true},
		{"three components uses last", "0;15;0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLORFGBG", tt.fgbg)
			if got := hasDarkBackground(); got != tt.want {
				t.Fatalf("hasDarkBackground(COLORFGBG=%q) = %v, want %v", tt.fgbg, got, tt.want)
			}
		})
	}
}

func TestStyleConfigNonTerminal(t *testing.T) {
	// In tests stdout is not a terminal, so the fast NoTTY path is taken.
	cfg := StyleConfig()
	if !reflect.DeepEqual(cfg, glamourstyles.NoTTYStyleConfig) {
		t.Fatal("StyleConfig on non-terminal stdout should return NoTTYStyleConfig")
	}
}

func TestStyleConfigForDarkMode(t *testing.T) {
	dark := StyleConfigForDarkMode(true)
	if dark.Code.Color == nil || *dark.Code.Color != "#7aa2f7" {
		t.Fatalf("dark config should apply accent #7aa2f7, got %v", dark.Code.Color)
	}
	if dark.Code.BackgroundColor != nil {
		t.Fatal("dark config should clear Code.BackgroundColor")
	}
	light := StyleConfigForDarkMode(false)
	if light.Code.Color == nil || *light.Code.Color != "#005f87" {
		t.Fatalf("light config should apply accent #005f87, got %v", light.Code.Color)
	}
	if light.Code.BackgroundColor != nil {
		t.Fatal("light config should clear Code.BackgroundColor")
	}
	// Chroma entries must be colored when present.
	if dark.CodeBlock.Chroma != nil {
		if dark.CodeBlock.Chroma.Comment.Color == nil {
			t.Fatal("dark config should color chroma comments")
		}
	}
	if light.CodeBlock.Chroma != nil && light.CodeBlock.Chroma.Text.Color == nil {
		t.Fatal("light config should color chroma text")
	}
}

func TestApplyPaletteNilSafe(t *testing.T) {
	// Must not panic on nil config.
	applyPalette(nil, "#000000", "#111111", "#222222", "#333333", "#444444")

	// Config without chroma: early return after base colors.
	cfg := glamourstyles.NoTTYStyleConfig
	applyPalette(&cfg, "#000000", "#111111", "#222222", "#333333", "#444444")
	if cfg.Code.Color == nil || *cfg.Code.Color != "#000000" {
		t.Fatalf("applyPalette should set Code.Color, got %v", cfg.Code.Color)
	}
}

func TestNormalizeEmpty(t *testing.T) {
	if got := Normalize(""); got != "" {
		t.Fatalf("Normalize(\"\") = %q, want empty", got)
	}
}

func TestNormalizeHeadingSpacing(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"blank line inserted before heading after paragraph",
			"para\n## head",
			"para\n\n## head",
		},
		{
			"blank line inserted after heading before paragraph",
			"## head\npara",
			"## head\n\npara",
		},
		{
			"no double blank when heading already separated",
			"para\n\n## head\n\npara",
			"para\n\n## head\n\npara",
		},
		{
			"consecutive headings do not get blank between",
			"## a\n## b",
			"## a\n\n## b",
		},
		{
			"heading at start needs no leading blank",
			"## head\nbody",
			"## head\n\nbody",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeFences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"bare backtick fence gets language",
			"code\n```\nx\n```",
			"code\n```text\nx\n```",
		},
		{
			"bare tilde fence gets language",
			"```\ny\n~~~",
			"```text\ny\n~~~",
		},
		{
			"language fence untouched",
			"```go\nz\n```",
			"```go\nz\n```",
		},
		{
			"content inside fence is not heading-normalized",
			"```\n## not a heading\n```",
			"```text\n## not a heading\n```",
		},
		{
			// Fence tracking toggles per fence-prefixed line regardless of
			// fence character: the "```" line closes the "~~~" fence, so the
			// final "~~~" is treated as a new opening fence and gains a lang.
			"fence toggle does not distinguish fence characters",
			"~~~\n```\n~~~",
			"~~~text\n```\n~~~text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeHeadingLastLineReset(t *testing.T) {
	// Single heading line: the trailing-line reset path executes.
	got := Normalize("## only")
	if got != "## only" {
		t.Fatalf("Normalize single heading = %q", got)
	}
}

func TestNormalizeHeadingVariants(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// normalizeHeading reconstruction
		{"simple h1", "# hello", "# hello"},
		{"simple h2", "## hello", "## hello"},
		{"h6", "###### hello", "###### hello"},
		{"leading space normalized away", "  ## hello", "## hello"},
		{"three leading spaces allowed", "   ## hello", "## hello"},
		{"four leading spaces not a heading", "    ## hello", "    ## hello"},
		{"hash without space not heading", "#nospace", "#nospace"},
		{"hash alone not heading", "#", "#"},
		{"hash with trailing space only not heading", "# ", "# "},
		{"seven hashes not heading", "####### deep", "####### deep"},
		{"tab after hashes", "#\thello", "# hello"},
		// closing sequences
		{"closing sequence stripped", "## hello ##", "## hello"},
		{"closing sequence with tab delimiter", "## heading\t#", "## heading"},
		{"word char before hashes kept", "## C#", "## C#"},
		{"all-hash content kept", "## ###", "## ###"},
		{"trailing spaces trimmed", "## hello   ", "## hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, isHeading := normalizeHeading(tt.in); got != tt.want {
				t.Fatalf("normalizeHeading(%q) = %q (heading=%v), want %q", tt.in, got, isHeading, tt.want)
			}
		})
	}
}

func TestNormalizeHeadingFlag(t *testing.T) {
	headingCases := []string{"# a", "## a", "  # a", "#\ta"}
	for _, in := range headingCases {
		if _, ok := normalizeHeading(in); !ok {
			t.Fatalf("normalizeHeading(%q) should report isHeading=true", in)
		}
	}
	nonHeadingCases := []string{"plain", "#nospace", "#", "# ", "####### x", "    # x"}
	for _, in := range nonHeadingCases {
		if _, ok := normalizeHeading(in); ok {
			t.Fatalf("normalizeHeading(%q) should report isHeading=false", in)
		}
	}
}
