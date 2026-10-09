package audit

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Reader-side reconstruction of a run from the audit ledger ("the log exists,
// the reader is missing" — Meta Muse Code 2026-08 shipped an append-only event
// log for crash recovery; the audit-trail gap is the human-review consumption
// surface that answers, after the fact, "what did the agent actually do and
// which decisions mattered").
//
// Digest is read-only: it never mutates the ledger, never needs the ledger to
// be open, and works on any chain state (a broken chain digests everything up
// to the break, exactly the entries Verify still vouches for).

// ToolStat is a per-tool rollup inside a digest.
type ToolStat struct {
	Tool   string
	Total  int
	OK     int
	Errors int
	Other  int // cancelled / invalid / approval decisions
	AvgMS  float64
	MaxMS  int64
}

// ApprovalLine is one human-gate decision on the timeline.
type ApprovalLine struct {
	Time   string
	Tool   string
	Status string // approved / user_denied / ask_timeout
	Err    string
}

// InvariantRejection counts rejections caused by a declared invariant (r454).
type InvariantRejection struct {
	InvariantID string
	Count       int
}

// PeerAttribution aggregates events attributed to remote A2A handoffs (r33).
type PeerAttribution struct {
	Peer   string
	TaskID string
	Events int
}

// ErrorCluster groups failed actions by a normalized error prefix, so a digest
// shows "edit_file failed 7x with 'old_text not found'" instead of 7 rows.
type ErrorCluster struct {
	Prefix string
	Count  int
	Tools  []string
}

// RunDigest is the human-readable reconstruction of what a ledger records.
type RunDigest struct {
	Path           string
	Session        string // empty = all sessions
	Entries        int    // entries considered (after session filter)
	SkippedOther   int    // entries belonging to other sessions
	FirstTime      string
	LastTime       string
	WallDurationMS int64 // last.Time - first.Time (coarse run wall time)
	ToolStats      []ToolStat
	Approvals      []ApprovalLine
	Invariants     []InvariantRejection
	Peers          []PeerAttribution
	ErrorClusters  []ErrorCluster
	// Verify is filled by Digest from Verify(path) so the tamper state of the
	// chain fronts the report; a reader must know whether the evidence itself
	// is intact before trusting anything below.
	VerifyOK   bool
	FirstBreak *Break
	Truncated  *Truncation
}

// approvalStatuses are the Event statuses that represent human-gate decisions
// rather than tool executions.
var approvalStatuses = map[string]bool{
	StatusUserApproved: true,
	StatusUserDenied:   true,
	StatusAskTimeout:   true,
}

// Digest reconstructs a run view from the ledger at path. When session is
// non-empty, only entries with that session (or no session set — legacy
// chains) are considered. The chain's Verify report is embedded so consumers
// render the tamper state alongside the reconstruction.
func Digest(path, session string) (RunDigest, error) {
	entries, err := scanEntries(path)
	if err != nil {
		return RunDigest{}, err
	}
	d := RunDigest{Path: path, Session: session, VerifyOK: true}

	tools := map[string]*ToolStat{}
	invariants := map[string]int{}
	peerTasks := map[string]*PeerAttribution{}
	errClusters := map[string]*ErrorCluster{}

	for _, e := range entries {
		// Sentinel for a malformed line: it is Verify's evidence, not digest
		// content. Count it but do not attribute it.
		if e.Seq == 0 {
			continue
		}
		if session != "" && e.Session != "" && e.Session != session {
			d.SkippedOther++
			continue
		}
		d.Entries++
		if d.FirstTime == "" {
			d.FirstTime = e.Time
		}
		d.LastTime = e.Time

		ts := tools[e.Tool]
		if ts == nil {
			ts = &ToolStat{Tool: e.Tool}
			tools[e.Tool] = ts
		}
		ts.Total++
		switch {
		case e.Status == StatusOK:
			ts.OK++
		case e.Status == StatusError:
			ts.Errors++
		default:
			ts.Other++
		}
		ts.MaxMS = maxInt64(ts.MaxMS, e.DurationMS)

		if approvalStatuses[e.Status] {
			d.Approvals = append(d.Approvals, ApprovalLine{
				Time: e.Time, Tool: e.Tool, Status: e.Status, Err: e.Err,
			})
		}
		if e.InvariantID != "" {
			invariants[e.InvariantID]++
		}
		if e.Peer != "" || e.TaskID != "" {
			key := e.Peer + "\x00" + e.TaskID
			pa := peerTasks[key]
			if pa == nil {
				pa = &PeerAttribution{Peer: e.Peer, TaskID: e.TaskID}
				peerTasks[key] = pa
			}
			pa.Events++
		}
		if e.Status == StatusError && e.Err != "" {
			prefix := normalizeErrPrefix(e.Err)
			ec := errClusters[prefix]
			if ec == nil {
				ec = &ErrorCluster{Prefix: prefix}
				errClusters[prefix] = ec
			}
			ec.Count++
			if !contains(ec.Tools, e.Tool) {
				ec.Tools = append(ec.Tools, e.Tool)
			}
		}
	}

	// Averages and duration rollups need a second pass over the same filter.
	if d.FirstTime != "" && d.LastTime != "" {
		if t0, err0 := time.Parse(time.RFC3339Nano, d.FirstTime); err0 == nil {
			if t1, err1 := time.Parse(time.RFC3339Nano, d.LastTime); err1 == nil {
				d.WallDurationMS = t1.Sub(t0).Milliseconds()
			}
		}
	}
	for _, e := range entries {
		if e.Seq == 0 {
			continue
		}
		if session != "" && e.Session != "" && e.Session != session {
			continue
		}
		if ts := tools[e.Tool]; ts != nil {
			ts.AvgMS += float64(e.DurationMS)
		}
	}
	for _, ts := range tools {
		if ts.Total > 0 {
			ts.AvgMS /= float64(ts.Total)
		}
		d.ToolStats = append(d.ToolStats, *ts)
	}
	sort.Slice(d.ToolStats, func(i, j int) bool {
		return d.ToolStats[i].Total > d.ToolStats[j].Total
	})

	for id, n := range invariants {
		d.Invariants = append(d.Invariants, InvariantRejection{InvariantID: id, Count: n})
	}
	sort.Slice(d.Invariants, func(i, j int) bool {
		return d.Invariants[i].Count > d.Invariants[j].Count
	})
	for _, pa := range peerTasks {
		d.Peers = append(d.Peers, *pa)
	}
	sort.Slice(d.Peers, func(i, j int) bool {
		return d.Peers[i].Events > d.Peers[j].Events
	})
	for _, ec := range errClusters {
		d.ErrorClusters = append(d.ErrorClusters, *ec)
	}
	sort.Slice(d.ErrorClusters, func(i, j int) bool {
		return d.ErrorClusters[i].Count > d.ErrorClusters[j].Count
	})

	// Embed chain tamper state: a digest over a broken chain is still useful
	// (everything before the break is vouched), but the break must travel with
	// it or a consumer could mistake post-break absence for "nothing happened".
	// #3692: a Verify IO failure must fail CLOSED - the initial VerifyOK=true
	// used to survive the error branch and Render() reported "VERIFIED
	// intact" for a chain it could not even read. Unverifiable != intact.
	// verifyChain is a seam: the IO error is only reachable via a TOCTOU
	// between Digest's own scanEntries read and Verify's (same path, two
	// opens); tests swap this to exercise the failure deterministically.
	ok, firstBreak, truncated := verifyChain(path)
	d.VerifyOK = ok
	d.FirstBreak = firstBreak
	d.Truncated = truncated
	return d, nil
}

// verifyChain runs Verify and folds its error into a fail-closed triple.
// Package var (not a plain func) so tests can swap it: the IO error is only
// reachable via a TOCTOU between Digest's scanEntries read and Verify's.
var verifyChain = func(path string) (ok bool, firstBreak *Break, truncated *Truncation) {
	rep, err := Verify(path)
	if err != nil {
		return false, nil, nil
	}
	return rep.OK(), rep.FirstBreak, rep.Truncated
}

// Render formats a digest as the human-readable audit report. Blast radius
// (when the caller knows the changed files) is appended as review guidance:
// review depth should match blast radius, not be flat across every run.
func (d RunDigest) Render(changedFiles []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Audit ledger: %s\n", d.Path)
	if d.Session != "" {
		fmt.Fprintf(&b, "Session filter: %s (skipped %d entries from other sessions)\n", d.Session, d.SkippedOther)
	}
	if d.VerifyOK {
		b.WriteString("Chain: VERIFIED intact (hash chain + head anchor)\n")
	} else {
		b.WriteString("Chain: TAMPER EVIDENCE — ")
		if d.FirstBreak != nil {
			fmt.Fprintf(&b, "break at seq %d: %s\n", d.FirstBreak.Seq, d.FirstBreak.Reason)
		}
		if d.Truncated != nil {
			fmt.Fprintf(&b, "tail truncated: anchor at seq %d but file ends at %d\n", d.Truncated.AnchoredSeq, d.Truncated.FileSeq)
		}
		if d.FirstBreak == nil && d.Truncated == nil {
			b.WriteString("verification failed\n")
		}
	}
	fmt.Fprintf(&b, "Entries: %d | span: %s -> %s (%s)\n\n",
		d.Entries, d.FirstTime, d.LastTime, formatMS(d.WallDurationMS))

	if len(d.Approvals) > 0 {
		b.WriteString("Human-gate decisions:\n")
		for _, a := range d.Approvals {
			note := ""
			if a.Err != "" {
				note = " — " + a.Err
			}
			fmt.Fprintf(&b, "  %s  %s  %s%s\n", a.Time, pad(a.Status, 13), a.Tool, note)
		}
		b.WriteString("\n")
	}
	if len(d.Invariants) > 0 {
		b.WriteString("Invariant rejections:\n")
		for _, iv := range d.Invariants {
			fmt.Fprintf(&b, "  %dx  %s\n", iv.Count, iv.InvariantID)
		}
		b.WriteString("\n")
	}
	if len(d.Peers) > 0 {
		b.WriteString("A2A peer attribution:\n")
		for _, p := range d.Peers {
			fmt.Fprintf(&b, "  %s (task %s): %d events\n", orDash(p.Peer), orDash(p.TaskID), p.Events)
		}
		b.WriteString("\n")
	}
	b.WriteString("Tool activity:\n")
	for _, ts := range d.ToolStats {
		fmt.Fprintf(&b, "  %-24s total %4d  ok %4d  err %4d  other %3d  avg %s  max %s\n",
			ts.Tool, ts.Total, ts.OK, ts.Errors, ts.Other,
			formatMS(int64(ts.AvgMS)), formatMS(ts.MaxMS))
	}
	if len(d.ErrorClusters) > 0 {
		b.WriteString("\nError clusters:\n")
		for _, ec := range d.ErrorClusters {
			fmt.Fprintf(&b, "  %dx [%s] %s\n", ec.Count, strings.Join(ec.Tools, ","), ec.Prefix)
		}
	}
	if len(changedFiles) > 0 {
		fmt.Fprintf(&b, "\nBlast radius: %s (from %d changed files)\n", BlastRadius(changedFiles), len(changedFiles))
	}
	return b.String()
}

// Blast radius tiers for review-depth guidance. The ledger deliberately does
// not store file paths (InputHash only — secrets never enter the chain), so
// the changed-file set comes from the caller (git diff of the run's work).
const (
	BlastCopy     = "copy"     // docs, comments, marketing text
	BlastCode     = "code"     // regular source changes
	BlastCritical = "critical" // migrations, auth, infra, CI, secrets
)

// blastRules maps path substrings to the critical tier. Order-independent:
// critical wins over code wins over copy.
var blastRules = []struct {
	sub string
}{
	{"migration"}, {"migrations"},
	{"schema"}, {".sql"},
	{"auth"}, {"permission"}, {"rbac"}, {"token"},
	{"terraform"}, {"k8s/"}, {"kubernetes"}, {"helm"},
	{".github/workflows"}, {"ci/"}, {"deploy"},
	{"secret"}, {"credential"}, {".pem"}, {".env"},
}

// BlastRadius classifies a changed-file set into a review-depth tier.
// Unknown/empty inputs default to BlastCode (never under-review code).
func BlastRadius(files []string) string {
	tier := BlastCopy
	for _, f := range files {
		fl := strings.ToLower(f)
		hit := false
		for _, r := range blastRules {
			if strings.Contains(fl, r.sub) {
				return BlastCritical
			}
		}
		switch {
		case strings.HasSuffix(fl, ".md"), strings.HasPrefix(fl, "docs/"),
			strings.Contains(fl, "README"), strings.HasSuffix(fl, ".txt"):
			// stays copy unless promoted by a rule above
		default:
			hit = true
		}
		if hit {
			tier = BlastCode
		}
	}
	return tier
}

// normalizeErrPrefix reduces an error string to a stable clustering key: the
// first clause up to a delimiter, truncated, with values normalized so the
// same failure shape clusters: paths -> <path>, file-ish tokens -> <file>,
// digit runs -> #. "old_text not found in a.go" and "in /x/b.go" both become
// "old_text not found in <file>".
func normalizeErrPrefix(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n:"); i > 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		switch {
		case strings.ContainsAny(f, `/\`):
			out = append(out, "<path>")
		case isFileIsh(f):
			out = append(out, "<file>")
		default:
			out = append(out, collapseDigits(f))
		}
	}
	return strings.Join(out, " ")
}

// isFileIsh reports whether a bare token looks like a filename: no path
// separators, contains a dot, and consists only of filename-safe characters.
func isFileIsh(f string) bool {
	if len(f) == 0 || len(f) > 64 || !strings.Contains(f, ".") {
		return false
	}
	for i := 0; i < len(f); i++ {
		c := f[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// collapseDigits replaces each maximal digit run with a single '#'.
func collapseDigits(f string) string {
	var b strings.Builder
	prevDigit := false
	for _, r := range f {
		if r >= '0' && r <= '9' {
			if !prevDigit {
				b.WriteByte('#')
			}
			prevDigit = true
			continue
		}
		prevDigit = false
		b.WriteRune(r)
	}
	return b.String()
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatMS(ms int64) string {
	switch {
	case ms >= 60_000:
		return fmt.Sprintf("%.1fm", float64(ms)/60_000)
	case ms >= 1_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1_000)
	default:
		return fmt.Sprintf("%dms", ms)
	}
}
