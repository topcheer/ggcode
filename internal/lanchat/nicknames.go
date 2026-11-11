package lanchat

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const DefaultRole = "developer"

const DefaultTeam = "dev-team"

// HomeTeam is the team name used when the workspace is the user's home
// directory.
const HomeTeam = "home"

// MaxTeamNameLen caps workspace-derived team names.
const MaxTeamNameLen = 30

// DefaultTeamFor derives the initial team name from the workspace path: the
// directory name with spaces replaced by underscores, truncated to
// MaxTeamNameLen runes, or HomeTeam when the workspace is the user's home
// directory. Falls back to DefaultTeam when no usable workspace is known.
func DefaultTeamFor(workspace string) string {
	if workspace == "" {
		return DefaultTeam
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if canonicalWorkspaceComparable(workspace) == canonicalWorkspaceComparable(home) {
			return HomeTeam
		}
	}
	name := filepath.Base(filepath.Clean(workspace))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return DefaultTeam
	}
	return truncateTeamName(strings.ReplaceAll(name, " ", "_"))
}

// HasTeamSegment reports whether a nick spec of the form nick[@role[@team]]
// carries an explicit team part.
func HasTeamSegment(input string) bool {
	return len(strings.Split(strings.TrimSpace(input), "@")) >= 3
}

func truncateTeamName(name string) string {
	if utf8.RuneCountInString(name) <= MaxTeamNameLen {
		return name
	}
	return string([]rune(name)[:MaxTeamNameLen])
}

var adjectives = []string{
	"Clever", "Brave", "Swift", "Bright", "Calm", "Bold", "Eager",
	"Gentle", "Happy", "Jolly", "Keen", "Lively", "Mighty", "Noble",
	"Quick", "Rapid", "Sharp", "Smart", "Steady", "Sunny", "Super",
	"Witty", "Zesty", "Cosmic", "Dapper", "Electric", "Frosty",
	"Glowing", "Hyper", "Iron", "Jazzy", "Lucky", "Magic", "Nimble",
	"Orbit", "Power", "Quantum", "Rapid", "Silent", "Turbo", "Ultra",
	"Vivid", "Wild", "Zen", "Atomic", "Blaze", "Cyber", "Delta",
	"Echo", "Flux",
}

var animals = []string{
	"Otter", "Wolf", "Fox", "Bear", "Lion", "Tiger", "Eagle",
	"Hawk", "Falcon", "Panther", "Lynx", "Owl", "Raven", "Stag",
	"Puma", "Seal", "Heron", "Orca", "Bison", "Moose", "Panda",
	"Cobra", "Drake", "Finch", "Gator", "Hare", "Koala", "Lamb",
	"Magpie", "Newt", "Pika", "Quail", "Robin", "Shark", "Toad",
	"Viper", "Whale", "Yak", "Zebra", "Crow", "Dingo", "Elk",
	"Gull", "Hawk", "Ibis", "Jay", "Kite", "Loon", "Mink",
	"Narwhal", "Plover",
}

func RandomNick() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	animal := animals[rand.Intn(len(animals))]
	return adj + animal
}

func AgentNick(humanNick string) string {

	if strings.HasSuffix(humanNick, "_agent") {
		return humanNick
	}
	return humanNick + "_agent"
}

func ParseNickRole(input string) (nick, role string) {
	nick, role, _ = ParseNickRoleTeam(input)
	return
}

func ParseNickRoleTeam(input string) (nick, role, team string) {
	input = strings.TrimSpace(input)
	parts := strings.Split(input, "@")
	nick = strings.TrimSpace(parts[0])
	role = DefaultRole
	team = DefaultTeam
	if len(parts) >= 2 {
		role = strings.TrimSpace(parts[1])
		if role == "" {
			role = DefaultRole
		}
	}
	if len(parts) >= 3 {
		team = strings.TrimSpace(parts[2])
		if team == "" {
			team = DefaultTeam
		}
	}
	return
}

func ResolveNickConflict(nick string, taken map[string]bool) string {
	if !taken[nick] {
		return nick
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", nick, i)
		if !taken[candidate] {
			return candidate
		}
	}
}
