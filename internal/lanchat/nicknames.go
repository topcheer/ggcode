package lanchat

import (
	"fmt"
	"math/rand"
	"strings"
)

// DefaultRole is assigned when no role is specified via /nick.
const DefaultRole = "developer"

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
// The split is on the last '@' so nicks cannot contain '@'.
func ParseNickRole(input string) (nick, role string) {
	input = strings.TrimSpace(input)
	idx := strings.LastIndex(input, "@")
	if idx < 0 {
		return input, DefaultRole
	}
	nick = strings.TrimSpace(input[:idx])
	role = strings.TrimSpace(input[idx+1:])
	if role == "" {
		role = DefaultRole
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
