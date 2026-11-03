package lanchat

import (
	"fmt"
	"math/rand"
	"strings"
)

const DefaultRole = "developer"

const DefaultTeam = "dev-team"

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
