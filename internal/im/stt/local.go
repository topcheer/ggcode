package stt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// whisperFlavor identifies the CLI dialect of a whisper binary. The
// openai-whisper (Python) CLI and whisper.cpp (C/C++) CLI accept entirely
// different argument sets, so the flavor must be known before building the
// command line (issue #969: passing openai-whisper args to whisper.cpp fails
// on every invocation while the availability cache stays stale).
type whisperFlavor int

const (
	// flavorOpenAI is the Python openai-whisper CLI: positional audio file,
	// --model/--output_dir/--output_format/--verbose long options.
	flavorOpenAI whisperFlavor = iota
	// flavorCPP is the whisper.cpp CLI: -m <ggml-model> -f <audio> plus
	// -otxt/-of short options.
	flavorCPP
)

// flavorFromBin classifies a binary by its base name. "whisper" is the
// openai-whisper entrypoint; whisper.cpp ships as whisper-cpp / whisper-cli /
// whisper.cpp depending on the distribution. Unknown names (user-provided
// wrappers) keep the legacy openai-whisper behavior.
func flavorFromBin(binPath string) whisperFlavor {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(binPath)))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "whisper":
		return flavorOpenAI
	case "whisper-cpp", "whisper-cli", "whisper.cpp", "whispercpp":
		return flavorCPP
	default:
		return flavorOpenAI
	}
}

// LocalWhisper uses a local whisper CLI binary for transcription.
type LocalWhisper struct {
	binPath  string
	flavor   whisperFlavor
	cppModel string // resolved ggml-*.bin path, only set for flavorCPP
	model    string
	language string
	// available/checked cache the result of the first availability probe;
	// unavailableReason keeps the real cause so cached errors are not
	// misleading ("no ggml model" must not surface as "binary not found").
	available         bool
	checked           bool
	unavailableReason string
	mu                sync.Mutex
}

// NewLocalWhisper creates a local whisper transcriber.
// binPath is the path to the whisper binary (empty = auto-detect).
// For the openai-whisper CLI, model is the model size (empty = "turbo" for
// speed + accuracy). For whisper.cpp binaries, model is the path to a
// ggml-*.bin model file (empty = auto-detect from common locations).
// language is the language code (empty = auto-detect).
func NewLocalWhisper(binPath, model, language string) *LocalWhisper {
	return &LocalWhisper{
		binPath:  strings.TrimSpace(binPath),
		model:    strings.TrimSpace(model),
		language: strings.TrimSpace(language),
	}
}

func (w *LocalWhisper) Transcribe(ctx context.Context, req Request) (Result, error) {
	if err := w.ensureAvailable(); err != nil {
		return Result{}, err
	}

	audioPath := strings.TrimSpace(req.Path)
	if audioPath == "" {
		return Result{}, fmt.Errorf("local whisper: no audio path provided")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return Result{}, fmt.Errorf("local whisper: audio file not found: %w", err)
	}

	// Create temp dir for whisper output
	outDir, err := os.MkdirTemp("", "ggcode-whisper-out-*")
	if err != nil {
		return Result{}, fmt.Errorf("local whisper: create temp dir: %w", err)
	}
	defer os.RemoveAll(outDir)

	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))

	var args []string
	var modelUsed string
	if w.flavor == flavorCPP {
		args, modelUsed = w.cppArgs(audioPath, outDir, base)
	} else {
		args, modelUsed = w.openaiArgs(audioPath, outDir)
	}

	debug.Log("stt", "local whisper: running %s %v", w.binPath, args)

	start := time.Now()
	cmd := exec.CommandContext(ctx, w.binPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("local whisper: execution failed (binary %q, flavor %s): %w", w.binPath, w.flavorName(), err)
	}

	// Both flavors write <outdir>/<audiobase>.txt: openai-whisper because
	// --output_format txt names output after the input; whisper.cpp because
	// -of sets the output path prefix and -otxt appends the extension.
	txtPath := filepath.Join(outDir, base+".txt")
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return Result{}, fmt.Errorf("local whisper: read output: %w", err)
	}

	text := strings.TrimSpace(string(data))
	debug.Log("stt", "local whisper: transcribed in %s, %d chars", time.Since(start).Round(time.Millisecond), len(text))

	return Result{
		Text:     text,
		Provider: "whisper-local",
		Model:    modelUsed,
	}, nil
}

// openaiArgs builds the Python openai-whisper CLI command line.
func (w *LocalWhisper) openaiArgs(audioPath, outDir string) ([]string, string) {
	model := w.model
	if model == "" {
		model = "turbo"
	}
	args := []string{
		audioPath,
		"--model", model,
		"--output_dir", outDir,
		"--output_format", "txt",
		"--verbose", "False",
	}
	if w.language != "" {
		args = append(args, "--language", w.language)
	}
	return args, model
}

// cppArgs builds the whisper.cpp CLI command line: the model must be a
// ggml-*.bin file passed via -m, the audio via -f, and output goes to
// <of-prefix>.txt when -otxt is given.
func (w *LocalWhisper) cppArgs(audioPath, outDir, audioBase string) ([]string, string) {
	args := []string{
		"-m", w.cppModel,
		"-f", audioPath,
		"-otxt",
		"-of", filepath.Join(outDir, audioBase),
	}
	if w.language != "" {
		args = append(args, "-l", w.language)
	}
	return args, w.cppModel
}

func (w *LocalWhisper) flavorName() string {
	if w.flavor == flavorCPP {
		return "whisper.cpp"
	}
	return "openai-whisper"
}

func (w *LocalWhisper) ensureAvailable() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.checked {
		if !w.available {
			return fmt.Errorf("local whisper: %s", w.unavailableReason)
		}
		return nil
	}
	w.checked = true

	explicitBin := w.binPath
	if explicitBin != "" {
		if p, err := exec.LookPath(explicitBin); err == nil {
			w.binPath = p
			if err := w.probeFlavor(); err == nil {
				debug.Log("stt", "local whisper: found binary at %s", p)
				return nil
			}
			return w.unavailableErr()
		}
	}
	// Auto-detect common whisper binaries. "whisper" (openai-whisper) is
	// preferred because it needs no extra model setup; whisper.cpp variants
	// are only accepted when a usable ggml model can be located.
	var probeErr error
	for _, candidate := range []string{"whisper", "whisper-cpp", "whisper-cli"} {
		p, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		w.binPath = p
		if err := w.probeFlavor(); err == nil {
			debug.Log("stt", "local whisper: found binary at %s", p)
			return nil
		}
		// This candidate exists but is not usable (e.g. whisper.cpp without
		// a ggml model); remember why and keep looking for another flavor.
		probeErr = err
	}
	switch {
	case probeErr != nil:
		// A binary was found but is unusable; report the concrete cause
		// (e.g. missing ggml model) instead of a misleading "not found".
		w.unavailableReason = probeErr.Error()
	case explicitBin != "":
		w.unavailableReason = fmt.Sprintf("binary %q not found in PATH and no whisper/whisper-cpp/whisper-cli candidate available", explicitBin)
	default:
		w.unavailableReason = "no whisper binary found in PATH"
	}
	return w.unavailableErr()
}

// probeFlavor validates that the currently detected binary can actually be
// invoked with the argument set Transcribe will build. For whisper.cpp this
// means resolving a ggml-*.bin model; without one the binary must not be
// marked available (issue #969).
func (w *LocalWhisper) probeFlavor() error {
	w.flavor = flavorFromBin(w.binPath)
	if w.flavor != flavorCPP {
		w.available = true
		return nil
	}
	modelPath, err := w.resolveCPPModel()
	if err != nil {
		w.available = false
		w.unavailableReason = err.Error()
		return err
	}
	w.cppModel = modelPath
	w.available = true
	return nil
}

func (w *LocalWhisper) unavailableErr() error {
	return fmt.Errorf("local whisper: %s", w.unavailableReason)
}

// cppModelSearchDirs returns the directories scanned for ggml-*.bin files
// when no explicit model is configured. It is a variable so tests can make
// auto-detection hermetic.
var cppModelSearchDirs = defaultCPPModelSearchDirs

func defaultCPPModelSearchDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".cache", "whisper"),
			filepath.Join(home, ".local", "share", "whisper.cpp", "models"),
		)
	}
	return append(dirs,
		"models", // whisper.cpp build-tree default
		"/usr/share/whisper.cpp/models",
		"/usr/local/share/whisper.cpp/models",
	)
}

// resolveCPPModel locates a ggml model file for whisper.cpp:
//  1. the model value from NewLocalWhisper (wired to im.stt.local_model),
//  2. the GGCODE_WHISPER_CPP_MODEL environment variable,
//  3. common download/build locations (first ggml-*.bin found, sorted).
//
// The returned error explains what to configure; it must never surface later
// as a generic "execution failed".
func (w *LocalWhisper) resolveCPPModel() (string, error) {
	if w.model != "" {
		if isRegularFile(w.model) {
			return w.model, nil
		}
		return "", fmt.Errorf("whisper.cpp model %q not found: set im.stt.local_model to an existing ggml-*.bin file path", w.model)
	}
	if p := strings.TrimSpace(os.Getenv("GGCODE_WHISPER_CPP_MODEL")); p != "" {
		if isRegularFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("GGCODE_WHISPER_CPP_MODEL=%q does not point to an existing file", p)
	}
	var searched []string
	for _, dir := range cppModelSearchDirs() {
		if dir == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "ggml-*.bin"))
		if len(matches) > 0 {
			sort.Strings(matches)
			debug.Log("stt", "local whisper: using ggml model %s", matches[0])
			return matches[0], nil
		}
		searched = append(searched, dir)
	}
	return "", fmt.Errorf("whisper.cpp detected but no ggml model found: set im.stt.local_model (or GGCODE_WHISPER_CPP_MODEL) to a ggml-*.bin file path; searched: %s", strings.Join(searched, ", "))
}

func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// Available checks if local whisper is usable without blocking.
func (w *LocalWhisper) Available() bool {
	return w.ensureAvailable() == nil
}
