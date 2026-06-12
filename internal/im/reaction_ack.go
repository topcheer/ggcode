package im

import (
	"hash/fnv"
	"strings"
	"sync"
)

type reactionAckState struct {
	mu         sync.Mutex
	lastByChat map[string]string
}

func (s *reactionAckState) NeedsSend(binding ChannelBinding, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	scope := reactionAckScope(binding)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastByChat == nil {
		s.lastByChat = make(map[string]string)
	}
	return s.lastByChat[scope] != target
}

func (s *reactionAckState) MarkSent(binding ChannelBinding, target string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	scope := reactionAckScope(binding)
	s.mu.Lock()
	if s.lastByChat == nil {
		s.lastByChat = make(map[string]string)
	}
	s.lastByChat[scope] = target
	s.mu.Unlock()
}

func reactionAckScope(binding ChannelBinding) string {
	parts := []string{
		strings.TrimSpace(binding.Workspace),
		strings.TrimSpace(binding.ChannelID),
		strings.TrimSpace(binding.ThreadID),
		strings.TrimSpace(binding.TargetID),
	}
	return strings.Join(parts, "\x00")
}

func reactionAckValue(platform Platform, target string) string {
	if platform == PlatformFeishu {
		return "Typing"
	}
	options := reactionAckOptions(platform)
	if len(options) == 0 {
		return ""
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(string(platform)))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(strings.TrimSpace(target)))
	return options[int(hasher.Sum32())%len(options)]
}

func reactionAckOptions(platform Platform) []string {
	switch platform {
	case PlatformSlack, PlatformMattermost:
		return []string{"eyes", "thinking_face", "hourglass_flowing_sand"}
	case PlatformMatrix, PlatformDiscord:
		return []string{"👀", "🤔", "⏳"}
	default:
		return nil
	}
}
