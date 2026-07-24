package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/session"
)

// pickSessionInteractive lists up to 10 sessions for the current workspace and reads the user's choice from stdin.
// Returns the selected session ID, or empty string to start a new session.
func pickSessionInteractive(store session.Store, lang daemon.Lang) string {
	sessions, err := store.List()
	if err != nil || len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.empty"))
		return ""
	}

	// Filter to current workspace only
	workingDir, _ := os.Getwd()
	filtered := agentruntime.FilterWorkspaceSessions(sessions, workingDir)
	if len(filtered) == 0 {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.empty"))
		return ""
	}

	// Limit to latest 10
	if len(filtered) > 10 {
		filtered = filtered[:10]
	}

	fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.title"))
	for i, s := range filtered {
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		fmt.Fprintf(os.Stderr, daemon.Tr(lang, "daemon.resume.item")+"\n", i+1, s.ID, title)
	}
	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.resume.prompt"))

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(filtered) {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.invalid"))
		return ""
	}
	return filtered[idx-1].ID
}
