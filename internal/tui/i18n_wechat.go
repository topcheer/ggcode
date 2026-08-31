package tui

func init() {
	registerCatalog(enWechatModule(), zhWechatModule())
}

func enWechatModule() map[string]string {
	return map[string]string{
		"panel.wechat.directory":       "Directory",
		"panel.wechat.bots":            "WeChat Bots",
		"panel.wechat.created":         "Registered: %d",
		"panel.wechat.bound":           "Bound: %d",
		"panel.wechat.available":       "Available: %d",
		"panel.wechat.current_binding": "Current Binding",
		"panel.wechat.none":            "(none)",
		"panel.wechat.default":         "(default)",
		"panel.wechat.target":          "Target: %s",
		"panel.wechat.bot_list":        "WeChat Bot List",
		"panel.wechat.no_bots":         "No WeChat bots configured. Press 'a' to scan QR code.",
		"panel.wechat.scan_qr":         "📱 Scan QR Code with WeChat",
		"panel.wechat.waiting_scan":    "Waiting for scan confirmation...",
		"panel.wechat.scanned":         "Scanned! Waiting for confirmation...",
		"panel.wechat.auth_confirmed":  "✓ Authorization confirmed! Bot connected.",
		"panel.wechat.auth_success":    "WeChat Bot authorized successfully!",
		"panel.wechat.removed":         "Removed binding for %s",
		"panel.wechat.message.no_bot":  "No WeChat bot available. Create or enable one first.",
		"panel.wechat.help":            "[a] Scan QR  [b] Bind  [e] Edit  [d] Disable/Enable  [r] Remove  [↑↓] Navigate  [esc] Close",
	}
}

func zhWechatModule() map[string]string {
	return map[string]string{
		"panel.wechat.directory":       "工作目录",
		"panel.wechat.bots":            "微信机器人",
		"panel.wechat.created":         "已注册: %d",
		"panel.wechat.bound":           "已绑定: %d",
		"panel.wechat.available":       "可用: %d",
		"panel.wechat.current_binding": "当前绑定",
		"panel.wechat.none":            "(无)",
		"panel.wechat.default":         "(默认)",
		"panel.wechat.target":          "目标: %s",
		"panel.wechat.bot_list":        "微信机器人列表",
		"panel.wechat.no_bots":         "暂无微信机器人。按 'a' 扫码授权。",
		"panel.wechat.scan_qr":         "📱 请用微信扫描二维码",
		"panel.wechat.waiting_scan":    "等待扫码确认中...",
		"panel.wechat.scanned":         "已扫码！等待确认中...",
		"panel.wechat.auth_confirmed":  "✓ 授权成功！机器人已连接。",
		"panel.wechat.auth_success":    "微信机器人授权成功！",
		"panel.wechat.removed":         "已移除 %s 的绑定",
		"panel.wechat.message.no_bot":  "暂无可用微信机器人，请先创建或启用。",
		"panel.wechat.help":            "[a] 扫码授权  [b] 绑定目录  [e] 编辑  [d] 禁用/启用  [r] 移除  [↑↓] 导航  [esc] 关闭",
	}
}
