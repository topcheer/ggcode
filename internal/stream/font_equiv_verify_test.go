package stream

import "testing"

// oldRuneWidth is a verbatim copy of the pre-refactor switch implementation,
// used to verify the table+binary-search version is behaviorally identical.
func oldRuneWidth(r rune) int {
	if r >= 0x20 && r <= 0x7E {
		return 1
	}
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2E80 && r <= 0x303E: // CJK Misc
		return 2
	case r >= 0x3040 && r <= 0x33BF: // Hiragana + Katakana + CJK punctuation
		return 2
	case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
		return 2
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return 2
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return 2
	case r >= 0xFE30 && r <= 0xFE6F: // CJK Compatibility Forms
		return 2
	case r >= 0xFF01 && r <= 0xFF60: // Fullwidth Forms
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth Signs
		return 2
	case r >= 0x20000 && r <= 0x2FFEF: // CJK Extensions B-I
		return 2
	case r >= 0x30000 && r <= 0x3FFEF: // CJK Extension G
		return 2
	case r >= 0x1F600 && r <= 0x1F64F: // Emoticons
		return 2
	case r >= 0x1F300 && r <= 0x1F5FF: // Misc Symbols and Pictographs
		return 2
	case r >= 0x1F680 && r <= 0x1F6FF: // Transport and Map
		return 2
	case r >= 0x1F900 && r <= 0x1F9FF: // Supplemental Symbols and Pictographs
		return 2
	case r >= 0x1FA00 && r <= 0x1FAFF: // Symbols and Pictographs Extended-A
		return 2
	case r >= 0x1F7E0 && r <= 0x1F7FF: // Geometric Shapes Extended (colored circles)
		return 2
	case r >= 0x2300 && r <= 0x23FF: // Misc Technical
		if r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA) {
			return 2
		}
		return 1
	case r >= 0x2600 && r <= 0x26FF: // Misc Symbols
		if r == 0x26A0 || r == 0x2614 || r == 0x2615 || r == 0x26AA || r == 0x26AB ||
			r == 0x26BD || r == 0x26BE || r == 0x26C4 || r == 0x26C5 ||
			(r >= 0x2648 && r <= 0x2653) || r == 0x26CE || r == 0x26D4 ||
			r == 0x26EA || (r >= 0x26F0 && r <= 0x26FA) || r == 0x26FD ||
			r == 0x2693 || r == 0x26F1 || r == 0x26F2 || r == 0x26F3 {
			return 2
		}
		return 1 // single-width
	case r >= 0x2700 && r <= 0x27BF: // Dingbats
		if r == 0x2702 || r == 0x2705 || r == 0x2708 || r == 0x2709 ||
			(r >= 0x270a && r <= 0x270D) || r == 0x270F ||
			r == 0x2712 || r == 0x2714 || r == 0x2716 || r == 0x271D ||
			r == 0x2721 || r == 0x2728 || r == 0x2733 || r == 0x2734 ||
			r == 0x2744 || r == 0x2747 || r == 0x274C || r == 0x274E ||
			(r >= 0x2753 && r <= 0x2755) || r == 0x2757 ||
			(r >= 0x2763 && r <= 0x2764) || (r >= 0x2795 && r <= 0x2797) ||
			r == 0x27A1 || r == 0x27B0 || r == 0x27BF {
			return 2
		}
		return 1
	case r >= 0x2B00 && r <= 0x2BFF: // Misc Symbols and Arrows
		if r == 0x2B05 || r == 0x2B06 || r == 0x2B07 ||
			(r >= 0x2B1B && r <= 0x2B1C) || r == 0x2B50 || r == 0x2B55 {
			return 2
		}
		return 1
	case r == 0xFE0F: // Variation Selector 16 (emoji presentation)
		return 0 // Zero-width modifier
	case r == 0x200D: // ZWJ (zero-width joiner)
		return 0
	}
	return 1
}

func TestRuneWidthEquivalentToLegacy(t *testing.T) {
	// Full sweep of the BMP plus the relevant astral planes.
	for r := rune(0); r <= 0xFFFF; r++ {
		if got, want := runeWidth(r), oldRuneWidth(r); got != want {
			t.Errorf("rune %U: new=%d old=%d", r, got, want)
		}
	}
	for r := rune(0x10000); r <= 0x400FF; r++ {
		if got, want := runeWidth(r), oldRuneWidth(r); got != want {
			t.Errorf("rune %U: new=%d old=%d", r, got, want)
		}
	}
	for r := rune(0x10FFFE); r <= 0x110001; r++ { // far astral boundary sanity
		if got, want := runeWidth(r), oldRuneWidth(r); got != want {
			t.Errorf("rune %U: new=%d old=%d", r, got, want)
		}
	}
}
