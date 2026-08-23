package stt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin writes a tiny shell script that records its argv to a file and
// creates the .txt output a real whisper CLI would produce (openai-whisper
// via --output_dir, whisper.cpp via the -of prefix). This lets the tests
// verify the exact argument layout per binary flavor without shipping
// whisper binaries. argv reconstruction relies on one argument per line,
// so any argument containing a newline would corrupt the log; none do.
func fakeBin(t *testing.T, dir, name, outText string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// -of handling mirrors whisper.cpp: the next argument after -of is the
	// output path prefix and -otxt appends .txt.
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvLog(dir, name) + "; done\n" +
		"prefix=\"\"\nout=\"\"\nprev=\"\"\nfor a in \"$@\"; do\n" +
		"  case \"$prev\" in\n" +
		"    --output_dir) out=\"$a/voice.txt\" ;;\n" +
		"    -of) prefix=\"$a\" ;;\n" +
		"  esac\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"if [ -n \"$out\" ]; then :; elif [ -n \"$prefix\" ]; then out=\"$prefix.txt\"; fi\n" +
		"if [ -n \"$out\" ]; then printf '%s' " + shQuote(outText) + " > \"$out\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return path
}

func argvLog(dir, name string) string {
	return filepath.Join(dir, "argv-"+name)
}

// shQuote wraps s in single quotes for safe interpolation into the fake
// binary's shell script.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return lines
}

// readArgvAt returns the index of want in the argv log, or -1.
func readArgvAt(t *testing.T, path, want string) int {
	t.Helper()
	for i, a := range readArgv(t, path) {
		if a == want {
			return i
		}
	}
	return -1
}

func writeAudio(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(p, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	return p
}

// newTestWhisperForPath builds a LocalWhisper pointed at an absolute binary
// path and forces the availability probe to succeed without touching PATH,
// so the test only exercises argv construction.
func forceAvailable(t *testing.T, w *LocalWhisper) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.checked = true
	w.available = true
	w.flavor = flavorFromBin(w.binPath)
	if w.flavor == flavorCPP && w.cppModel == "" {
		// Keep an explicitly configured model path; otherwise fall back to
		// any placeholder so argv construction can proceed.
		if w.model != "" {
			w.cppModel = w.model
		} else {
			w.cppModel = filepath.Join(t.TempDir(), "ggml-base.en-q5_1.bin")
		}
	}
}

func TestTranscribeOpenAIWhisperUsesLongOptions(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "whisper", "hello from openai-whisper")
	audio := writeAudio(t, dir)

	w := NewLocalWhisper(bin, "", "en")
	forceAvailable(t, w)

	res, err := w.Transcribe(context.Background(), Request{Path: audio})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if res.Text != "hello from openai-whisper" {
		t.Errorf("transcribed text = %q", res.Text)
	}
	if res.Provider != "whisper-local" {
		t.Errorf("provider = %q", res.Provider)
	}
	argv := readArgv(t, argvLog(dir, "whisper"))

	// First arg must be the positional audio path, followed by openai-whisper
	// long options --model/--output_dir/--output_format/--verbose (issue #969).
	if len(argv) < 8 {
		t.Fatalf("unexpected argv shape: %v", argv)
	}
	if argv[0] != audio {
		t.Errorf("audio must be the positional first argument, got %q", argv[0])
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--model turbo",
		"--output_format txt",
		"--verbose False",
		"--language en",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("openai-whisper argv missing %q: %v", want, argv)
		}
	}
	if !strings.Contains(joined, "--output_dir") {
		t.Errorf("openai-whisper argv missing --output_dir: %v", argv)
	}
	if strings.Contains(joined, " -m ") || strings.Contains(joined, " -f ") {
		t.Errorf("openai-whisper argv must not contain whisper.cpp short options: %v", argv)
	}
	if res.Model != "turbo" {
		t.Errorf("result model = %q, want turbo", res.Model)
	}
}

func TestTranscribeWhisperCPPUsesShortOptions(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "whisper-cpp", "hello from whisper.cpp")
	audio := writeAudio(t, dir)
	model := filepath.Join(dir, "ggml-small.bin")
	if err := os.WriteFile(model, []byte("ggml"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}

	w := NewLocalWhisper(bin, model, "en")
	forceAvailable(t, w)

	res, err := w.Transcribe(context.Background(), Request{Path: audio})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if res.Text != "hello from whisper.cpp" {
		t.Errorf("transcribed text = %q", res.Text)
	}
	if res.Model != model {
		t.Errorf("result model = %q, want %q", res.Model, model)
	}
	argv := readArgv(t, argvLog(dir, "whisper-cpp"))
	joined := strings.Join(argv, " ")

	// whisper.cpp short-option interface: -m <model> -f <audio> -otxt -of <prefix>.
	for _, want := range []string{"-m " + model, "-f " + audio, "-otxt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("whisper.cpp argv missing %q: %v", want, argv)
		}
	}
	// -of must set an output path prefix ending with the audio base name
	// (the .txt extension is appended by -otxt).
	foundOf := false
	for i, a := range argv {
		if a == "-of" && i+1 < len(argv) {
			foundOf = true
			if !strings.HasSuffix(argv[i+1], "voice") {
				t.Errorf("-of prefix %q should end with audio base %q", argv[i+1], "voice")
			}
		}
	}
	if !foundOf {
		t.Errorf("whisper.cpp argv missing -of <prefix>: %v", argv)
	}
	// Language goes through -l, never --language.
	if !strings.Contains(joined, "-l en") || strings.Contains(joined, "--language") {
		t.Errorf("whisper.cpp language flag wrong: %v", argv)
	}
	// No openai-whisper long options may leak into the whisper.cpp call.
	for _, bad := range []string{"--model", "--output_dir", "--output_format", "--verbose"} {
		if strings.Contains(joined, bad) {
			t.Errorf("whisper.cpp argv must not contain %q: %v", bad, argv)
		}
	}
	if !strings.Contains(joined, model) {
		t.Errorf("whisper.cpp argv must pass the ggml model path: %v", argv)
	}
}

func TestFlavorFromBin(t *testing.T) {
	cases := map[string]whisperFlavor{
		"whisper":              flavorOpenAI,
		"/usr/bin/whisper":     flavorOpenAI,
		"whisper.exe":          flavorOpenAI,
		"whisper-cpp":          flavorCPP,
		"whisper-cli":          flavorCPP,
		"whisper.cpp":          flavorCPP,
		"whisper-cpp.exe":      flavorCPP,
		"/opt/w/Whisper-CLI":   flavorCPP,
		"my-custom-transcribe": flavorOpenAI,
	}
	for bin, want := range cases {
		if got := flavorFromBin(bin); got != want {
			t.Errorf("flavorFromBin(%q) = %v, want %v", bin, got, want)
		}
	}
}

func TestResolveCPPModelExplicitAndFallback(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "ggml-base.en.bin")
	if err := os.WriteFile(model, []byte("ggml"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}

	// Explicit model wins.
	w := NewLocalWhisper("", model, "")
	if p, err := w.resolveCPPModel(); err != nil || p != model {
		t.Fatalf("explicit model: got %q, %v", p, err)
	}

	// Missing explicit model produces an actionable error, not a generic one.
	t.Setenv("GGCODE_WHISPER_CPP_MODEL", "")
	w2 := NewLocalWhisper("", filepath.Join(dir, "ggml-missing.bin"), "")
	_, err := w2.resolveCPPModel()
	if err == nil {
		t.Fatal("expected error for missing explicit model")
	}
	for _, want := range []string{"local_model", "ggml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing-model error should mention %q, got: %v", want, err)
		}
	}
}

func TestResolveCPPModelAutoDetectsDownloadedModel(t *testing.T) {
	models := t.TempDir()
	small := filepath.Join(models, "ggml-a.bin")
	tiny := filepath.Join(models, "ggml-b.bin")
	for _, p := range []string{small, tiny} {
		if err := os.WriteFile(p, []byte("ggml"), 0o644); err != nil {
			t.Fatalf("write model: %v", err)
		}
	}
	t.Setenv("GGCODE_WHISPER_CPP_MODEL", "")
	restore := swapCPPModelSearchDirs(t, []string{models})
	defer restore()

	w := NewLocalWhisper("", "", "")
	p, err := w.resolveCPPModel()
	if err != nil {
		t.Fatalf("auto-detect: %v", err)
	}
	// Deterministic pick: lexicographically-first ggml-*.bin in the first
	// matching search dir.
	if p != small {
		t.Errorf("auto-detect should pick lexicographically-first model, got %q want %q", p, small)
	}
}

// swapCPPModelSearchDirs replaces the model search dirs for the current
// test and returns a restore func.
func swapCPPModelSearchDirs(t *testing.T, dirs []string) func() {
	t.Helper()
	orig := cppModelSearchDirs
	cppModelSearchDirs = func() []string { return dirs }
	return func() { cppModelSearchDirs = orig }
}

func TestProbeFlavorCPPWithoutModelMarksUnavailable(t *testing.T) {
	// No configured model, no env override, empty search dirs: resolution
	// must fail with an error that explains the ggml model requirement
	// rather than "binary not found".
	t.Setenv("GGCODE_WHISPER_CPP_MODEL", "")
	restore := swapCPPModelSearchDirs(t, []string{t.TempDir()})
	defer restore()

	w := NewLocalWhisper("", "", "")
	w.binPath = "/nonexistent-path/whisper-cpp"
	err := w.probeFlavor()
	if err == nil {
		t.Fatal("probeFlavor should fail when no ggml model can be located")
	}
	if w.available {
		t.Error("probeFlavor must not mark whisper.cpp available without a model")
	}
	if !strings.Contains(err.Error(), "ggml") || !strings.Contains(err.Error(), "local_model") {
		t.Errorf("error must explain the ggml model requirement, got: %v", err)
	}
	if !strings.Contains(err.Error(), "whisper.cpp") {
		t.Errorf("error should mention whisper.cpp, got: %v", err)
	}
	// The stored reason must survive into the cached unavailable state.
	if !strings.Contains(w.unavailableReason, "ggml") {
		t.Errorf("unavailableReason should keep the concrete cause, got: %q", w.unavailableReason)
	}
}

func TestProbeFlavorOpenAINeedsNoModel(t *testing.T) {
	w := NewLocalWhisper("", "", "")
	w.binPath = "/nonexistent-path/whisper"
	if err := w.probeFlavor(); err != nil {
		t.Fatalf("openai-whisper probe must not require a ggml model: %v", err)
	}
	if !w.available {
		t.Error("openai-whisper probe should mark available")
	}
}

func TestEnsureAvailableCachedErrorIsActionable(t *testing.T) {
	// The cached-unavailable path must replay the stored concrete reason,
	// not the old misleading "binary not found at %q" with an empty path.
	w := NewLocalWhisper("", "", "")
	w.mu.Lock()
	w.checked = true
	w.available = false
	w.unavailableReason = "whisper.cpp detected but no ggml model found: set im.stt.local_model"
	w.mu.Unlock()

	err := w.ensureAvailable()
	if err == nil {
		t.Fatal("expected cached unavailable error")
	}
	if !strings.Contains(err.Error(), "ggml") {
		t.Errorf("cached error should preserve the real reason, got: %v", err)
	}
	if strings.Contains(err.Error(), `binary not found at ""`) {
		t.Errorf("cached error must not report the legacy misleading message: %v", err)
	}
	if w.Available() {
		t.Error("Available() must be false")
	}
}
