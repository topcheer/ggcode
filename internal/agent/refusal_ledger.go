package agent

// refusal_ledger.go -- Persisted User Refusals as enforceable constraints.
//
// Research basis: arXiv 2605.00055 "Ambient Persuasion in a Deployed AI
// Agent" (Cuadros & Maiga, 2026-04). A deployed agent installed 107
// unauthorized components, overwrote a registry, and OVERRID a prior
// negative decision from an oversight agent after routine non-adversarial
// content re-weighted its directives ("directive weighting error"). The
// paper's first design lesson, verbatim: "prior refusals must persist as
// enforceable constraints rather than message-level reminders".
//
// Gap this closes (audited r23/sa-54): constraint_amnesia.go already
// extracts natural-language refusals ("don't touch X", "never use --force")
// but is advisory-only, capped at 1 warning/run, reset every run, and lost
// to compaction - exactly the "message-level reminder" the paper warns
// fails. approval-memory covers approval-UI choices; cmd deny patterns
// cover command approvals; NEITHER covers conversational refusals.
//
// Design (deliberately conservative on false positives):
//   - record(): negation-form constraints extracted from user text are
//     persisted to <workspace>/.ggcode/refusals.json (atomic write, rolling
//     window, same file discipline as intervention_ledger.go).
//   - checkBlocked(): before WRITE-class tool execution, each entry is
//     matched against the tool arguments by deterministic substring over
//     STRUCTURED TARGETS ONLY - path-like tokens (≥4 chars), flags (≥3
//     chars incl. dash). An entry with no structured target never blocks
//     (pure-semantic refusals stay advisory; a hard block on a guess would
//     cost more than it buys). Read-class tools never block.
//   - Block is a hard tool error (not guidance injection): the model hits
//     it once and routes around it for the rest of the run - machine
//     enforcement, not persuasion.
//   - release(): the user can lift a refusal conversationally ("ok, you
//     can modify X now") - a release phrase overlapping an entry's targets
//     removes it. ClearRefusals backs /refusals clear.
//   - #4284 source discrimination: extraction runs only on DIRECT user
//     prose - fenced code, quoted reply lines, log-shaped lines, and line
//     comments are stripped first (stripNonDirectiveText), so a pasted
//     code comment or log line never becomes a 30-day write block. Legacy
//     entries are re-annotated at load; fragment-kind entries are
//     advisory-only (never block).
//   - English-only extraction in V1, matching the existing constraint
//     extraction layer it extends.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	refusalMaxEntries = 50
	refusalLedgerFile = "refusals.json"
	refusalExcerptLen = 160
	refusalRecency    = 30 * 24 * time.Hour
)

// refusalEntry is one persisted user refusal.
type refusalEntry struct {
	Excerpt string `json:"excerpt"`
	Source  string `json:"source"` // "nl" | "approval-deny"
	// Kind distinguishes text NATURE (#4284): a genuine user directive
	// ("user-direct", enforced) from quoted/log/prompt debris that merely
	// LOOKS like one ("fragment", advisory-only - displayed in /refusals,
	// never blocks). Empty (legacy files) is re-annotated at load.
	Kind string `json:"kind,omitempty"`
	Ts   int64  `json:"ts"`
}

const (
	refusalKindDirect   = "user-direct" // real user instruction - enters anchor extraction & enforcement
	refusalKindFragment = "fragment"    // quoted text / tool output / prompt debris - advisory only
)

type refusalLedgerData struct {
	Entries []refusalEntry `json:"entries"`
}

// refusalLedger persists user refusals workspace-wide.
type refusalLedger struct {
	mu         sync.Mutex
	workingDir string
	data       refusalLedgerData
}

func newRefusalLedger(workingDir string) *refusalLedger {
	l := &refusalLedger{workingDir: workingDir}
	l.load()
	return l
}

func (l *refusalLedger) path() string {
	return filepath.Join(l.workingDir, ".ggcode", refusalLedgerFile)
}

func (l *refusalLedger) load() {
	raw, err := os.ReadFile(l.path())
	if err != nil {
		return
	}
	var rawLedger refusalLedgerData
	if json.Unmarshal(raw, &rawLedger) == nil {
		// #4284: legacy entries (no Kind) were recorded before source
		// discrimination existed - production refusals.json was nearly all
		// mis-entered code comments / quoted prompt fragments. Re-annotate by
		// excerpt shape: comment/quote/log-shaped excerpts are downgraded to
		// advisory-only. Deterministic, so an unsaved re-annotation simply
		// re-applies on next load.
		for i := range rawLedger.Entries {
			if rawLedger.Entries[i].Kind == "" {
				if refusalExcerptLooksNonDirective(rawLedger.Entries[i].Excerpt) {
					rawLedger.Entries[i].Kind = refusalKindFragment
				} else {
					rawLedger.Entries[i].Kind = refusalKindDirect
				}
			}
		}
		l.data = rawLedger
	}
}

func (l *refusalLedger) saveLocked() {
	raw, err := json.MarshalIndent(l.data, "", " ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.path()), 0o755)
	tmp := l.path() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, l.path())
	}
}

// refusalNegatePattern matches negation-form refusals only (the advisory
// layer also extracts "only use X"-style constraints; those are directives,
// not refusals, and do not belong in an enforcement ledger).
var refusalNegatePattern = regexp.MustCompile(
	`(?i)(?:don'?t|do not|never|no new|avoid|must not|should not|stop using|leave\b[^n]{0,5}alone|don'?t touch)\b[^\n.]{4,120}`)

// releasePattern matches conversational lift phrases.
// #3712: CJK lift words must live OUTSIDE the \b group - Go regexp \b is
// an ASCII word boundary and never matches beside CJK runes (same trap the
// reaffirm pattern's own comment documents), so 可以了|解除|允许 inside
// the group never fired and pure-Chinese releases failed 100%.
var refusalReleasePattern = regexp.MustCompile(
	`(?i)\b(?:ok|fine|alright|go ahead|you (?:may|can) now|you(?:'re| are) (?:allowed|free) to|it'?s ok|lift)\b[^\n.]{0,80}|解除|允许|可以了`)

// #3469 reaffirmation guard: lift text that ALSO continues the refusal is
// a reaffirmation, not a release. "Ok, don't touch config.yaml, that's
// exactly right" must never silently delete the 30-day block.
// English terms are \b-bounded; CJK terms are NOT (Go regexp \b is an
// ASCII word boundary and never matches beside CJK runes, so 别/保持 etc.
// must live outside the \b group or they silently fail to veto).
var refusalReaffirmPattern = regexp.MustCompile(
	`(?i)\b(?:don'?t|do not|never|no\s+new|avoid|must not|should not|stop using|still\s+(?:do(?:n'?t)?|not)?\b|stays?\b|keep\s+(?:avoiding|out|away|it\s+that\s+way)|leave\s+[^\n]{0,10}alone|untouched|off[- ]limits|forbidden|not\s+allowed|prohibited)\b|不许|别碰|不要|禁止|继续(?:别|不|不要|禁止|远离?|保持)|保持(?:现状|不动|原样|别动)`)

// #3469 weak/strong lift split: bare "ok/fine/alright" prefixes are too
// weak to release on their own (reaffirmations routinely start with
// them); they require a co-occurring positive authorization. Strong
// imperative/expermission phrases stand alone.
var refusalWeakLiftPattern = regexp.MustCompile(`(?i)\b(?:ok|fine|alright|it'?s ok)\b`)
var refusalStrongLiftPattern = regexp.MustCompile(
	`(?i)\b(?:go ahead|you (?:may|can) now|you(?:'re| are) (?:allowed|free) to|lift(?:ed)?)\b|解除|允许|可以了`)

// #3712: CJK words outside the \b group (see refusalReleasePattern note).
var refusalAuthorizePattern = regexp.MustCompile(
	`(?i)\b(?:can|may|now|go ahead|allowed|free to|resume|again|touch|modify|edit|write|use)\b|可以|动了?|改了?|碰`)

// #3469 (note): provably read-only command shapes are not write-class
// actions; blocking "grep foo build/" on a "build/" refusal target would
// defeat harmless verification without protecting anything.
var refusalReadOnlyCmdPattern = regexp.MustCompile(
	`^\s*(?:git\s+(?:status|log|diff|show|blame)|grep|rg|cat|head|tail|ls|find|wc|file|stat|which|pwd|echo)\b`)

// refusalCmdWriteEvidence (#3484/#4338): shapes that make an otherwise
// read-only-prefixed command a writer. Any occurrence disqualifies the
// read-only exemption:
//   - `>` / `>>` / `1>` / `2>` / `&>` output redirection (covers `echo x > f`,
//     `cat a > b`, `head -n5 f > f` self-truncation, `git log > f`)
//   - find write-flags: -delete, -exec/-execdir, -fprint*/-fls
//   - git --output=<file> (log/diff/show write their output to a file)
//   - #4338: mid-command write vectors past the read-only prefix -
//     pipe-to-writer consumers (tee/sponge/truncate/cpio/dd of=), xargs
//     feeding a destructive writer, and compound separators (`;`, `&&`,
//     `||`) chaining into one. Without these the prefix exemption
//     fail-opened: `cat a | tee config.yaml`, `pwd && rm config.yaml`.
//
// `>>` and fd-prefixed forms are subsumed by the bare `>` alternative.
// Heredoc `<<` contains no `>` and is input-side only - not matched.
var refusalCmdWriteEvidence = regexp.MustCompile(
	`(?:\d)?>{1,2}|&>|-delete\b|-exec(dir)?\b|-fprint\w*\b|-fls\b|--output(=|\s)` +
		`|\b(?:tee|sponge|truncate|cpio)\b` +
		`|\bdd\s+[^;|]*\bof=` +
		`|\bxargs\s+(?:rm|rmdir|mv|cp|chmod|chown|truncate|shred|dd|ln|mkdir|touch|tee)\b` +
		`|(?:;|&&|\|\|)\s*(?:rm|rmdir|mv|cp|chmod|chown|truncate|shred|dd|tee|sponge|ln|mkdir|touch)\b`)

// refusalPathPattern: path-like tokens (contain / or .ext), ≥4 chars.
// #anchor-boundary: the extension branch is \b-anchored so an in-word
// fragment ("xconfig.yaml" inside an identifier) does not yield
// "config.yaml" as a match anchor.
var refusalPathPattern = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_.-]+)+|\b[A-Za-z0-9_-]+\.[A-Za-z0-9]{1,8}\b`)

// refusalFlagToken is the character class a flag anchor may span after its
// leading dash(es).
var refusalFlagBody = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*`)

// refusalFlags extracts flag-shaped anchors (-abc / --abc, ≥3 chars of
// body) from excerpt TOKENS. #anchor-boundary: the old free-floating
// `--?[A-Za-z][A-Za-z0-9-]{2,}` regex had no left boundary, so hyphenated
// prose words ("mid-run", "re-search") leaked "-run"/"-search" as anchors
// and then blocked any go test invocation using those literal flags - the
// exact false-positive chain observed in production. A flag anchor only
// counts when the dash is at token START.
func refusalFlags(excerpt string) []string {
	var out []string
	for _, tok := range strings.Fields(excerpt) {
		tok = strings.Trim(tok, "\"'`(),;:[]{}<>.")
		if len(tok) < 4 || tok[0] != '-' {
			continue
		}
		body := strings.TrimPrefix(tok[1:], "-")
		m := refusalFlagBody.FindString(body)
		if len(m) < 3 {
			continue
		}
		dashes := len(tok) - len(body) // 1 or 2 leading dashes
		out = append(out, strings.ToLower(tok[:dashes]+m))
	}
	return out
}

// Non-directive shapes (#4284). The ledger turns extracted phrases into
// 30-day cross-run write blocks, so material the user merely QUOTED or
// PASTED must never enter with the same weight as their own words:
//   - fenced code blocks (``` / ~~~ spans): code comments, embedded prompts
//   - block-quote / reply-quote lines ("> ...") and table rows ("| ...")
//   - log-like lines: ISO-timestamp prefixes, [LEVEL]/LEVEL/level= prefixes
//   - line-comment lines ("// ...", "# ...", "-- ...")
//
// Deliberately conservative: only shapes that are near-certainly not the
// user's own prose are stripped. Ambiguous forms (bullets, inline quotes)
// are kept - a false strip would silently drop a REAL refusal.
var refusalFencePattern = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
var refusalLogLinePattern = regexp.MustCompile(`(?i)^\s*(?:\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}|\[?(?:trace|debug|info|notice|warn(?:ing)?|error|fatal|crit)\]?[\s:\]]|level=[a-z]+)\b`)
var refusalQuoteLinePattern = regexp.MustCompile(`^\s*[>|].*`)

// excerptNonDirectiveShape matches a single line/excerpt with a
// near-certain non-directive SHAPE (code comment, quoted fragment, log
// fragment). Used both for pre-extraction line filtering and for
// re-annotating legacy entries whose kind is unknown. A leading "#" also
// covers markdown headers / yaml comments inside pasted prompts.
var excerptNonDirectiveShape = regexp.MustCompile(`^\s*(?://+|#|--|>|\|).*$|^(?:trace|debug|info|notice|warn(?:ing)?|error|fatal|crit)[\s:\]]`)

func refusalExcerptLooksNonDirective(excerpt string) bool {
	return excerptNonDirectiveShape.MatchString(strings.TrimSpace(excerpt))
}

// stripNonDirectiveText returns text with non-directive segments blanked
// (line structure preserved; extraction regexes run on the result).
func stripNonDirectiveText(text string) string {
	text = refusalFencePattern.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if refusalLogLinePattern.MatchString(ln) ||
			refusalQuoteLinePattern.MatchString(ln) ||
			refusalExcerptLooksNonDirective(ln) {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// extractRefusals returns negation-form refusal excerpts from user text.
// #4284: only DIRECT user instructions are extracted - quoted text, pasted
// logs, and code comments are stripped first (see stripNonDirectiveText).
func extractRefusals(text string) []string {
	if len(text) == 0 {
		return nil
	}
	text = stripNonDirectiveText(text)
	matches := refusalNegatePattern.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		e := strings.TrimSpace(m)
		if len(e) > refusalExcerptLen {
			r := []rune(e)
			cut := refusalExcerptLen
			if cut > len(r) {
				cut = len(r)
			}
			e = string(r[:cut]) + "..."
		}
		key := strings.ToLower(e)
		if len(key) > 40 {
			key = key[:40]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

// record persists refusals (idempotent by excerpt prefix). Never blocks.
func (l *refusalLedger) record(excerpt, source, kind string) {
	if l == nil || excerpt == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := refusalKey(excerpt)
	for _, e := range l.data.Entries {
		if refusalKey(e.Excerpt) == key {
			return // already persisted
		}
	}
	l.data.Entries = append(l.data.Entries, refusalEntry{
		Excerpt: excerpt, Source: source, Kind: kind, Ts: time.Now().Unix(),
	})
	if excess := len(l.data.Entries) - refusalMaxEntries; excess > 0 {
		l.data.Entries = l.data.Entries[excess:]
	}
	l.saveLocked()
}

func refusalKey(excerpt string) string {
	k := strings.ToLower(strings.TrimSpace(excerpt))
	if len(k) > 40 {
		k = k[:40]
	}
	return k
}

// release removes entries whose structured targets overlap the lift text.
// Returns the number of entries removed.
func (l *refusalLedger) release(text string) int {
	if l == nil || text == "" {
		return 0
	}
	lower := strings.ToLower(text)
	// #3469: a lift phrase accompanied by continuing-refusal wording is a
	// reaffirmation ("Ok, don't touch config.yaml, that's exactly right").
	// Reaffirmations never release; deletion is persistent and silent, so
	// ambiguity must fail closed.
	if refusalReaffirmPattern.MatchString(lower) {
		return 0
	}
	// #3469: weak conversational prefixes (ok/fine/alright) alone are not
	// release evidence; require a positive authorization co-occurrence.
	strong := refusalStrongLiftPattern.MatchString(lower)
	if !strong {
		if !refusalWeakLiftPattern.MatchString(lower) || !refusalAuthorizePattern.MatchString(lower) {
			return 0
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.data.Entries[:0]
	removed := 0
	for _, e := range l.data.Entries {
		hit := false
		if strong || refusalReleasePattern.MatchString(lower) {
			for _, t := range refusalTargets(e.Excerpt) {
				if strings.Contains(lower, t) {
					hit = true
					break
				}
			}
		}
		if hit {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed > 0 {
		l.data.Entries = kept
		l.saveLocked()
	}
	return removed
}

// refusalWriteTools are the tool classes a refusal may block. Read-only
// tools never block: a "don't touch" refusal targets mutation, and
// blocking reads would break harmless verification.
var refusalWriteTools = map[string]bool{
	"edit_file": true, "multi_edit_file": true, "write_file": true,
	"multi_file_edit": true, "multi_file_write": true, "file_ops": true,
	"batch_replace": true, "notebook_edit": true, "run_command": true,
	"start_command": true, "git_add": true, "git_commit": true,
	"git_checkout": true, "git_reset": true, "git_revert": true,
	"git_tag": true, "git_stash": true, "apply_patch": true,
}

// refusalTargets extracts the deterministic match anchors from an excerpt:
// path-like tokens and flags. Lowercased for substring matching.
func refusalTargets(excerpt string) []string {
	var out []string
	for _, p := range refusalPathPattern.FindAllString(excerpt, -1) {
		if len(p) >= 4 {
			out = append(out, strings.ToLower(p))
		}
	}
	out = append(out, refusalFlags(excerpt)...)
	return out
}

// checkBlocked returns a hard-block message when a persisted refusal
// deterministically matches this write-class tool call. Empty = allow.
func (l *refusalLedger) checkBlocked(tool string, args string) string {
	if l == nil || !refusalWriteTools[tool] {
		return ""
	}
	lower := strings.ToLower(refusalMatchDomain(tool, args))
	// #3469 (note): read-only run_command shapes never mutate the refusal
	// target, so they are exempt from the write-class block.
	// #3484: the prefix match alone is NOT proof of read-only-ness - shell
	// redirection and find write-flags turn these prefixes into writers
	// (`echo x > f`, `cat a > b`, `find . -name f -delete`, `git log --output=f`).
	// Any write evidence in the command disqualifies the exemption and the
	// call falls through to normal entry matching (fail-closed).
	if tool == "run_command" {
		var rc struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &rc) == nil &&
			refusalReadOnlyCmdPattern.MatchString(rc.Command) &&
			!refusalCmdWriteEvidence.MatchString(rc.Command) {
			return ""
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().Unix()
	for _, e := range l.data.Entries {
		if e.Kind == refusalKindFragment {
			continue // #4284: quoted/log/prompt debris is advisory-only
		}
		if now-e.Ts > int64(refusalRecency.Seconds()) {
			continue // expired; pruned lazily
		}
		for _, t := range refusalTargets(e.Excerpt) {
			if strings.Contains(lower, t) {
				age := refusalAgeWords(now - e.Ts)
				return fmt.Sprintf(
					"blocked by persisted user refusal (%s): \"%s\". "+
						"The user refused this action and that refusal is enforced across runs. "+
						"Do not attempt it again unless the user explicitly lifts the refusal; "+
						"ask them to confirm (they can also run /refusals clear).",
					age, e.Excerpt)
			}
		}
	}
	return ""
}

// refusalMatchDomain narrows #3717: the refusal check used to match against
// the ENTIRE tool args JSON, so an edit_file whose new_text merely MENTIONED
// a refused filename ("config.yaml supports many options") was hard-blocked
// even though the write targeted a completely different file. The match
// domain is now:
//   - run_command / start_command: the command string itself (the command
//     IS the write vector; whole-domain matching is correct there);
//   - every other write tool: the path-bearing FIELD VALUES only
//     (file_path/path/notebook_path/notebook/file/source/destination,
//     recursively - covers files[].path and operations[].source/dest);
//   - non-JSON args (or a non-object root): fall back to the raw string so
//     a malformed payload fails CLOSED, never open.
func refusalMatchDomain(tool string, args string) string {
	if tool == "run_command" || tool == "start_command" {
		var rc struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &rc) == nil && rc.Command != "" {
			return rc.Command
		}
		return args
	}
	var root any
	if err := json.Unmarshal([]byte(args), &root); err != nil {
		return args // fail closed: unparseable payload keeps old semantics
	}
	var b strings.Builder
	collectPathValues(root, &b)
	if b.Len() == 0 {
		// No path-bearing field at all: keep the raw args rather than an
		// empty domain - an unrecognized write shape must not silently pass.
		return args
	}
	return b.String()
}

// refusalPathKeys are the argument keys that carry a write TARGET. Content
// keys (content/old_text/new_text/description...) are deliberately absent:
// text that merely mentions a refused path is not a write to it.
var refusalPathKeys = map[string]bool{
	"file_path": true, "path": true, "file": true,
	"notebook_path": true, "notebook": true,
	"source": true, "destination": true, "to_path": true,
}

func collectPathValues(node any, b *strings.Builder) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if refusalPathKeys[k] {
				if s, ok := child.(string); ok {
					b.WriteString(s)
					b.WriteByte('\n')
				}
			}
			collectPathValues(child, b)
		}
	case []any:
		for _, child := range v {
			collectPathValues(child, b)
		}
	}
}

func refusalAgeWords(secs int64) string {
	h := secs / 3600
	switch {
	case h < 1:
		return "just now"
	case h < 24:
		return fmt.Sprintf("%d hour(s) ago", h)
	default:
		return fmt.Sprintf("%d day(s) ago", h/24)
	}
}

// clear wipes all persisted refusals (backs /refusals clear).
func (l *refusalLedger) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = refusalLedgerData{}
	l.saveLocked()
}

// recordUserRefusals extracts and persists NL refusals from user text.
// Called on every user message, right after the advisory constraint layer.
func (a *Agent) recordUserRefusals(userText string) {
	// #4284: extraction itself is source-filtered (quoted text / pasted
	// logs / code comments never produce entries); whatever survives IS a
	// direct user instruction.
	for _, ex := range extractRefusals(userText) {
		a.refusalLedger.record(ex, "nl", refusalKindDirect)
	}
}

// remoteRefusalInhibit (#3460): consume-once gate marking the NEXT run as
// remote-origin (LAN Chat @agent DM / IM inbound), whose user text must NOT
// mutate the persistent refusal ledger. The ledger turns conversational
// refusals into 30-day cross-run write blocks; a LAN peer can forge
// FromRole=agent (self-reported, unauthenticated) and inject "never touch
// X" into an auto-approved DM, permanently poisoning the workspace without
// the local user ever seeing the message. Remote runs skip BOTH record and
// release - a remote "you may X now" lifting the user's own blocks is the
// same integrity violation in reverse.
var remoteRefusalInhibit atomic.Bool

// InhibitNextRefusalLedgerWrite marks the next agent run as remote-origin:
// its user text is exempt from refusal-ledger record/release. Set by the
// TUI at the remote injection boundaries (lanchat DM / IM inbound) right
// before triggering the run; consumed by RunStreamWithContent at the
// ledger gate.
func (a *Agent) InhibitNextRefusalLedgerWrite() { remoteRefusalInhibit.Store(true) }

// consumeRemoteRefusalInhibit returns and clears the flag. Call once per
// candidate ledger mutation site; the first consumer wins and later sites
// in the same run see false (record and release sit in the same gate).
func consumeRemoteRefusalInhibit() bool { return remoteRefusalInhibit.Swap(false) }

// ReleaseMatchingRefusals lifts refusals matching the user's lift text.
func (a *Agent) ReleaseMatchingRefusals(text string) int {
	return a.refusalLedger.release(text)
}

// ClearRefusals wipes the refusal ledger (/refusals clear).
func (a *Agent) ClearRefusals() { a.refusalLedger.clear() }

// RefusalSummary renders persisted refusals for the user.
func (a *Agent) RefusalSummary() string {
	a.refusalLedger.mu.Lock()
	defer a.refusalLedger.mu.Unlock()
	if len(a.refusalLedger.data.Entries) == 0 {
		return "No persisted user refusals. Saying \"don't/never/avoid X\" records an enforceable refusal that blocks matching write actions across runs (arXiv 2605.00055)."
	}
	var sb strings.Builder
	now := time.Now().Unix()
	sb.WriteString("Persisted user refusals (enforced on write-class tool calls):\n")
	for i, e := range a.refusalLedger.data.Entries {
		if now-e.Ts > int64(refusalRecency.Seconds()) {
			continue
		}
		mark := ""
		if e.Kind == refusalKindFragment {
			mark = ", advisory-only"
		}
		fmt.Fprintf(&sb, "  %d. [%s%s, %s] \"%s\"\n", i+1, e.Source, mark, refusalAgeWords(now-e.Ts), e.Excerpt)
	}
	sb.WriteString("Lift one by saying e.g. \"ok, you can modify X now\"; wipe all with /refusals clear.")
	return sb.String()
}
