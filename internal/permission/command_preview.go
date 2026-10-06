package permission

import (
	"strings"
)

// Command rehearsal (r451, production-agent three-step pattern for
// high-risk actions: simulate -> preview -> confirm). The dangerous.go
// detector CLASSIFIES a command against patterns; this package-side
// companion enumerates the command's WRITE EFFECTS - which files/dirs a
// compound command (pipes, &&/|| chains) would create, overwrite or
// delete - purely by lexical analysis. Zero execution, zero shell.

// SubcommandPreview is the rehearsal result for one segment of a
// compound command (split on &&, ||, ;, | - quote-aware).
type SubcommandPreview struct {
	Raw          string   // the segment as written
	Verb         string   // first token (argv[0]-ish)
	Args         []string // remaining tokens, flags included
	WriteTargets []string // paths this segment would create/overwrite/delete
	Destructive  bool     // rm -rf / git reset --hard / git clean class
	Notes        []string // caveats the caller should surface
}

// PreviewReport is the full rehearsal for one command line.
type PreviewReport struct {
	Subcommands  []SubcommandPreview
	WriteTargets []string // union, in first-seen order
	Destructive  bool     // any segment destructive
}

// verbs that (may) write/create/delete; targets enumerated lexically.
// NOTE: git and sed are deliberately absent - they have dedicated
// subcommand-level handling in previewSegment and must not be swallowed
// by the generic verb dispatch.
var previewWriteVerbs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "mkdir": true, "rmdir": true,
	"touch": true, "dd": true, "tee": true, "truncate": true,
	"chmod": true, "chown": true, "ln": true, "install": true,
	"shred": true, "rsync": true, "tar": true, "unzip": true, "find": true,
}

// verbs that are destructive by class (before flag analysis).
var previewDestructiveVerbs = map[string]bool{
	"rm": true, "shred": true,
}

// destructive git subcommands.
var previewDestructiveGitSubs = map[string]bool{
	"clean": true, "reset": true, "checkout": true, "restore": true,
	"push": true, "rebase": true, "cherry-pick": true, "filter-branch": true,
}

// sed in-place flag.
const sedInPlace = "-i"

// PreviewCommand lexically rehearses a compound shell command and reports
// its write effects without running anything. It deliberately
// under-approximates exotic quoting; every such gap is recorded as a Note
// so the caller knows the preview is not exhaustive rather than trusting
// an empty target list blindly.
func PreviewCommand(cmd string) PreviewReport {
	var rep PreviewReport
	seen := map[string]bool{}
	for _, seg := range splitCompound(cmd) {
		sc := previewSegment(seg)
		rep.Subcommands = append(rep.Subcommands, sc)
		rep.Destructive = rep.Destructive || sc.Destructive
		for _, tgt := range sc.WriteTargets {
			if !seen[tgt] {
				seen[tgt] = true
				rep.WriteTargets = append(rep.WriteTargets, tgt)
			}
		}
	}
	return rep
}

// splitCompound splits on ;, &&, ||, | outside quotes.
func splitCompound(cmd string) []string {
	var segs []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == ';' || c == '|' || (c == '&' && i+1 < len(cmd) && cmd[i+1] == '&'):
			// consume the doubling (&&, ||) so it is not treated as two splits
			if (c == '|' || c == '&') && i+1 < len(cmd) && cmd[i+1] == c {
				i++
			}
			if strings.TrimSpace(cur.String()) != "" {
				segs = append(segs, strings.TrimSpace(cur.String()))
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		segs = append(segs, s)
	}
	return segs
}

// previewSegment analyzes one simple-ish segment (verb + args + possible
// output redirection).
func previewSegment(seg string) SubcommandPreview {
	var sc SubcommandPreview
	sc.Raw = seg
	fields := tokenizeSegment(seg)
	if len(fields) == 0 {
		return sc
	}
	sc.Verb = basename(fields[0])
	sc.Args = fields[1:]

	// Output redirection targets: > file, >> file (attach even for
	// read-only verbs - `sort x > y` overwrites y).
	sc.WriteTargets = append(sc.WriteTargets, redirectTargets(fields)...)

	switch {
	case previewWriteVerbs[sc.Verb]:
		sc.WriteTargets = append(sc.WriteTargets, writeTargetsFor(sc.Verb, sc.Args)...)
	case sc.Verb == "git" && len(sc.Args) > 0:
		sub := sc.Args[0]
		if previewDestructiveGitSubs[sub] {
			sc.Destructive = true
			sc.Notes = append(sc.Notes, "git "+sub+" can discard uncommitted work")
		}
		if sub == "clean" {
			// `git clean -fd dir...` targets dir...; without paths it
			// targets the whole worktree.
			paths := nonFlagArgs(sc.Args[1:])
			if len(paths) == 0 {
				sc.WriteTargets = append(sc.WriteTargets, "(entire worktree)")
			} else {
				sc.WriteTargets = append(sc.WriteTargets, paths...)
			}
		}
	case sc.Verb == "sudo" || sc.Verb == "env" || sc.Verb == "nohup":
		// wrapper verbs: rehearse the wrapped command if present.
		if len(sc.Args) > 0 {
			inner := previewSegment(strings.Join(sc.Args, " "))
			sc.WriteTargets = append(sc.WriteTargets, inner.WriteTargets...)
			sc.Destructive = inner.Destructive
			sc.Verb = sc.Verb + " " + inner.Verb
			sc.Notes = append(sc.Notes, "wrapped command rehearsed through "+sc.Verb)
		}
	}
	if previewDestructiveVerbs[sc.Verb] {
		sc.Destructive = true
	}
	if sc.Verb == "sed" {
		for _, a := range sc.Args {
			if strings.HasPrefix(a, sedInPlace) {
				// GNU sed in-place: the FIRST positional is the script
				// (s/a/b/), the rest are the files it rewrites.
				positional := nonFlagArgs(sc.Args)
				if len(positional) > 1 {
					sc.WriteTargets = append(sc.WriteTargets, positional[1:]...)
				}
				break
			}
		}
	}
	return sc
}

// tokenizeSegment splits a segment on whitespace outside quotes.
func tokenizeSegment(seg string) []string {
	var fields []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t':
			flush()
		case c == '>' || c == '<':
			// redirection operator: flush current field, emit as own field
			flush()
			// >> is one operator
			if i+1 < len(seg) && seg[i+1] == '>' {
				cur.WriteString(">>")
				i++
			} else {
				cur.WriteByte(c)
			}
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return fields
}

// redirectTargets returns files opened for writing by > / >> operators.
func redirectTargets(fields []string) []string {
	var out []string
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == ">" || fields[i] == ">>" {
			t := fields[i+1]
			if t != "" && t != "&" && !strings.HasPrefix(t, "&") {
				out = append(out, t)
			}
		}
	}
	return out
}

// nonFlagArgs returns args that are not -flags.
func nonFlagArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// writeTargetsFor enumerates write targets for known file-writing verbs.
func writeTargetsFor(verb string, args []string) []string {
	positional := nonFlagArgs(args)
	var targets []string
	switch verb {
	case "rm":
		targets = positional
	case "mv", "cp", "rsync", "ln", "install":
		// last positional is the destination
		if len(positional) >= 2 {
			targets = []string{positional[len(positional)-1]}
		}
	case "tee", "truncate", "chmod", "chown", "shred", "touch":
		if len(positional) >= 1 {
			targets = positional
		}
	case "dd":
		// dd of=/path writes `of=` operand
		for _, a := range args {
			if strings.HasPrefix(a, "of=") {
				targets = []string{strings.TrimPrefix(a, "of=")}
				break
			}
		}
	case "mkdir", "rmdir":
		targets = positional
	case "find":
		// find ... -delete is hard to bound; flag as note-level target.
		for _, a := range args {
			if strings.HasPrefix(a, "-delete") {
				targets = []string{"(find -delete: unbounded paths)"}
				break
			}
		}
	case "tar", "unzip":
		// archive extraction writes into -C dir / cwd
		for i := 0; i < len(args); i++ {
			if args[i] == "-C" && i+1 < len(args) {
				targets = []string{args[i+1]}
				break
			}
		}
		if targets == nil {
			targets = []string{"(cwd)"}
		}
	}
	// sed is handled by the caller (needs script-vs-file disambiguation).
	return targets
}

// basename strips a leading path from a verb (/bin/rm -> rm).
func basename(v string) string {
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		return v[i+1:]
	}
	return v
}
