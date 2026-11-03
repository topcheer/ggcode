package agentruntime

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const toolchainTimeout = 3 * time.Second

var (
	toolchainCache   = make(map[string]string)
	toolchainCacheMu sync.RWMutex
)

type toolchainProbe struct {
	binary  string
	args    []string
	markers []string
	label   string
}

var toolchainProbes = []toolchainProbe{
	{
		binary:  "go",
		args:    []string{"version"},
		markers: []string{"go.mod", "go.work"},
		label:   "Go",
	},
	{
		binary:  "node",
		args:    []string{"--version"},
		markers: []string{"package.json"},
		label:   "Node",
	},
	{
		binary:  "npm",
		args:    []string{"--version"},
		markers: []string{"package.json"},
		label:   "npm",
	},
	{
		binary:  "python3",
		args:    []string{"--version"},
		markers: []string{"requirements.txt", "pyproject.toml", "setup.py", "Pipfile"},
		label:   "Python",
	},
	{
		binary:  "cargo",
		args:    []string{"--version"},
		markers: []string{"Cargo.toml"},
		label:   "Rust/Cargo",
	},
	{
		binary:  "java",
		args:    []string{"-version"},
		markers: []string{"pom.xml", "build.gradle", "build.gradle.kts"},
		label:   "Java",
	},
}

func runVersionCmd(binary string, args []string) string {
	toolchainCacheMu.RLock()
	if v, ok := toolchainCache[binary]; ok {
		toolchainCacheMu.RUnlock()
		return v
	}
	toolchainCacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), toolchainTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)

	out, err := cmd.CombinedOutput()
	cancel()

	result := ""
	if err == nil && len(out) > 0 {
		result = strings.TrimSpace(string(out))
	}

	toolchainCacheMu.Lock()
	toolchainCache[binary] = result
	toolchainCacheMu.Unlock()
	return result
}

func resetToolchainCache() {
	toolchainCacheMu.Lock()
	toolchainCache = make(map[string]string)
	toolchainCacheMu.Unlock()
}

func detectToolchain(workingDir string) []string {
	if workingDir == "" {
		return nil
	}

	var results []string
	for _, probe := range toolchainProbes {
		if !toolchainHasMarker(workingDir, probe.markers) {
			continue
		}
		ver := runVersionCmd(probe.binary, probe.args)
		if ver == "" {
			continue
		}

		if idx := strings.IndexByte(ver, '\n'); idx >= 0 {
			ver = ver[:idx]
		}
		results = append(results, probe.label+": "+ver)
	}
	return results
}

func toolchainHasMarker(workingDir string, markers []string) bool {
	for _, m := range markers {
		if _, err := os.Stat(workingDir + string(os.PathSeparator) + m); err == nil {
			return true
		}
	}
	return false
}

func toolchainSection(workingDir string) string {
	versions := detectToolchain(workingDir)
	if len(versions) == 0 {
		return ""
	}
	return "\n\n## Toolchain\nInstalled toolchain versions (for writing compatible code):\n- " +
		strings.Join(versions, "\n- ")
}
