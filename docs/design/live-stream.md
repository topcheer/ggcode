# ggcode Live Stream Design

## Overview

Built-in live streaming feature that captures the TUI rendering output (ANSI text) and pushes to multiple streaming platforms simultaneously. Works entirely within the process — no platform-specific screen capture APIs needed.

## Key Principles

- **Virtual terminal capture**: Use `Model.View()` from Bubble Tea to get rendered ANSI strings, convert to image frames via ANSI-to-image library
- **Cross-platform**: Pure Go, no avfoundation/X11/DXGI dependency
- **Background-capable**: Works even when TUI window is minimized or in background
- **TUI-only content**: Only terminal content, no desktop background or other windows

## Architecture

```
Bubble Tea Model.View()
        │
        ▼
   ANSI String ──────► ANSI Parser + Font Rasterizer ──────► image.RGBA frames
                                                                    │
                                                                    ▼
                                                            H.264 Encoder (cgo libx264 or ffmpeg sub-process)
                                                                    │
                                                        ┌───────────┼───────────┐
                                                        ▼           ▼           ▼
                                                   RTMPS/RTMP   RTMPS/RTMP   RTMPS/RTMP
                                                   YouTube      Bilibili     Twitch
```

## Supported Platforms

### International
| Platform | Protocol | URL | Auth |
|----------|----------|-----|------|
| YouTube Live | RTMPS (port 443) | `rtmps://a.rtmp.youtube.com/live2/{key}` | Stream Key |
| Twitch | RTMPS | `rtmps://live.twitch.tv/app/{key}` | Stream Key |
| Facebook Live | RTMPS | `rtmps://live-api-s.facebook.com:443/rtmp/{key}` | Stream Key |
| X/Twitter | RTMP | Per-session URL | Stream Key |

### China Domestic
| Platform | Protocol | URL | Auth |
|----------|----------|-----|------|
| Bilibili | RTMP | `rtmp://live-push.bilivideo.com/live-bvc/{key}` | Stream Key (from live room settings) |
| Douyin (抖音) | RTMP | `rtmp://push.douyin.com/app/{key}` | Stream Key |
| Kuaishou (快手) | RTMP | Per-session URL | Stream Key |
| Huya (虎牙) | RTMP | `rtmp://push.huya.com/live/{key}` | Stream Key |
| Douyu (斗鱼) | RTMP | `rtmp://tx.direct.douyucdn.cn/douyu/{key}` | Stream Key |
| Xiaohongshu (小红书) | RTMP | Per-session URL | Stream Key |

## Configuration

```yaml
stream:
  # Global settings
  fps: 15                    # Frame rate (default 15, terminal changes slowly)
  width: 1280                # Output width
  height: 720                # Output height (maintain aspect ratio with padding)
  quality: 26                # QP value for H.264 (lower = better quality, higher bitrate)
  font_size: 14              # Font size in points for rendering
  # font_path: ""            # Optional: custom TTF font path
  
  # Platform targets
  targets:
    - name: youtube
      enabled: true
      url: "rtmps://a.rtmp.youtube.com/live2"
      key: "${YOUTUBE_STREAM_KEY}"
      
    - name: bilibili
      enabled: true
      url: "rtmp://live-push.bilivideo.com/live-bvc"
      key: "${BILIBILI_STREAM_KEY}"
      
    - name: twitch
      enabled: false
      url: "rtmps://live.twitch.tv/app"
      key: "${TWITCH_STREAM_KEY}"
```

## Runtime Control

### TUI Slash Commands
| Command | Description |
|---------|-------------|
| `/stream start [targets...]` | Start streaming (all enabled, or named targets) |
| `/stream stop [targets...]` | Stop streaming (all, or named targets) |
| `/stream status` | Show all targets with status/bitrate/uptime |
| `/stream add <name> <url> <key>` | Add a new target at runtime |
| `/stream remove <name>` | Remove a target |
| `/stream toggle <name>` | Enable/disable a target |

### IM Slash Commands (Daemon Mode)
Same commands available via IM: `/stream start`, `/stream stop`, `/stream status`

### WebUI
REST API endpoints:
- `GET /api/stream/status` — all targets with stats
- `POST /api/stream/start` — `{ "targets": ["youtube"] }`
- `POST /api/stream/stop` — `{ "targets": ["youtube"] }`

## Implementation Components

### 1. ANSI Renderer (`internal/stream/renderer.go`)
- Parse ANSI escape sequences (colors, styles, cursor positioning)
- Render to `image.RGBA` using Go freetype or pre-rasterized font atlas
- Target: 15fps on 1280×720 should be achievable with <5% CPU

### 2. Video Encoder (`internal/stream/encoder.go`)
- **Option A**: `exec.Command("ffmpeg")` sub-process, pipe raw frames via stdin
  - Pros: No CGO, battle-tested encoding, supports all codecs
  - Cons: External dependency on ffmpeg binary
- **Option B**: CGO `libx264` binding
  - Pros: No external binary, lower latency
  - Cons: CGO breaks cross-compilation, build complexity
- **Recommended**: Option A (ffmpeg sub-process), with graceful fallback if ffmpeg not found

### 3. Multi-Target Manager (`internal/stream/manager.go`)
- Fan-out: one encoder output → N RTMP connections
- Per-target goroutine with independent reconnect logic
- Each target has its own state: `idle | connecting | live | error | stopped`
- Target state changes emit events for TUI/IM/WebUI notification

### 4. RTMP Client (`internal/stream/rtmp.go`)
- Use existing Go RTMP libraries (`github.com/yutopp/go-rtmp` or similar)
- Or: pipe encoded FLV to ffmpeg sub-processes (one per target)
- TLS support for RTMPS (YouTube, Twitch, Facebook)

### 5. Frame Source Integration (`internal/tui/stream_hook.go`)
- Hook into Bubble Tea's render cycle
- On each `View()` call, capture the output and feed to the stream renderer
- Throttle to target FPS (don't render 60fps for a 15fps stream)

## Frame Pipeline Detail

```
Every 1/FPS seconds:
  1. model.View() → ANSI string (already rendered by Bubble Tea)
  2. ANSI string → renderer.Render() → image.RGBA
  3. image.RGBA → encoder.Encode() → H.264 NAL units in FLV container
  4. FLV data → fan-out to all active target goroutines
  5. Each target: FLV data → RTMP/RTMPS connection → platform server
```

## Technical Notes

- **Audio**: Include silent AAC audio track (platforms like YouTube require audio)
- **Bitrate**: Terminal content is very compressible; expect 500-2000kbps at QP 26
- **Latency**: With `zerolatency` tune and low buffer, expect 2-5 seconds end-to-end
- **Reconnect**: Exponential backoff (2s, 5s, 15s, 30s, 60s max) on connection failure
- **Stream key security**: Keys stored in config with `${ENV_VAR}` expansion, never logged
- **Aspect ratio**: Maintain TUI aspect ratio, add black padding if needed

## Milestones

### Phase 1: MVP
- Single target, ffmpeg sub-process encoding
- Config-only targets (no runtime add/remove)
- `/stream start` / `/stream stop` commands
- Basic status display

### Phase 2: Multi-Target
- Multiple simultaneous targets with fan-out
- Runtime add/remove/toggle
- Per-target reconnect logic
- WebUI integration

### Phase 3: Polish
- Custom font/size configuration
- Quality/bitrate presets (low/medium/high)
- Stream overlay (title, timer, chat integration)
- Recording to local file simultaneously
