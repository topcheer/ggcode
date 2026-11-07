package tool

import "testing"

// #5231: the ripgrep auto-install fallback must only use official channels.
// The PyPI "ripgrep" package is a third-party wrapper around an unofficial
// binary - flags/output can diverge from official rg, which this tool's
// parsing depends on, and the wrapper is unaudited (supply-chain surface).
// brew/cargo/conda(-forge) all package the official ripgrep source/binary.
func TestIssue5231NoPipInstallChannel(t *testing.T) {
	if len(rgInstallChannels) == 0 {
		t.Fatal("install channel table must not be empty")
	}
	for _, ch := range rgInstallChannels {
		switch ch.bin {
		case "pip", "pip3":
			t.Fatalf("channel %q uses %q: PyPI ripgrep is a third-party wrapper (#5231)", ch.name, ch.bin)
		}
		switch ch.bin {
		case "brew", "cargo", "conda":
			// official-source channels
		default:
			t.Errorf("channel %q uses unrecognized manager %q - extend the official-channel allowlist deliberately", ch.name, ch.bin)
		}
		if len(ch.args) == 0 {
			t.Errorf("channel %q has empty install args", ch.name)
		}
	}

	// Preference order must stay brew > cargo > conda (fast native channel
	// first; conda-forge last because env activation quirks are the most
	// common failure mode).
	want := []string{"brew", "cargo", "conda"}
	for i, w := range want {
		if rgInstallChannels[i].bin != w {
			t.Fatalf("channel order changed: want %v, got table starting %v", want, rgInstallChannels)
		}
	}
}
