package permission

import (
	"strings"
)

type SubcommandPreview struct {
	Raw          string
	Verb         string
	Args         []string
	WriteTargets []string
	Destructive  bool
	Notes        []string
}

type PreviewReport struct {
	Subcommands  []SubcommandPreview
	WriteTargets []string
	Destructive  bool
}

var previewWriteVerbs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "mkdir": true, "rmdir": true,
	"touch": true, "dd": true, "tee": true, "truncate": true,
	"chmod": true, "chown": true, "ln": true, "install": true,
	"shred": true, "rsync": true, "tar": true, "unzip": true, "find": true,
}

var previewDestructiveVerbs = map[string]bool{
	"rm": true, "shred": true,
}

var previewDestructiveGitSubs = map[string]bool{
	"clean": true, "reset": true, "checkout": true, "restore": true,
	"push": true, "rebase": true, "cherry-pick": true, "filter-branch": true,
}

const sedInPlace = "-i"

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

			flush()

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

func nonFlagArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func writeTargetsFor(verb string, args []string) []string {
	positional := nonFlagArgs(args)
	var targets []string
	switch verb {
	case "rm":
		targets = positional
	case "mv", "cp", "rsync", "ln", "install":

		if len(positional) >= 2 {
			targets = []string{positional[len(positional)-1]}
		}
	case "tee", "truncate", "chmod", "chown", "shred", "touch":
		if len(positional) >= 1 {
			targets = positional
		}
	case "dd":

		for _, a := range args {
			if strings.HasPrefix(a, "of=") {
				targets = []string{strings.TrimPrefix(a, "of=")}
				break
			}
		}
	case "mkdir", "rmdir":
		targets = positional
	case "find":

		for _, a := range args {
			if strings.HasPrefix(a, "-delete") {
				targets = []string{"(find -delete: unbounded paths)"}
				break
			}
		}
	case "tar", "unzip":

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

	return targets
}

func basename(v string) string {
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		return v[i+1:]
	}
	return v
}
