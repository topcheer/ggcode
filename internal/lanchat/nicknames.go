package lanchat

import (
	"fmt"
	"math/rand"
	"strings"
)

// DefaultRole is assigned when no role is specified via /nick.
const DefaultRole = "developer"

// DefaultTeam is assigned when no team is specified via /nick.
const DefaultTeam = "dev-team"

// adjectives for random nickname generation.
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

// animals for random nickname generation.
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

// RandomNick generates a random nickname like "CleverOtter".
func RandomNick() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	animal := animals[rand.Intn(len(animals))]
	return adj + animal
}

// AgentNick returns the agent nickname derived from a human nick.
func AgentNick(humanNick string) string {
	// If already ends with _agent, don't double up.
	if strings.HasSuffix(humanNick, "_agent") {
		return humanNick
	}
	return humanNick + "_agent"
}

// ParseNickRole splits "alice@frontend" into ("alice", "frontend").
// "alice" → ("alice", "developer").
// Deprecated: Use ParseNickRoleTeam for full 3-part parsing.
func ParseNickRole(input string) (nick, role string) {
	nick, role, _ = ParseNickRoleTeam(input)
	return
}

// ParseNickRoleTeam splits "alice@frontend@platform" into ("alice", "frontend", "platform").
// Missing parts get defaults: role="developer", team="dev-team".
//
//	"alice" → ("alice", "developer", "dev-team")
//	"alice@frontend" → ("alice", "frontend", "dev-team")
//	"alice@frontend@platform" → ("alice", "frontend", "platform")
//
// The split is on '@' so nicks, roles, and teams cannot contain '@'.
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

// ResolveNickConflict appends a number if the nick is already taken.
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
