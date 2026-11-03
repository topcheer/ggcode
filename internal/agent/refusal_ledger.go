package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	refusalMaxEntries = 50
	refusalLedgerFile = "refusals.json"
	refusalExcerptLen = 160
	refusalRecency    = 30 * 24 * time.Hour
)

type refusalEntry struct {
	Excerpt string `json:"excerpt"`
	Source  string `json:"source"`
	Kind    string `json:"kind,omitempty"`
	Ts      int64  `json:"ts"`
}

const (
	refusalKindDirect   = "user-direct"
	refusalKindFragment = "fragment"
)

type refusalLedgerData struct {
	Entries []refusalEntry `json:"entries"`
}

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

var refusalNegatePattern = regexp.MustCompile(
	`(?i)(?:don'?t|do not|never|no new|avoid|must not|should not|stop using|leave\b[^\n]{0,40}alone|don'?t touch)\b[^\n.]{4,120}(?:\.[A-Za-z0-9]{1,8}\b)?`)

var refusalReleasePattern = regexp.MustCompile(
	`(?i)\b(?:ok|fine|alright|go ahead|you (?:may|can) now|you(?:'re| are) (?:allowed|free) to|it'?s ok|lift)\b[^\n.]{0,80}|解除|允许|可以了`)

var refusalReaffirmPattern = regexp.MustCompile(
	`(?i)\b(?:don'?t|do not|never|no\s+new|avoid|must not|should not|stop using|still\s+(?:do(?:n'?t)?|not)\b|stays?\b|keep\s+(?:avoiding|out|away|it\s+that\s+way)|leave\s+[^\n]{0,40}alone|untouched|off[- ]limits|forbidden|not\s+allowed|prohibited)\b|不许|别碰|不要|禁止|继续(?:别|不|不要|禁止|远离?|保持)|保持(?:现状|不动|原样|别动)`)

var refusalWeakLiftPattern = regexp.MustCompile(`(?i)\b(?:ok|fine|alright|it'?s ok)\b`)
var refusalStrongLiftPattern = regexp.MustCompile(
	`(?i)\b(?:go ahead|you (?:may|can) now|you(?:'re| are) (?:allowed|free) to|lift(?:ed)?)\b|解除|允许|可以了`)

var refusalAuthorizePattern = regexp.MustCompile(
	`(?i)\b(?:may|go ahead|allowed|free to|resume|touch|modify|edit|write|use)\b|可以|动了?|改了?|碰`)

var refusalReadOnlyCmdPattern = regexp.MustCompile(
	`^\s*(?:git\s+(?:status|log|diff|show|blame)|grep|rg|cat|head|tail|ls|find|wc|file|stat|which|pwd|echo)\b`)

var refusalCmdWriteEvidence = regexp.MustCompile(
	`(?:\d)?>{1,2}|&>|-delete\b|-exec(dir)?\b|-fprint\w*\b|-fls\b|--output(=|\s)` +
		`|\b(?:tee|sponge|truncate|cpio)\b` +
		`|\bdd\s+[^;|]*\bof=` +
		`|\bxargs\s+(?:\S*/|env\s+|sudo\s+)?(?:rm|rmdir|mv|cp|chmod|chown|truncate|shred|dd|ln|mkdir|touch|tee|sh|bash)\b` +
		`|(?:;|&&|\|\||\||\n)\s*(?:rm|rmdir|mv|cp|chmod|chown|truncate|shred|dd|tee|sponge|ln|mkdir|touch|sh|bash|zsh)\b` +
		`|(?:\||\n|;|&&|\|\|)\s*(?:python\d?|perl|ruby|node|ssh)\b`)

var refusalPathPattern = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_.-]+)+|\b[A-Za-z0-9_-]+\.[A-Za-z0-9]{1,8}\b`)

var refusalFlagBody = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*`)

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
		dashes := len(tok) - len(body)
		out = append(out, strings.ToLower(tok[:dashes]+m))
	}
	return out
}

var refusalFencePattern = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
var refusalLogLinePattern = regexp.MustCompile(`(?i)^\s*(?:\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}|\[?(?:trace|debug|info|notice|warn(?:ing)?|error|fatal|crit)\]?[\s:\]]|level=[a-z]+)\b`)
var refusalQuoteLinePattern = regexp.MustCompile(`^\s*[>|].*`)

var excerptNonDirectiveShape = regexp.MustCompile(`^\s*(?://+|#|--|>|\|).*$|^(?:trace|debug|info|notice|warn(?:ing)?|error|fatal|crit)[\s:\]]`)

func refusalExcerptLooksNonDirective(excerpt string) bool {
	return excerptNonDirectiveShape.MatchString(strings.TrimSpace(excerpt))
}

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

const inlineQuoteTailWindow = 8

func inlineQuotedContext(text string, start, end int) bool {
	if start > 0 {
		if prev := text[start-1]; prev == '"' || prev == '`' {
			return true
		}
	}
	if end < len(text) && text[end] == '.' {
		tail := text[end:]
		if len(tail) > inlineQuoteTailWindow {
			tail = tail[:inlineQuoteTailWindow]
		}
		if strings.ContainsAny(tail, "\"`") {
			return true
		}
	}
	return false
}

func extractRefusals(text string) []string {
	out := extractRefusalCandidates(text)
	excerpts := make([]string, 0, len(out))
	for _, c := range out {
		excerpts = append(excerpts, c.Excerpt)
	}
	return excerpts
}

type refusalCandidate struct {
	Excerpt   string
	Discussed bool
}

func extractRefusalCandidates(text string) []refusalCandidate {
	if len(text) == 0 {
		return nil
	}
	text = stripNonDirectiveText(text)
	locs := refusalNegatePattern.FindAllStringIndex(text, -1)
	out := make([]refusalCandidate, 0, len(locs))
	seen := map[string]bool{}
	for _, loc := range locs {
		e := strings.TrimSpace(text[loc[0]:loc[1]])
		if len(e) > refusalExcerptLen {
			r := []rune(e)
			cut := refusalExcerptLen
			if cut > len(r) {
				cut = len(r)
			}
			e = string(r[:cut]) + "..."
		}
		key := refusalKey(e)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, refusalCandidate{
			Excerpt:   e,
			Discussed: excerptLooksDiscussed(e) || inlineQuotedContext(text, loc[0], loc[1]),
		})
	}
	return out
}

func (l *refusalLedger) record(excerpt, source, kind string) {
	if l == nil || excerpt == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := refusalKey(excerpt)
	for i, e := range l.data.Entries {
		if refusalKey(e.Excerpt) == key {

			if kind == refusalKindDirect && e.Kind != refusalKindDirect {
				l.data.Entries[i].Kind = refusalKindDirect
				l.data.Entries[i].Ts = time.Now().Unix()
				l.saveLocked()
			}
			return
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

func (l *refusalLedger) release(text string) int {
	if l == nil || text == "" {
		return 0
	}
	lower := strings.ToLower(text)

	if refusalReaffirmPattern.MatchString(lower) {
		return 0
	}

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

				if refusalAnchorHit(lower, t) {
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

var refusalWriteTools = map[string]bool{
	"edit_file": true, "multi_edit_file": true, "write_file": true,
	"multi_file_edit": true, "multi_file_write": true, "file_ops": true,
	"batch_replace": true, "notebook_edit": true, "run_command": true,
	"start_command": true, "git_add": true, "git_commit": true,
	"git_checkout": true, "git_reset": true, "git_revert": true,
	"git_tag": true, "git_stash": true, "apply_patch": true,
}

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

func refusalAnchorHit(text, anchor string) bool {
	if anchor == "" {
		return false
	}
	if anchor[0] != '-' {
		return strings.Contains(text, anchor)
	}
	for start := 0; ; {
		i := strings.Index(text[start:], anchor)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(anchor)
		leftOK := i == 0 || !isRefusalFlagBodyByte(text[i-1])
		rightOK := end == len(text) || !isRefusalFlagBodyByte(text[end])
		if leftOK && rightOK {
			return true
		}
		start = i + 1
	}
}

func isRefusalFlagBodyByte(b byte) bool {
	return b == '-' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func (l *refusalLedger) checkBlocked(tool string, args string) string {
	if l == nil || !refusalWriteTools[tool] {
		return ""
	}
	lower := strings.ToLower(refusalMatchDomain(tool, args))

	if tool == "run_command" || tool == "start_command" {
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
			continue
		}
		if now-e.Ts > int64(refusalRecency.Seconds()) {
			continue
		}
		for _, t := range refusalTargets(e.Excerpt) {
			if refusalAnchorHit(lower, t) {
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
		return args
	}
	var b strings.Builder
	collectPathValues(root, &b)
	if b.Len() == 0 {

		return args
	}
	return b.String()
}

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

func (l *refusalLedger) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = refusalLedgerData{}
	l.saveLocked()
}

func excerptLooksDiscussed(ex string) bool {
	if strings.Contains(ex, "`") || strings.Contains(ex, "\"") {
		return true
	}
	for _, r := range ex {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func (a *Agent) recordUserRefusals(userText string) {

	for _, c := range extractRefusalCandidates(userText) {
		kind := refusalKindDirect
		if c.Discussed {
			kind = refusalKindFragment
		}
		a.refusalLedger.record(c.Excerpt, "nl", kind)
	}
}

func (a *Agent) InhibitNextRefusalLedgerWrite() { a.remoteRefusalInhibit.Store(true) }

func (a *Agent) consumeRemoteRefusalInhibit() bool { return a.remoteRefusalInhibit.Swap(false) }

func (a *Agent) ReleaseMatchingRefusals(text string) int {
	if a.refusalLedger == nil {
		return 0
	}
	return a.refusalLedger.release(text)
}

func (a *Agent) ClearRefusals() { a.refusalLedger.clear() }

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
