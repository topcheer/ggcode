package agentruntime

import "testing"

func TestSelectVisionModel(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		refWin int
		want   string
	}{
		{
			name:   "vision model picked over non-vision",
			models: []string{"deepseek-chat", "gpt-4o"},
			refWin: 128000,
			want:   "gpt-4o",
		},
		{
			name:   "smaller window than reference is excluded",
			models: []string{"gpt-4o"}, // 128k
			refWin: 1000000,
			want:   "",
		},
		{
			name:   "smallest qualifying window wins",
			models: []string{"gpt-4.1", "gpt-4o"}, // 1M vs 128k, both vision
			refWin: 128000,
			want:   "gpt-4o",
		},
		{
			name:   "exact 1M reference matches 1M vision candidate",
			models: []string{"glm-5.3", "glm-5.3-flash"}, // non-vision + 1M vision
			refWin: 1000000,
			want:   "glm-5.3-flash",
		},
		{
			name:   "no vision candidates at all",
			models: []string{"deepseek-chat", "glm-5.3"},
			refWin: 128000,
			want:   "",
		},
		{
			name:   "empty list",
			models: nil,
			refWin: 128000,
			want:   "",
		},
		{
			name:   "zero reference window accepts any vision model",
			models: []string{"gpt-4o", "gpt-4.1"},
			refWin: 0,
			want:   "gpt-4o",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SelectVisionModel(tt.models, tt.refWin); got != tt.want {
				t.Errorf("SelectVisionModel(%v, %d) = %q, want %q", tt.models, tt.refWin, got, tt.want)
			}
		})
	}
}
