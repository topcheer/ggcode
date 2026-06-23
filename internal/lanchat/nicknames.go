package lanchat

import (
	"fmt"
	"math/rand"
	"strings"
)

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
	"Otter", "Falcon", "Tiger", "Wolf", "Bear", "Eagle", "Fox",
	"Hawk", "Lion", "Panda", "Raven", "Shark", "Whale", "Owl",
	"Lynx", "Seal", "Hare", "Cat", "Crow", "Dolphin", "Crane",
	"Moose", "Bison", "Cobra", "Drake", "Finch", "Gecko", "Heron",
	"Ibis", "Jaguar", "Koala", "Leopard", "Magpie", "Newt", "Orca",
	"Panther", "Quail", "Robin", "Stoat", "Trout", "Viper", "Wombat",
	"Yak", "Zebra", "Badger", "Coyote", "Ferret", "Grouse", "Husky",
	"Indri", "Jackal",
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
