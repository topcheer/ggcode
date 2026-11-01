package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// #4861 (reframed checklist, items 2+3+4): onboarding run-record with
// resume + config-write invalidation, the setup-time probe step, and the
// first-use bridge guidance. Secrets are NEVER persisted here - the record
// tracks progress only (platform + step + timestamp); credentials live in
// the config via the existing panels.

// imOnboardRecord is the persisted onboarding progress for one platform.
type imOnboardRecord struct {
	Platform  string    `json:"platform"`
	Step      int       `json:"step"` // 1-based step the wizard is on
	UpdatedAt time.Time `json:"updated_at"`
}

// imOnboardRecordPath returns the run-record location under the workspace's
// .ggcode dir (shared across sessions of this workspace; one wizard at a
// time is the supported shape).
func imOnboardRecordPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".ggcode", "im-onboarding.json")
}

// loadIMOnboardRecord reads the current run-record; nil when absent/corrupt
// (a corrupt record must never block starting fresh).
func loadIMOnboardRecord(workspaceDir string) *imOnboardRecord {
	data, err := os.ReadFile(imOnboardRecordPath(workspaceDir))
	if err != nil {
		return nil
	}
	var rec imOnboardRecord
	if json.Unmarshal(data, &rec) != nil || rec.Platform == "" {
		return nil
	}
	return &rec
}

// saveIMOnboardRecord persists progress (best-effort; failure is surfaced).
func saveIMOnboardRecord(workspaceDir string, rec imOnboardRecord) error {
	if err := os.MkdirAll(filepath.Dir(imOnboardRecordPath(workspaceDir)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(imOnboardRecordPath(workspaceDir), data, 0o644)
}

// clearIMOnboardRecord drops the record - called on completion AND by every
// config-write invalidation hook (hand edits are caught by the mtime check
// in imOnboardingDue: record stale vs config file mtime).
func clearIMOnboardRecord(workspaceDir string) {
	_ = os.Remove(imOnboardRecordPath(workspaceDir))
}

// imOnboardingDue reports whether onboarding needs attention: an unfinished
// run-record exists AND it predates the last config write (config edits
// invalidate stale wizard progress). A record newer than the config file is
// mid-flow and resumable, not "due anew".
func imOnboardingDue(workspaceDir, configPath string) bool {
	rec := loadIMOnboardRecord(workspaceDir)
	if rec == nil {
		return false
	}
	cfgInfo, err := os.Stat(configPath)
	if err != nil {
		return true // no readable config: any record counts as due
	}
	return rec.UpdatedAt.Before(cfgInfo.ModTime())
}

// imOnboardProbeTail is the wizard's final verification step (checklist 3):
// setup-time connectivity probe + first-use bridge (checklist 4). The probe
// reuses the adapter's own paths - pairing for QR platforms, getMe/ping for
// token platforms - fired via /im status after the adapter starts, and the
// first-use bridge sends the standard welcome/self-test through the channel.
const imOnboardProbeTail = `
Final steps (verification + first use):
  1. Save the panel, then restart the adapter (disable + enable, or restart ggcode).
  2. Run /im status - the platform's row must read connected. failed/expired
     rows carry the adapter's last error; fix the credential in the panel and
     repeat (the probe runs at setup time - no live traffic needed).
  3. First use (closes the loop end-to-end): send the welcome self-test
     through the channel itself:
        - via the IM tool:  action "send", adapter "<adapter>", message "onboard self-test"
        - or from the bound channel: send any command; the reply IS the proof.
  4. The onboarding record clears itself on completion; /im onboard resumes
     an interrupted run at the saved step.`

// renderIMFullStatusTable renders the five-state registry (checklist 1):
// connected / failed / expired / never-configured (+ muted/disabled) per
// platform with the adapter's last error, derived from the runtime snapshot
// states cross-referenced with the wizard's full platform list.
func renderIMFullStatusTable(platformStates []IMAdapterStateInfo, cfgConfigured map[string]string) string {
	wizard := imOnboardWizard()
	stateByPlatform := map[string]IMAdapterStateInfo{}
	for _, s := range platformStates {
		platform := strings.ToLower(strings.TrimSpace(s.Platform))
		if platform == "" {
			platform = s.Adapter
		}
		stateByPlatform[platform] = s
	}
	names := make([]string, 0, len(wizard))
	for name := range wizard {
		names = append(names, name)
	}
	// Include configured-but-not-in-wizard platforms too.
	for platform := range cfgConfigured {
		if _, ok := wizard[platform]; !ok {
			names = append(names, platform)
		}
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("IM Platform Registry (full state)\n")
	sb.WriteString("==================================\n")
	sb.WriteString(fmt.Sprintf("%-12s %-16s %-14s %s\n", "PLATFORM", "STATE", "ADAPTER", "LAST ERROR"))
	for _, name := range names {
		adapter := cfgConfigured[name]
		if adapter == "" {
			adapter = "-"
		}
		state := "never-configured"
		lastErr := ""
		if st, ok := stateByPlatform[name]; ok {
			state = classifyIMAdapterState(st, cfgConfigured[name] != "")
			lastErr = st.LastError
		} else if cfgConfigured[name] != "" {
			state = "not connected (adapter not running)"
		}
		if lastErr != "" && len(lastErr) > 60 {
			lastErr = lastErr[:60] + "…"
		}
		sb.WriteString(fmt.Sprintf("%-12s %-16s %-14s %s\n", name, state, adapter, lastErr))
	}
	sb.WriteString("\nStates: connected | failed | expired | muted | disabled | not connected | never-configured.\nLive values refresh with /im status; the im tool (action status) carries per-adapter detail.")
	return sb.String()
}

// IMAdapterStateInfo is the runtime adapter state the table derives from
// (mirrors the im tool's adapter info; kept local so the TUI does not
// import the tool package).
type IMAdapterStateInfo struct {
	Adapter   string
	Platform  string
	Healthy   bool
	Status    string
	Muted     bool
	Disabled  bool
	LastError string
}

// classifyIMAdapterState maps a runtime adapter state onto the five-state
// vocabulary (checklist 1). Explicit adapter status strings win; healthy
// defaults to connected; an unhealthy adapter with an auth-ish last error
// reads expired, otherwise failed.
func classifyIMAdapterState(st IMAdapterStateInfo, configured bool) string {
	switch strings.ToLower(strings.TrimSpace(st.Status)) {
	case "connected", "connecting", "reconnecting", "disconnected", "muted", "disabled":
		// pass through lowercased below
	default:
		if st.Disabled {
			return "disabled"
		}
		if st.Muted {
			return "muted"
		}
		if st.Healthy {
			return "connected"
		}
		le := strings.ToLower(st.LastError)
		if strings.Contains(le, "token") || strings.Contains(le, "auth") ||
			strings.Contains(le, "401") || strings.Contains(le, "expired") ||
			strings.Contains(le, "credential") {
			return "expired"
		}
		if !configured {
			return "never-configured"
		}
		return "failed"
	}
	s := strings.ToLower(strings.TrimSpace(st.Status))
	if s == "disconnected" && st.Healthy {
		return "connected"
	}
	return s
}
