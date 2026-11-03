package agent

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

const auditLedgerEnv = "GGCODE_AUDIT_LEDGER"

const auditErrMax = 300

type auditLedgerState struct {
	mu     sync.Mutex
	ledger *audit.Ledger
	path   string
}

func newAuditLedgerState() *auditLedgerState {
	path := strings.TrimSpace(os.Getenv(auditLedgerEnv))
	if path == "" {
		return &auditLedgerState{}
	}
	lg, err := audit.Open(path, "")
	if err != nil {
		debug.Log("agent", "[audit-ledger] disabled, open failed: %v", err)
		return &auditLedgerState{}
	}
	debug.Log("agent", "[audit-ledger] enabled, ledger file: %s", path)
	return &auditLedgerState{ledger: lg, path: path}
}

func (a *Agent) auditToolResult(name string, args json.RawMessage, status, errMsg string, dur time.Duration, invariantID ...string) {
	st := a.auditLedger
	if st == nil || st.ledger == nil {
		return
	}
	if errMsg != "" && len(errMsg) > auditErrMax {
		errMsg = errMsg[:auditErrMax]
	}
	ev := audit.Event{
		Tool:       name,
		Status:     status,
		InputHash:  toolreplay.HashInput(args),
		DurationMS: dur.Milliseconds(),
		Err:        errMsg,
	}
	if len(invariantID) > 0 {
		ev.InvariantID = invariantID[0]
	}
	if a != nil {
		ev.Session = a.SessionID()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, err := st.ledger.Append(ev); err != nil {
		debug.Log("agent", "[audit-ledger] append failed for %s: %v", name, err)
	}
}

func (a *Agent) auditToolExecution(name string, args json.RawMessage, result tool.Result, err error, dur time.Duration) {
	switch {
	case err != nil:
		a.auditToolResult(name, args, audit.StatusError, err.Error(), dur)
	case result.IsError:
		a.auditToolResult(name, args, audit.StatusError, result.Content, dur)
	default:
		a.auditToolResult(name, args, audit.StatusOK, "", dur)
	}
}

func (a *Agent) auditAnchor() {
	st := a.auditLedger
	if st == nil || st.ledger == nil {
		return
	}
	if err := st.ledger.Anchor(); err != nil {
		debug.Log("agent", "[audit-ledger] anchor failed: %v", err)
	}
}
