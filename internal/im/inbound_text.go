package im

import "strings"

func BuildInboundText(msg InboundMessage) string {
	blocks := msg.ProviderContent()
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				lines = append(lines, text)
			}
		case "image":
			lines = append(lines, "[Attached image from remote IM]")
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(strings.Join(lines, "\n\n"))
	}
	return strings.TrimSpace(msg.Text)
}
