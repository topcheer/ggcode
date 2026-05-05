package tui

func init() {
	registerCatalog(enQROverlay(), zhQROverlay())
}

func enQROverlay() map[string]string {
	return map[string]string{
		"panel.qr.title":     "Scan to Add Bot",
		"panel.qr.scan_hint": "Scan the QR code with your mobile app to add this bot",
		"panel.qr.esc_hint":  "Esc or q - back to panel",
		"panel.qr.no_qr":     "No contact link available for this adapter",
	}
}

func zhQROverlay() map[string]string {
	return map[string]string{
		"panel.qr.title":     "扫码添加机器人",
		"panel.qr.scan_hint": "用手机客户端扫描二维码添加机器人",
		"panel.qr.esc_hint":  "Esc 或 q - 返回面板",
		"panel.qr.no_qr":     "当前适配器没有可用的联系链接",
	}
}
