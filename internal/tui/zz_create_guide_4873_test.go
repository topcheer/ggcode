package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #4873: the where-to-get-credentials guide (Hermes-style numbered steps)
// must render IN the panel when createMode is active, in both languages,
// with the VERIFIED console URLs (q.qq.com - not the API-only bots.qq.com;
// dev.dingtalk.com - the console the adapter itself links to).
func TestCreateGuideRendersInCreateMode(t *testing.T) {
	cases := []struct {
		name     string
		language string
		open     func(m *Model)
		render   func(m *Model) string
		create   func(m *Model)
		want     []string
	}{
		{
			name:     "qq english",
			language: "",
			open:     func(m *Model) { m.openQQPanel() },
			render:   func(m *Model) string { return m.renderQQPanel() },
			create:   func(m *Model) { m.qqPanel.createMode = true },
			want:     []string{"https://q.qq.com", "AppID and AppSecret", "2. Create a robot app"},
		},
		{
			name:     "qq chinese",
			language: "zh-CN",
			open:     func(m *Model) { m.openQQPanel() },
			render:   func(m *Model) string { return m.renderQQPanel() },
			create:   func(m *Model) { m.qqPanel.createMode = true },
			want:     []string{"https://q.qq.com", "QQ 开放平台", "AppID 和 AppSecret"},
		},
		{
			name:     "dingtalk english",
			language: "",
			open:     func(m *Model) { m.openDingtalkPanel() },
			render:   func(m *Model) string { return m.renderDingtalkPanel() },
			create:   func(m *Model) { m.dingtalkPanel.createMode = true },
			want:     []string{"https://dev.dingtalk.com", "AppKey and AppSecret"},
		},
		{
			name:     "dingtalk chinese",
			language: "zh-CN",
			open:     func(m *Model) { m.openDingtalkPanel() },
			render:   func(m *Model) string { return m.renderDingtalkPanel() },
			create:   func(m *Model) { m.dingtalkPanel.createMode = true },
			want:     []string{"https://dev.dingtalk.com", "钉钉开发者后台", "AppKey 和 AppSecret"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(nil, nil)
			m.handleResize(160, 50)
			m.SetConfig(&config.Config{
				Language: tc.language,
				IM: config.IMConfig{
					Adapters: map[string]config.IMAdapterConfig{
						"qq-a": {Enabled: true, Platform: "qq"},
					},
				},
			})
			tc.open(&m)
			tc.create(&m)
			rendered := tc.render(&m)
			for _, want := range tc.want {
				if !strings.Contains(rendered, want) {
					t.Fatalf("expected guide line %q in create mode render, got:\n%s", want, rendered)
				}
			}
		})
	}
}

// The guide is scoped to createMode: idle panels must NOT push setup
// instructions at users who are not creating (the #4869 lesson).
func TestCreateGuideAbsentWhenNotCreating(t *testing.T) {
	m := NewModel(nil, nil)
	m.handleResize(160, 50)
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"qq-a": {Enabled: true, Platform: "qq"},
			},
		},
	})
	m.openQQPanel()
	rendered := m.renderQQPanel()
	if strings.Contains(rendered, "q.qq.com") {
		t.Fatalf("guide must not render outside create mode, got:\n%s", rendered)
	}
}
