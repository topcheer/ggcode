package im

import (
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	imstt "github.com/topcheer/ggcode/internal/im/stt"
)

// resolveSTTConfigFunc extracts an STT config from global + adapter-specific settings.
type resolveSTTConfigFunc func(global config.IMSTTConfig, extra map[string]interface{}) *config.IMSTTConfig

// buildSTTWithFallback builds a transcriber with local whisper as fallback.
// If remote STT is configured, it will be primary with local whisper as backup.
// If no remote STT, local whisper is used directly (if available).
func buildSTTWithFallback(global config.IMSTTConfig, extra map[string]interface{}, resolve resolveSTTConfigFunc) imstt.Transcriber {
	var primary imstt.Transcriber
	if sttCfg := resolve(global, extra); sttCfg != nil {
		primary = imstt.NewOpenAICompatible(sttCfg.BaseURL, sttCfg.APIKey, sttCfg.Model, sttCfg.Provider)
	}

	local := imstt.NewLocalWhisper("", "", "")
	if local.Available() {
		debug.Log("im", "local whisper available, using as %s", func() string {
			if primary != nil {
				return "fallback"
			}
			return "primary STT"
		}())
		if primary != nil {
			return imstt.NewFallback(primary, local)
		}
		return local
	}

	return primary
}
