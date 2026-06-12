# App Store Review Information

## Review Notes

GGCode Mobile is a **companion app** that connects to the GGCode desktop AI coding agent. It requires a running GGCode desktop instance to function.

### How to Test

1. **Install GGCode Desktop** on macOS:
   - Download from https://github.com/topcheer/ggcode/releases
   - Or build from source: `go build -tags goolm ./cmd/ggcode-desktop`

2. **Start GGCode Desktop** with relay enabled:
   - Launch the app
   - The share dialog will appear automatically with a QR code

3. **Connect from this iOS app**:
   - Tap "Scan QR Code" and scan the QR code shown on desktop
   - Or manually enter the WebSocket URL displayed on desktop

4. **Send a message** in the chat:
   - Type any coding question (e.g., "What files are in the current directory?")
   - The AI agent on desktop will process it and stream results back

5. **Review tool executions**:
   - The agent will execute tools (file read, terminal commands, etc.)
   - Results are displayed in collapsible tool cards
   - Tap "Approve" or "Reject" for tools that require permission

### Key Screens

| Screen | Description |
|--------|-------------|
| Connect | QR scanner + manual URL entry |
| Chat | Streaming AI conversation + tool results |
| Ask User | Interactive questionnaire from the agent |

### Notes for Reviewer

- **No login/account required** — the app connects via QR code or URL
- **No data collection** — all communication is direct WebSocket to your own desktop
- **No IAP/ads** — completely free, no monetization
- **Works on local network only** — desktop and phone must be on the same network (or use a relay server)
- The app has no standalone functionality without a desktop GGCode instance

### Demo Video

A screen recording demonstrating the full flow (scan → connect → chat → tool execution) is available at: https://ggcode.dev/demo

### Contact

- Support URL: https://ggcode.dev
- Privacy Policy: https://ggcode.dev/privacy
