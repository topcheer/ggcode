package im

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) NotifyPreviousBindingReplaced(ctx context.Context, result PairingResult) error {
	binding, text, ok := previousBindingReplacementNotice(result)
	if !ok {
		return nil
	}
	return m.SendDirect(ctx, binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: text,
	})
}

func previousBindingReplacementNotice(result PairingResult) (ChannelBinding, string, bool) {
	if !result.Bound || result.PreviousBinding == nil {
		return ChannelBinding{}, "", false
	}
	previous := *result.PreviousBinding
	if strings.TrimSpace(previous.ChannelID) == "" {
		return ChannelBinding{}, "", false
	}
	if result.NewBinding != nil &&
		previous.Adapter == result.NewBinding.Adapter &&
		previous.ChannelID == result.NewBinding.ChannelID &&
		previous.ThreadID == result.NewBinding.ThreadID {
		return ChannelBinding{}, "", false
	}
	platformName := pairingPlatformDisplayName(PlatformUnknown)
	if result.NewBinding != nil {
		platformName = pairingPlatformDisplayName(result.NewBinding.Platform)
	}
	if platformName == "" {
		return previous, "This workspace was switched to another channel. Start pairing again to rebind.\n当前目录已切换到其他渠道，如需重新绑定请再次发起配对。", true
	}
	return previous, fmt.Sprintf("This workspace was switched to another %s channel. Start pairing again to rebind.\n当前目录已切换到其他 %s 渠道，如需重新绑定请再次发起配对。", platformName, platformName), true
}

func pairingPlatformDisplayName(platform Platform) string {
	switch platform {
	case PlatformQQ:
		return "QQ"
	case PlatformTelegram:
		return "Telegram"
	case PlatformDiscord:
		return "Discord"
	case PlatformFeishu:
		return "Feishu"
	case PlatformDingTalk:
		return "DingTalk"
	case PlatformSlack:
		return "Slack"
	case PlatformWechat:
		return "WeChat"
	case PlatformWeCom:
		return "WeCom"
	case PlatformMattermost:
		return "Mattermost"
	case PlatformMatrix:
		return "Matrix"
	case PlatformSignal:
		return "Signal"
	case PlatformIRC:
		return "IRC"
	case PlatformNostr:
		return "Nostr"
	case PlatformTwitch:
		return "Twitch"
	case PlatformWhatsApp:
		return "WhatsApp"
	default:
		return ""
	}
}
