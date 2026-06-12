package tui

import (
	"fmt"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
)

func defaultPasteShortcut() string {
	switch runtime.GOOS {
	case "darwin":
		return "Cmd+V"
	case "windows":
		return "Ctrl+V"
	default:
		return "Ctrl+Shift+V"
	}
}

func placeholderWithPasteShortcutHint(placeholder string, lang Language) string {
	return placeholderWithPasteShortcutHintFor(placeholder, lang, defaultPasteShortcut())
}

func placeholderWithPasteShortcutHintFor(placeholder string, lang Language, shortcut string) string {
	placeholder = strings.TrimSpace(placeholder)
	shortcut = strings.TrimSpace(shortcut)
	if placeholder == "" || shortcut == "" {
		return placeholder
	}
	switch lang {
	case LangZhCN:
		return fmt.Sprintf("%s（粘贴：%s）", placeholder, shortcut)
	default:
		return fmt.Sprintf("%s (Paste: %s)", placeholder, shortcut)
	}
}

func pasteShortcutHintText(lang Language) string {
	shortcut := defaultPasteShortcut()
	switch lang {
	case LangZhCN:
		return fmt.Sprintf("粘贴：%s", shortcut)
	default:
		return fmt.Sprintf("Paste: %s", shortcut)
	}
}

func renderPasteShortcutHint(lang Language) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" " + pasteShortcutHintText(lang))
}
