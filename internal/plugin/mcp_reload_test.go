package plugin

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMCPServerConfigEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b config.MCPServerConfig
		want bool
	}{
		{
			name: "identical",
			a:    config.MCPServerConfig{Name: "s1", Type: "stdio", Command: "node"},
			b:    config.MCPServerConfig{Name: "s1", Type: "stdio", Command: "node"},
			want: true,
		},
		{
			name: "different name",
			a:    config.MCPServerConfig{Name: "s1", Type: "stdio"},
			b:    config.MCPServerConfig{Name: "s2", Type: "stdio"},
			want: false,
		},
		{
			name: "different command",
			a:    config.MCPServerConfig{Name: "s1", Command: "node"},
			b:    config.MCPServerConfig{Name: "s1", Command: "python"},
			want: false,
		},
		{
			name: "different args",
			a:    config.MCPServerConfig{Name: "s1", Args: []string{"a", "b"}},
			b:    config.MCPServerConfig{Name: "s1", Args: []string{"a", "c"}},
			want: false,
		},
		{
			name: "different env",
			a:    config.MCPServerConfig{Name: "s1", Env: map[string]string{"K": "v1"}},
			b:    config.MCPServerConfig{Name: "s1", Env: map[string]string{"K": "v2"}},
			want: false,
		},
		{
			name: "same env different order",
			a:    config.MCPServerConfig{Name: "s1", Env: map[string]string{"A": "1", "B": "2"}},
			b:    config.MCPServerConfig{Name: "s1", Env: map[string]string{"B": "2", "A": "1"}},
			want: true,
		},
		{
			name: "different readonly",
			a:    config.MCPServerConfig{Name: "s1", ReadOnly: false},
			b:    config.MCPServerConfig{Name: "s1", ReadOnly: true},
			want: false,
		},
		{
			name: "different url",
			a:    config.MCPServerConfig{Name: "s1", URL: "http://a"},
			b:    config.MCPServerConfig{Name: "s1", URL: "http://b"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mcpServerConfigEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("mcpServerConfigEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}
