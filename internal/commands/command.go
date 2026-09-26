package commands

import (
	"strconv"
	"strings"
)

type Source string

const (
	SourceBundled Source = "bundled"
	SourceUser    Source = "user"
	SourceProject Source = "project"
	SourcePlugin  Source = "plugin"
	SourceMCP     Source = "mcp"
)

type LoadedFrom string

const (
	LoadedFromBundled  LoadedFrom = "bundled"
	LoadedFromSkills   LoadedFrom = "skills"
	LoadedFromCommands LoadedFrom = "commands"
	LoadedFromPlugin   LoadedFrom = "plugin"
	LoadedFromMCP      LoadedFrom = "mcp"
)

// Command represents a reusable slash command or skill loaded from markdown.
type Command struct {
	Name                   string
	Template               string
	Description            string
	Source                 Source
	LoadedFrom             LoadedFrom
	Path                   string
	DisplayName            string
	AllowedTools           []string
	ArgumentHint           string
	Arguments              []string
	WhenToUse              string
	RequiresTools          []string // external CLI tools that must be on PATH (e.g. docker, kubectl)
	Dependencies           []string // prerequisite skill names that should be loaded first
	Version                string   // semantic version declared in frontmatter (e.g. "1.0.0")
	UserInvocable          bool
	DisableModelInvocation bool
	Context                string
	Enabled                bool // false = skill is disabled and won't be invoked by the agent
}

// Expand replaces template variables in the command template.
// Supported: $FILE, $DIR, $ARGS, plus any named variables supplied.
// Positional references are left untouched (no positional args provided).
func (c *Command) Expand(vars map[string]string) string {
	return c.ExpandWithArgs(vars, nil)
}

// ExpandWithArgs expands template variables plus positional invocation
// arguments. Positional syntax (only expanded when args is non-empty):
//
//	$1..$9         1-based positional argument
//	${1}..${9}     brace form of the above
//	$ARGUMENTS     all arguments joined by single spaces
//	$ARGUMENTS[0]  0-based positional argument (bracket form)
//
// Positional expansion runs BEFORE named-variable replacement. Besides
// enabling parameterized skills, this fixes an order-dependent corruption:
// $ARGS is a prefix of $ARGUMENTS, so the old single map-pass replacement
// could turn a literal $ARGUMENTS into "<value>UMENTS" depending on Go map
// iteration order.
func (c *Command) ExpandWithArgs(vars map[string]string, args []string) string {
	result := c.Template
	if len(args) > 0 {
		result = expandPositional(result, args)
	}
	for k, v := range vars {
		result = replaceVar(result, "$"+k, v)
	}
	return result
}

// SplitArgs splits a raw invocation argument string into positional
// arguments. Whitespace separates arguments; double quotes group text with
// spaces into a single argument (e.g. `"my env" v2` -> ["my env", "v2"]).
func SplitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	started := false
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			started = true
		case !inQuote && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}

// expandPositional rewrites positional references in a single left-to-right
// pass. Inserted argument values are never re-scanned, so an argument like
// "$2" is inserted verbatim instead of being recursively expanded.
// Missing positions expand to the empty string. "$10" stays literal
// (single-digit positions only, matching shell convention).
func expandPositional(s string, args []string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		rest := s[i+1:]
		switch {
		case strings.HasPrefix(rest, "ARGUMENTS["):
			if end := strings.IndexByte(rest, ']'); end > 0 {
				if n, err := strconv.Atoi(rest[len("ARGUMENTS["):end]); err == nil && n >= 0 {
					b.WriteString(argAt(args, n))
					i += 1 + end + 1
					continue
				}
			}
		case strings.HasPrefix(rest, "ARGUMENTS"):
			b.WriteString(strings.Join(args, " "))
			i += 1 + len("ARGUMENTS")
			continue
		case strings.HasPrefix(rest, "{"):
			if end := strings.IndexByte(rest, '}'); end > 0 {
				if n, err := strconv.Atoi(rest[1:end]); err == nil && n >= 1 {
					b.WriteString(argAt(args, n-1))
					i += 1 + end + 1
					continue
				}
			}
		case rest != "" && rest[0] >= '1' && rest[0] <= '9' && (len(rest) == 1 || rest[1] < '0' || rest[1] > '9'):
			// Bare $N expands only when not followed by another digit, so
			// "$10" (or a shell variable like $100) stays literal. Positions
			// >= 10 use the brace form: ${10}.
			b.WriteString(argAt(args, int(rest[0]-'1')))
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func argAt(args []string, n int) string {
	if n < 0 || n >= len(args) {
		return ""
	}
	return args[n]
}

func (c *Command) SlashName() string {
	if c == nil || strings.TrimSpace(c.Name) == "" {
		return ""
	}
	return "/" + c.Name
}

func (c *Command) UserSlashVisible() bool {
	return c != nil && c.UserInvocable && c.LoadedFrom == LoadedFromCommands && c.SlashName() != ""
}

// IsBuiltin returns true for bundled/internal skills that cannot be disabled.
func (c *Command) IsBuiltin() bool {
	if c == nil {
		return false
	}
	return c.Source == SourceBundled || c.LoadedFrom == LoadedFromBundled
}

func (c *Command) Title() string {
	if c == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(c.DisplayName); trimmed != "" {
		return trimmed
	}
	return c.Name
}

func replaceVar(s, key, value string) string {
	// Simple string replacement
	result := ""
	for i := 0; i < len(s); {
		if i+len(key) <= len(s) && s[i:i+len(key)] == key {
			result += value
			i += len(key)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
