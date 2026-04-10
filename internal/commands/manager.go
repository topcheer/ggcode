package commands

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	loader         *Loader
	mu             sync.RWMutex
	commands       map[string]*Command
	signature      string
	extraProviders []func() []*Command
}

func NewManager(projectDir string) *Manager {
	m := &Manager{loader: NewLoader(projectDir)}
	m.Reload()
	return m
}

func (m *Manager) Reload() bool {
	if m == nil {
		return false
	}
	cmds := m.loader.Load()
	sig := commandSetSignature(cmds)
	m.mu.Lock()
	defer m.mu.Unlock()
	if sig == m.signature {
		return false
	}
	m.commands = cmds
	m.signature = sig
	return true
}

func (m *Manager) Commands() map[string]*Command {
	return m.combinedCommands()
}

func (m *Manager) UserSlashCommands() map[string]*Command {
	all := m.combinedCommands()
	out := make(map[string]*Command)
	for name, cmd := range all {
		if !cmd.UserSlashVisible() {
			continue
		}
		out[name] = cmd
	}
	return out
}

func (m *Manager) List() []*Command {
	all := m.combinedCommands()
	out := make([]*Command, 0, len(all))
	for _, cmd := range all {
		out = append(out, cmd)
	}
	usage := loadUsageSnapshot()
	now := time.Now()
	sort.Slice(out, func(i, j int) bool {
		iScore := usageScoreForCommand(out[i], usage, now)
		jScore := usageScoreForCommand(out[j], usage, now)
		if iScore != jScore {
			return iScore > jScore
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].LoadedFrom != out[j].LoadedFrom {
			return out[i].LoadedFrom < out[j].LoadedFrom
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) RecordUsage(name string) {
	_ = RecordUsage(name)
}

func usageScoreForCommand(cmd *Command, usage map[string]skillUsageEntry, now time.Time) float64 {
	if cmd == nil {
		return 0
	}
	return usageScore(usage[normalizeSkillName(cmd.Name)], now)
}

func loadUsageSnapshot() map[string]skillUsageEntry {
	skillUsageMu.Lock()
	defer skillUsageMu.Unlock()
	usage, err := loadUsageLocked()
	if err != nil {
		return map[string]skillUsageEntry{}
	}
	return usage
}

func (m *Manager) Get(name string) (*Command, bool) {
	all := m.combinedCommands()
	cmd, ok := all[strings.TrimPrefix(strings.TrimSpace(name), "/")]
	return cmd, ok
}

func (m *Manager) SetExtraProviders(providers ...func() []*Command) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extraProviders = append([]func() []*Command(nil), providers...)
}

func (m *Manager) combinedCommands() map[string]*Command {
	m.mu.RLock()
	base := make(map[string]*Command, len(m.commands))
	for name, cmd := range m.commands {
		base[name] = cmd
	}
	providers := append([]func() []*Command(nil), m.extraProviders...)
	m.mu.RUnlock()

	out := make(map[string]*Command)
	for _, cmd := range bundledSkills() {
		if cmd == nil || strings.TrimSpace(cmd.Name) == "" {
			continue
		}
		out[cmd.Name] = cmd
	}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		for _, cmd := range provider() {
			if cmd == nil || strings.TrimSpace(cmd.Name) == "" {
				continue
			}
			out[cmd.Name] = cmd
		}
	}
	for name, cmd := range base {
		out[name] = cmd
	}
	return out
}

func commandSetSignature(cmds map[string]*Command) string {
	names := make([]string, 0, len(cmds))
	for name := range cmds {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		cmd := cmds[name]
		b.WriteString(name)
		b.WriteString("|")
		b.WriteString(string(cmd.Source))
		b.WriteString("|")
		b.WriteString(string(cmd.LoadedFrom))
		b.WriteString("|")
		b.WriteString(cmd.Path)
		b.WriteString("|")
		b.WriteString(cmd.Description)
		b.WriteString("|")
		b.WriteString(cmd.WhenToUse)
		b.WriteString("|")
		b.WriteString(cmd.Template)
		b.WriteString("\n")
	}
	return b.String()
}
