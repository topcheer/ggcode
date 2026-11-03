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

type whisperFlavor int

const (
	flavorOpenAI whisperFlavor = iota
	flavorCPP
)

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

type LocalWhisper struct {
	binPath           string
	flavor            whisperFlavor
	cppModel          string
	model             string
	language          string
	available         bool
	checked           bool
	unavailableReason string
	downloadedModels  map[string]bool
	mu                sync.Mutex
}

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

	bound := 10 * time.Minute
	if w.flavor == flavorOpenAI && !w.modelDownloaded(modelUsed) {
		bound = 60 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, w.binPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("local whisper: execution failed (binary %q, flavor %s): %w", w.binPath, w.flavorName(), err)
	}

	if w.flavor == flavorOpenAI {
		w.markModelDownloaded(modelUsed)
	}

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

func (w *LocalWhisper) modelDownloaded(model string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downloadedModels[model] {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".cache", "whisper", model+".pt")); err == nil {
			if w.downloadedModels == nil {
				w.downloadedModels = map[string]bool{}
			}
			w.downloadedModels[model] = true
			return true
		}
	}
	return false
}

func (w *LocalWhisper) markModelDownloaded(model string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downloadedModels == nil {
		w.downloadedModels = map[string]bool{}
	}
	w.downloadedModels[model] = true
}

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

		probeErr = err
	}
	switch {
	case probeErr != nil:

		w.unavailableReason = probeErr.Error()
	case explicitBin != "":
		w.unavailableReason = fmt.Sprintf("binary %q not found in PATH and no whisper/whisper-cpp/whisper-cli candidate available", explicitBin)
	default:
		w.unavailableReason = "no whisper binary found in PATH"
	}
	return w.unavailableErr()
}

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
		"models",
		"/usr/share/whisper.cpp/models",
		"/usr/local/share/whisper.cpp/models",
	)
}

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

func (w *LocalWhisper) Available() bool {
	return w.ensureAvailable() == nil
}
