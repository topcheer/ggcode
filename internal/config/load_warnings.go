package config

import "sync"

// LoadWarning describes an external config file that failed to parse during
// Load(). The file's contents are ignored for the session (defaults and any
// remaining main-config sections apply), and the next Save() rewrites the
// file from in-memory state, so surfacing the failure at startup is the only
// chance the user gets to fix the YAML before it is overwritten.
type LoadWarning struct {
	File string // path of the file that failed to parse
	Err  string // parse error text
}

// maxLoadWarnings bounds the collector. A long-running process reparses these
// files on hot-reload without a consumer for the warnings, so the slice must
// stay small; startup surfaces at most a handful of files anyway.
const maxLoadWarnings = 16

var (
	loadWarningsMu sync.Mutex
	loadWarnings   []LoadWarning
)

// recordLoadWarning appends a parse failure to the process-wide warning
// collector. Best-effort by design: recording never fails and never changes
// load semantics (the loader still falls back to defaults and still logs to
// the debug channel).
func recordLoadWarning(file, errText string) {
	loadWarningsMu.Lock()
	defer loadWarningsMu.Unlock()
	if len(loadWarnings) >= maxLoadWarnings {
		return
	}
	loadWarnings = append(loadWarnings, LoadWarning{File: file, Err: errText})
}

// TakeConfigLoadWarnings returns and clears the warnings collected during
// config loading. Consume-once: the CLI startup path calls this a single time
// to surface warnings to the user (system notice in the TUI, stderr in pipe
// mode). An empty result is the normal no-corruption case.
func TakeConfigLoadWarnings() []LoadWarning {
	loadWarningsMu.Lock()
	defer loadWarningsMu.Unlock()
	ws := loadWarnings
	loadWarnings = nil
	return ws
}
