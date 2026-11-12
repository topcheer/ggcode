package main

import (
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/audit"
)

func auditVizStatusClass(status string, hasErr bool) string {
	switch {
	case hasErr:
		return "st-err"
	case status == audit.StatusOK || status == audit.StatusUserApproved:
		return "st-ok"
	case status == audit.StatusUserDenied || status == audit.StatusPolicyDenied || status == audit.StatusInvalid:
		return "st-denied"
	case status == audit.StatusAskTimeout:
		return "st-timeout"
	case status == audit.StatusCancelled:
		return "st-cancel"
	default:
		return "st-ok"
	}
}

// renderAuditViz builds the self-contained HTML decision timeline. All
// model/user-derived text (rationale, err, tool, ids) passes through
// html.EscapeString; no client-side data injection is used (filtering
// works on escaped row attributes via vanilla JS) - zero XSS surface.
func renderAuditViz(path, session string, entries []audit.Entry, rep audit.Report) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\">")
	b.WriteString("<title>ggcode audit viz</title><style>")
	b.WriteString("body{font-family:-apple-system,sans-serif;margin:24px;background:#111;color:#ddd}")
	b.WriteString(".banner{padding:10px 14px;border-radius:8px;margin-bottom:16px;font-weight:600}")
	b.WriteString(".banner.ok{background:#12351f;color:#7ee787}.banner.bad{background:#3d1212;color:#ff7b72}")
	b.WriteString(".cards{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:16px}")
	b.WriteString(".card{background:#1b1f24;border-radius:8px;padding:8px 14px;font-size:13px}")
	b.WriteString(".card b{font-size:18px;display:block}")
	b.WriteString("input,select{background:#1b1f24;color:#ddd;border:1px solid #333;border-radius:6px;padding:6px 10px;margin-right:8px}")
	b.WriteString(".row{background:#161a1e;border-left:4px solid #444;border-radius:6px;padding:8px 12px;margin:6px 0;font-size:13px}")
	b.WriteString(".row.st-ok{border-left-color:#3fb950}.row.st-err{border-left-color:#f85149}")
	b.WriteString(".row.st-denied{border-left-color:#d29922}.row.st-timeout{border-left-color:#a371f7}.row.st-cancel{border-left-color:#656d76}")
	b.WriteString(".seq{color:#8b949e;font-size:11px}.tool{font-weight:700;color:#58a6ff}")
	b.WriteString(".badge{display:inline-block;background:#21262d;border-radius:10px;padding:1px 8px;margin-left:6px;font-size:11px;color:#8b949e}")
	b.WriteString(".rat{color:#7ee787;margin-top:4px}.err{color:#ff7b72;margin-top:4px}")
	b.WriteString(".dur{color:#8b949e;float:right}")
	b.WriteString("</style></head><body>")

	// Chain verification banner (degraded loudly, never silently).
	if rep.OK() {
		b.WriteString("<div class='banner ok'>Chain VERIFIED — hash chain intact")
		if !rep.HeadAnchored {
			b.WriteString(" (no head anchor file)")
		}
		b.WriteString("</div>")
	} else {
		b.WriteString("<div class='banner bad'>Chain verification FAILED — treat this timeline as untrusted")
		if rep.FirstBreak != nil {
			fmt.Fprintf(&b, " — first break at seq %d: %s", rep.FirstBreak.Seq, html.EscapeString(rep.FirstBreak.Reason))
		}
		if rep.Truncated != nil {
			fmt.Fprintf(&b, " — tail truncation detected (anchored seq %d)", rep.Truncated.AnchoredSeq)
		}
		b.WriteString("</div>")
	}

	fmt.Fprintf(&b, "<div class='cards'><span class='card'><b>%d</b>entries</span>", len(entries))
	if session != "" {
		fmt.Fprintf(&b, "<span class='card'>session filter: <b>%s</b></span>", html.EscapeString(session))
	}
	nRationale, nErr, nDenied := 0, 0, 0
	toolSet := map[string]bool{}
	for _, e := range entries {
		if e.Rationale != "" {
			nRationale++
		}
		if e.Err != "" {
			nErr++
		}
		if e.Status == audit.StatusUserDenied || e.Status == audit.StatusPolicyDenied || e.Status == audit.StatusInvalid || e.Status == audit.StatusAskTimeout {
			nDenied++
		}
		toolSet[e.Tool] = true
	}
	fmt.Fprintf(&b, "<span class='card'><b>%d</b>with rationale</span><span class='card'><b>%d</b>errors</span><span class='card'><b>%d</b>gate/denied</span><span class='card'><b>%d</b>tools</span></div>", nRationale, nErr, nDenied, len(toolSet))

	b.WriteString("<div><input id='q' placeholder='filter text…' oninput='filt()'><select id='st' onchange='filt()'>")
	b.WriteString("<option value=''>all statuses</option><option value='st-ok'>ok/approved</option><option value='st-err'>error</option><option value='st-denied'>gate/denied</option><option value='st-timeout'>ask_timeout</option><option value='st-cancel'>cancelled</option></select>")
	b.WriteString("<select id='tl' onchange='filt()'><option value=''>all tools</option>")
	for t := range toolSet {
		fmt.Fprintf(&b, "<option value='%s'>%s</option>", html.EscapeString(t), html.EscapeString(t))
	}
	b.WriteString("</select><span id='cnt' class='seq'></span></div><div id='rows'>")

	for _, e := range entries {
		cls := auditVizStatusClass(e.Status, e.Err != "")
		attrs := fmt.Sprintf("data-status='%s' data-tool='%s'", html.EscapeString(cls), html.EscapeString(e.Tool))
		fmt.Fprintf(&b, "<div class='row %s' %s><span class='seq'>#%d %s</span> <span class='tool'>%s</span>", cls, attrs, e.Seq, html.EscapeString(e.Time), html.EscapeString(e.Tool))
		fmt.Fprintf(&b, "<span class='badge'>%s</span>", html.EscapeString(e.Status))
		if e.DurationMS > 0 {
			fmt.Fprintf(&b, "<span class='dur'>%dms</span>", e.DurationMS)
		}
		if e.Session != "" {
			fmt.Fprintf(&b, "<span class='badge'>sess:%s</span>", html.EscapeString(e.Session))
		}
		if e.InvariantID != "" {
			fmt.Fprintf(&b, "<span class='badge'>invariant:%s</span>", html.EscapeString(e.InvariantID))
		}
		if e.Peer != "" || e.TaskID != "" {
			fmt.Fprintf(&b, "<span class='badge'>peer:%s task:%s</span>", html.EscapeString(e.Peer), html.EscapeString(e.TaskID))
		}
		if e.Rationale != "" {
			fmt.Fprintf(&b, "<div class='rat'>why: %s</div>", html.EscapeString(e.Rationale))
		}
		if e.Err != "" {
			fmt.Fprintf(&b, "<div class='err'>err: %s</div>", html.EscapeString(e.Err))
		}
		b.WriteString("</div>")
	}
	b.WriteString("</div><script>function filt(){var q=document.getElementById('q').value.toLowerCase(),st=document.getElementById('st').value,tl=document.getElementById('tl').value,n=0;var rows=document.querySelectorAll('#rows .row');for(var i=0;i<rows.length;i++){var r=rows[i];var ok=(!st||r.getAttribute('data-status')==st)&&(!tl||r.getAttribute('data-tool')==tl)&&(!q||r.textContent.toLowerCase().indexOf(q)>=0);r.style.display=ok?'':'none';if(ok)n++}document.getElementById('cnt').textContent=n+' / '+rows.length+' shown'}filt()</script>")
	b.WriteString("</body></html>")
	return b.String()
}

func newAuditVizCmd() *cobra.Command {
	var (
		pathArg    string
		session    string
		outPath    string
		noOpen     bool
		maxEntries int
	)
	cmd := &cobra.Command{
		Use:   "viz",
		Short: "Render the audit ledger as an interactive HTML decision timeline",
		Long: "Render the audit ledger as a self-contained, offline HTML decision\n" +
			"timeline: chain-verification banner, summary cards, and a filterable\n" +
			"per-entry view (status colors, rationale, errors, invariant/peer\n" +
			"attribution). All derived text is HTML-escaped; the file has no\n" +
			"external assets.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := strings.TrimSpace(pathArg)
			if path == "" {
				path = strings.TrimSpace(os.Getenv(auditCmdEnv))
			}
			if path == "" {
				return fmt.Errorf("no ledger path: pass --path or set %s", auditCmdEnv)
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("ledger not found at %s: %w", path, err)
			}
			sess := strings.TrimSpace(session)
			entries, err := audit.Timeline(path, sess)
			if err != nil {
				return err
			}
			if maxEntries > 0 && len(entries) > maxEntries {
				entries = entries[len(entries)-maxEntries:] // most recent N
			}
			rep, err := audit.Verify(path)
			if err != nil {
				return err
			}
			if outPath == "" {
				outPath = "ggaudit.html"
			}
			if err := os.WriteFile(outPath, []byte(renderAuditViz(path, sess, entries, rep)), 0o600); err != nil {
				return fmt.Errorf("writing viz html: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d entries, chain %s)\n", outPath, len(entries), map[bool]string{true: "VERIFIED", false: "FAILED"}[rep.OK()])
			if !noOpen {
				openBrowserOut(outPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pathArg, "path", "", "audit ledger JSONL path (default $"+auditCmdEnv+")")
	cmd.Flags().StringVar(&session, "session", "", "restrict the timeline to one session ID")
	cmd.Flags().StringVar(&outPath, "o", "ggaudit.html", "output HTML path")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open the generated file in a browser")
	cmd.Flags().IntVar(&maxEntries, "max-entries", 0, "keep only the most recent N entries (0 = all)")
	return cmd
}
