package stream

type PlatformPreset struct {
	Name string

	ID string

	URL string

	Region string

	HelpURL string

	KeyHint string

	Protocol string
}

var Presets = []PlatformPreset{

	{
		Name:     "YouTube",
		ID:       "youtube",
		URL:      "rtmps://a.rtmp.youtube.com/live2",
		Region:   "international",
		HelpURL:  "https://support.google.com/youtube/answer/2907883",
		KeyHint:  "YouTube Studio → Go Live → Stream URL → Stream Key",
		Protocol: "rtmps",
	},
	{
		Name:     "Twitch",
		ID:       "twitch",
		URL:      "rtmps://live.twitch.tv/app",
		Region:   "international",
		HelpURL:  "https://help.twitch.tv/s/article/live-streaming-guide",
		KeyHint:  "Twitch Dashboard → Settings → Stream → Primary Stream Key",
		Protocol: "rtmps",
	},
	{
		Name:     "Facebook Live",
		ID:       "facebook",
		URL:      "rtmps://live-api-s.facebook.com:443/rtmp",
		Region:   "international",
		HelpURL:  "https://www.facebook.com/help/585152814910608",
		KeyHint:  "Facebook → Live Producer → Stream Key",
		Protocol: "rtmps",
	},
	{
		Name:     "X/Twitter",
		ID:       "twitter",
		URL:      "rtmp://rtmp.pscp.tv:80/x",
		Region:   "international",
		HelpURL:  "https://help.x.com/en/using-x/live-video",
		KeyHint:  "X → Go Live → Stream Key (per-session)",
		Protocol: "rtmp",
	},

	{
		Name:     "Bilibili (哔哩哔哩)",
		ID:       "bilibili",
		URL:      "rtmp://live-push.bilivideo.com/live-bvc",
		Region:   "china",
		HelpURL:  "https://link.bilibili.com/p/center/index",
		KeyHint:  "B站直播中心 → 我的直播间 → 开始直播 → 服务器和串流密钥",
		Protocol: "rtmp",
	},
	{
		Name:     "Douyin (抖音)",
		ID:       "douyin",
		URL:      "rtmp://push.douyin.com/app",
		Region:   "china",
		HelpURL:  "https://live.douyin.com/",
		KeyHint:  "抖音直播 → 开播设置 → 推流地址",
		Protocol: "rtmp",
	},
	{
		Name:     "Kuaishou (快手)",
		ID:       "kuaishou",
		URL:      "rtmp://push.kuaishou.com/live",
		Region:   "china",
		HelpURL:  "https://live.kuaishou.com/",
		KeyHint:  "快手直播 → 开播设置 → 推流地址（每次不同）",
		Protocol: "rtmp",
	},
	{
		Name:     "Huya (虎牙)",
		ID:       "huya",
		URL:      "rtmp://push.huya.com/live",
		Region:   "china",
		HelpURL:  "https://www.huya.com/",
		KeyHint:  "虎牙直播 → 开播设置 → 推流地址和推流码",
		Protocol: "rtmp",
	},
	{
		Name:     "Douyu (斗鱼)",
		ID:       "douyu",
		URL:      "rtmp://tx.direct.douyucdn.cn/douyu",
		Region:   "china",
		HelpURL:  "https://www.douyu.com/",
		KeyHint:  "斗鱼直播 → 开播设置 → 推流地址",
		Protocol: "rtmp",
	},
	{
		Name:     "Xiaohongshu (小红书)",
		ID:       "xiaohongshu",
		URL:      "rtmp://push.xiaohongshu.com/live",
		Region:   "china",
		HelpURL:  "https://www.xiaohongshu.com/",
		KeyHint:  "小红书 → 开播设置 → 推流地址（每次不同）",
		Protocol: "rtmp",
	},
}

func PresetByID(id string) *PlatformPreset {
	for i := range Presets {
		if Presets[i].ID == id {
			return &Presets[i]
		}
	}
	return nil
}

func PresetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}

func PresetsByRegion() map[string][]PlatformPreset {
	m := make(map[string][]PlatformPreset)
	for _, p := range Presets {
		m[p.Region] = append(m[p.Region], p)
	}
	return m
}
