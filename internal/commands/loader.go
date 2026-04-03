package commands

import (
	"os"
	"path/filepath"
	"strings"
)

// Loader finds and loads custom slash commands from ~/.ggcode/commands/ and .ggcode/commands/.
type Loader struct {
	dirs []string
}

// NewLoader creates a loader scanning global and project-local command directories.
// projectDir is the current working directory of the project.
func NewLoader(projectDir string) *Loader {
	home, _ := os.UserHomeDir()
	return &Loader{
		dirs: []string{
			filepath.Join(home, ".ggcode", "commands"),
			filepath.Join(projectDir, ".ggcode", "commands"),
		},
	}
}

// Load scans all command directories and returns loaded commands.
// Later directories override earlier ones for the same command name.
func (l *Loader) Load() map[string]*Command {
	cmds := make(map[string]*Command)
	for _, dir := range l.dirs {
		loadFromDir(dir, cmds)
	}
	return cmds
}

// CommandDirs returns the directories being scanned (for display purposes).
func (l *Loader) CommandDirs() []string {
	return l.dirs
}

func loadFromDir(dir string, cmds map[string]*Command) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		content := string(data)
		// First non-empty line as description
		desc := ""
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				desc = line
				break
			}
		}
		cmds[name] = &Command{
			Name:        name,
			Template:    content,
			Description: desc,
		}
	}
}
