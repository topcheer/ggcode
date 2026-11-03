package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

type effectOutcome int

const (
	effectFailed effectOutcome = iota
	effectUncertain
)

type effectRecord struct {
	command string
	workDir string
	at      time.Time
	outcome effectOutcome
}

var (
	maxEffectRecords  = 64
	maxEffectWarnings = 5
	effectRetryWindow = 30 * time.Minute
)

var neverExecutedMarkers = []string{
	"Permission denied for tool",
	"invalid input:",
	"Command blocked:",
	"failed to resolve shell",
	"command job manager not available",
	"failed to start command job",
}

var uncertainMarkers = []string{
	"timed out after",
	"context deadline exceeded",
	"signal: killed",
	"context canceled",
}

type effectLedgerState struct {
	mu      sync.Mutex
	records []effectRecord
	warned  int
	now     func() time.Time
}

func newEffectLedger() *effectLedgerState {
	return &effectLedgerState{now: time.Now}
}

func classifyEffectOutcome(res tool.Result) (effectOutcome, bool) {
	if !res.IsError {
		return 0, false
	}
	c := res.Content
	for _, m := range neverExecutedMarkers {
		if strings.Contains(c, m) {
			return 0, false
		}
	}
	for _, m := range uncertainMarkers {
		if strings.Contains(c, m) {
			return effectUncertain, true
		}
	}
	return effectFailed, true
}

func effectKey(command, workDir string) string {
	return strings.TrimSpace(workDir) + "\x00" + strings.TrimSpace(stripLeadingShellComment(command))
}

func (e *effectLedgerState) priorHint(command, workDir string) string {
	if e == nil || command == "" {
		return ""
	}
	key := effectKey(command, workDir)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	for i := len(e.records) - 1; i >= 0; i-- {
		r := e.records[i]
		if effectKey(r.command, r.workDir) != key {
			continue
		}
		if now.Sub(r.at) > effectRetryWindow {
			return ""
		}
		if e.warned >= maxEffectWarnings {
			return ""
		}
		e.warned++
		return effectHintText(r, now)
	}
	return ""
}

func effectHintText(r effectRecord, now time.Time) string {
	state := "failed"
	why := ""
	if r.outcome == effectUncertain {
		state = "timed out / was interrupted"
		why = " A timed-out or interrupted command may have PARTIALLY landed"
	}
	return fmt.Sprintf(
		"\n[Effect Ledger] This exact command already ran earlier in this session — the most recent prior attempt (%s ago) %s.%s "+
			"Do not treat this run's output as proof about the earlier attempt. Verify the actual external state before "+
			"re-attempting or drawing conclusions (e.g. git log/status for pushes, check the published/deployed artifact, "+
			"re-read files the command may have touched).\n",
		now.Sub(r.at).Round(time.Second), state, why)
}

func (e *effectLedgerState) record(command, workDir string, outcome effectOutcome) {
	if e == nil || command == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, effectRecord{
		command: strings.TrimSpace(stripLeadingShellComment(command)),
		workDir: strings.TrimSpace(workDir),
		at:      e.now(),
		outcome: outcome,
	})
	if len(e.records) > maxEffectRecords {

		e.records = append([]effectRecord(nil), e.records[len(e.records)/2:]...)
	}
}

func (a *Agent) recordEffectAttempt(name string, args []byte, res tool.Result) string {
	if name != "run_command" {
		return ""
	}
	var parsed struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil || parsed.Command == "" {
		return ""
	}
	ledger := a.effectLedger
	if ledger == nil {
		return ""
	}
	outcome, executed := classifyEffectOutcome(res)

	hint := ""
	if executed {
		hint = ledger.priorHint(parsed.Command, parsed.WorkingDir)
		ledger.record(parsed.Command, parsed.WorkingDir, outcome)

		if outcome == effectFailed {
			a.buildIdempot.markBuildFailed(parsed.Command)
		}
	}
	return hint
}
