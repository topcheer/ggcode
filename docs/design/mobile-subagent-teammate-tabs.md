# Mobile Sub-Agent / Teammate Tab UI Design

## 1. Current State Analysis

### 1.1 Protocol Layer (Go `internal/tunnel/`)

**Already implemented.** The protocol already defines all required event types:

| Event Type | Const | Data Struct | Purpose |
|---|---|---|---|
| `subagent_spawn` | `EventSubagentSpawn` | `SubagentSpawnData` | Notifies mobile an agent was created |
| `subagent_text` | `EventSubagentText` | `SubagentTextData` | Streaming text chunk from agent |
| `subagent_status` | `EventSubagentStatus` | `SubagentStatusData` | Status update (running/waiting/completed) |
| `subagent_complete` | `EventSubagentComplete` | `SubagentCompleteData` | Agent finished (success/failure) |

The `Broker` (`internal/tunnel/broker.go`) already has Push methods:
- `PushSubagentSpawn(agentID, name, task, color, parentID)`
- `PushSubagentText(agentID, msgID, chunk, done)`
- `PushSubagentStatus(agentID, status, message)`
- `PushSubagentComplete(agentID, name, summary, success)`

All are emitted onto the broker's session-scoped event stream; reconnect replay is owned by the relay's per-client cursor resume logic.

### 1.2 Desktop Agent Bridge (`desktop/ggcode-desktop/agent_bridge.go`)

**Already wired.** Both sub-agents and swarm teammates push events to mobile:

**Sub-agents** (lines 139-180):
- `subAgentMgr.SetOnUpdate` callback fires on every state change
- On `StatusRunning` (first update): `PushSubagentSpawn` + `PushSubagentStatus`
- On each update: pushes latest text event from `sa.Events()` via `PushSubagentText`
- On `StatusCompleted`: pushes result text + `PushSubagentComplete`
- On `StatusFailed`/`StatusCancelled`: pushes error + `PushSubagentComplete`

**Swarm teammates** (lines 207-249):
- `swarmMgr.SetOnUpdate` callback fires on swarm events
- `teammate_spawned`: `PushSubagentSpawn` (with teamID as parentID)
- `teammate_working`: `PushSubagentStatus` + latest text event
- `teammate_idle`: pushes result text + `PushSubagentComplete`
- `teammate_shutdown`: `PushSubagentComplete` with "shutdown"

**Key detail**: Both sub-agents and teammates use the **same protocol events** (`subagent_*`). The `parentID` field distinguishes: empty = sub-agent, teamID = teammate.

### 1.3 Mobile Flutter - Protocol Models (`lib/core/models/protocol.dart`)

**Already complete.** All Dart model classes mirror the Go protocol:
- `SubagentSpawnData` (agentId, name, task, color, parentId)
- `SubagentTextData` (agentId, id, chunk, done)
- `SubagentStatusData` (agentId, status, message)
- `SubagentCompleteData` (agentId, name, summary, success)

### 1.4 Mobile Flutter - State Management (`lib/core/providers/session_provider.dart`)

**Partially implemented.** The message dispatch in `_dispatchMessage` already handles:
- `subagent_spawn`: Creates `SubagentInfo` in `subagentProvider`
- `subagent_text`: Routes to `ChatNotifier.handleSubagentText()`
- `subagent_status`: Updates status in `subagentProvider`
- `subagent_complete`: Updates status + auto-removes after 3 seconds

**The `ChatNotifier`** maintains a **single flat list** of `ChatMessage`. Sub-agent messages have `sourceId` set to the agent's ID, enabling filtering.

**SubagentInfo** tracks: agentId, name, task, color, parentId, status, summary, completed, success.

### 1.5 Mobile Flutter - Chat UI (`lib/features/chat/chat_screen.dart`)

**Tab system already exists!** The `ChatScreen` already implements:
- `TabController` with dynamic tabs
- Tab list = `['main']` + active agents + completed agents
- Messages filtered by `sourceId` for the current tab
- Tab headers show running spinner / completed checkmark
- Dismissible close button on completed tabs
- `_closeTab` removes agent from state and purges its messages

### 1.6 Gaps Identified

1. **Completed agents auto-removed after 3s** - The `subagent_complete` handler removes agents after 3 seconds, which conflicts with the tab UI that shows completed agents until manually closed. The 3s timer should be removed since tabs handle this.

2. **No team lifecycle events** - Team creation/deletion (`team_created`/`team_deleted`) are emitted by swarm manager but NOT forwarded to mobile. Mobile cannot know when a team is created/destroyed.

3. **No `kind` field** - SubagentSpawnData lacks a `kind` field to distinguish sub-agents from teammates. Currently only inferred from `parentId`.

4. **Sub-agent text throttling** - The `subAgentMgr.notifyUpdate` is throttled to 10Hz, so some text chunks may be coalesced. The mobile sees the latest text event on each throttle tick, not every token.

5. **No tool_call/tool_result events per agent** - Sub-agent tool calls are not pushed to mobile. Only text events and final status are sent.

6. **The `SubagentPanel` widget exists but is unused** - `subagent_panel.dart` defines floating live-activity cards but `chat_screen.dart` doesn't include it (uses tabs instead).

---

## 2. Design Proposal

### 2.1 Protocol Enhancements

#### New event types needed:

```
// Team lifecycle (server -> client)
EventTeamCreated  = "team_created"
EventTeamDeleted  = "team_deleted"
EventTeamList     = "team_list"       // bulk sync on reconnect
```

#### Enhanced `SubagentSpawnData`:

```go
type SubagentSpawnData struct {
    AgentID  string `json:"agent_id"`
    Name     string `json:"name"`
    Task     string `json:"task"`
    Kind     string `json:"kind"`                // "subagent" or "teammate" (NEW)
    Color    string `json:"color,omitempty"`
    ParentID string `json:"parent_id,omitempty"` // teamID for teammates
}
```

#### New data structures:

```go
type TeamCreatedData struct {
    TeamID string `json:"team_id"`
    Name   string `json:"name"`
}

type TeamDeletedData struct {
    TeamID string `json:"team_id"`
}
```

### 2.2 Desktop Broker Changes

Add to `broker.go`:

```go
func (b *Broker) PushTeamCreated(teamID, name string) {
    b.send(EventTeamCreated, TeamCreatedData{TeamID: teamID, Name: name})
}

func (b *Broker) PushTeamDeleted(teamID string) {
    b.send(EventTeamDeleted, TeamDeletedData{TeamID: teamID})
}
```

Add to `agent_bridge.go` swarm event handler:

```go
case "team_created":
    b.tunnelBroker.PushTeamCreated(ev.TeamID, ev.TeamName)

case "team_deleted":
    b.tunnelBroker.PushTeamDeleted(ev.TeamID)
```

Update existing `PushSubagentSpawn` calls to include `kind`:
- Sub-agents: `kind = "subagent"`
- Teammates: `kind = "teammate"`

### 2.3 Mobile State Management Changes

#### Remove auto-dismiss timer

In `session_provider.dart`, the `subagent_complete` handler currently has:
```dart
Future.delayed(const Duration(seconds: 3), () {
    final current = Map<String, SubagentInfo>.from(_ref.read(subagentProvider));
    current.remove(data.agentId);
    _ref.read(subagentProvider.notifier).state = current;
});
```
**Remove this.** Completed agents should persist in the tab bar until manually dismissed.

#### Add `kind` to SubagentInfo

```dart
class SubagentInfo {
    final String agentId;
    final String name;
    final String task;
    final String kind;       // NEW: "subagent" or "teammate"
    final String color;
    final String parentId;
    final String status;
    final String? summary;
    final bool completed;
    final bool success;
    // ...
}
```

#### Handle team lifecycle events

Add to `_dispatchMessage`:
```dart
case 'team_created':
    // Update team list, possibly group teammate tabs
    break;
case 'team_deleted':
    // Remove all teammate tabs for this team
    final data = ...;
    final agents = Map<String, SubagentInfo>.from(_ref.read(subagentProvider));
    agents.removeWhere((id, info) => info.parentId == data.teamId);
    _ref.read(subagentProvider.notifier).state = agents;
    break;
```

### 2.4 Mobile Tab UI Design

The tab UI already works well. The following refinements are proposed:

#### Tab bar layout (existing, enhanced)

```
[ Chat ] [ Researcher* ] [ Coder* ] [ Writer ]
  ^           ^              ^          ^
  main    running sub    running tm  completed tm
```

- **Chat** (always present): Main conversation (user <-> agent)
- **Agent tabs**: Dynamically added on `subagent_spawn`
- **Active tabs**: Show spinning indicator
- **Completed tabs**: Show checkmark (success) or error icon, with close button
- **Team tabs**: Teammates show a small team indicator (colored dot matching team)

#### Tab grouping for teams (future enhancement)

For large teams (5+ teammates), consider grouping under a team dropdown:
```
[ Chat ] [ Team Alpha v ] [ Researcher* ]
```
Expanding "Team Alpha" shows all teammates. This is **not needed for v1**.

#### Per-tab content

Each tab shows:
1. **Header**: Agent name, status badge, elapsed time (if running)
2. **Message stream**: Streaming text with typing indicator
3. **Tool calls** (future): Collapsible tool call cards
4. **Summary card** (when complete): Final result in a highlighted box

### 2.5 Streaming per Tab

**Yes, each tab already streams independently.** The current architecture handles this:

1. Desktop pushes `subagent_text` events with `agent_id`
2. Mobile's `handleSubagentText` routes to `ChatMessage` with `sourceId = agentId`
3. Tab filtering shows only messages for the active tab
4. The `streaming: true` flag shows a blinking cursor

The only limitation is the 10Hz throttle on the Go side. For most LLM output, this is sufficient since tokens arrive at ~5-20 tokens/sec.

### 2.6 Team Lifecycle on Mobile

| Event | Source | Mobile Action |
|---|---|---|
| `team_created` | Desktop swarm manager | Show system message "Team 'Alpha' created". Optionally show team tab. |
| `teammate_spawned` | Desktop swarm manager | Add teammate tab, show streaming |
| `teammate_working` | Desktop swarm manager | Update status indicator |
| `teammate_idle` | Desktop swarm manager | Mark tab as completed with result |
| `team_deleted` | Desktop swarm manager | Close all tabs for this team, show "Team disbanded" |

---

## 3. Implementation Plan

### Phase 1: Fix existing issues (low effort, high impact)

**Order: Go first, then Flutter**

| Step | File | Change |
|---|---|---|
| 1.1 | `internal/tunnel/protocol.go` | Add `Kind` field to `SubagentSpawnData`. Add `TeamCreatedData`, `TeamDeletedData` structs. Add event constants. |
| 1.2 | `internal/tunnel/broker.go` | Add `PushTeamCreated`, `PushTeamDeleted` methods. Update `PushSubagentSpawn` signature to include `kind`. |
| 1.3 | `desktop/ggcode-desktop/agent_bridge.go` | Update `PushSubagentSpawn` calls to pass `kind`. Add `team_created`/`team_deleted` handling in swarm onUpdate callback. |
| 1.4 | `mobile/flutter/lib/core/models/protocol.dart` | Add `kind` to `SubagentSpawnData`. Add `TeamCreatedData`, `TeamDeletedData`. |
| 1.5 | `mobile/flutter/lib/core/providers/session_provider.dart` | Remove 3-second auto-dismiss. Add `kind` to `SubagentInfo`. Handle `team_created`/`team_deleted` in dispatch. |
| 1.6 | `mobile/flutter/lib/features/chat/chat_screen.dart` | Minor: show team badge on teammate tabs using `parentId`. |

### Phase 2: Sub-agent tool call visibility (medium effort)

| Step | File | Change |
|---|---|---|
| 2.1 | `internal/tunnel/protocol.go` | Add `SubagentToolCallData`, `SubagentToolResultData` structs. |
| 2.2 | `internal/tunnel/broker.go` | Add `PushSubagentToolCall`, `PushSubagentToolResult` methods. |
| 2.3 | `desktop/ggcode-desktop/agent_bridge.go` | In sub-agent/swarm onUpdate callbacks, push tool_call/tool_result events from the agent's event log. |
| 2.4 | `mobile/flutter/lib/core/models/protocol.dart` | Add Dart models for sub-agent tool events. |
| 2.5 | `mobile/flutter/lib/core/providers/session_provider.dart` | Handle new events, create tool message entries with `sourceId`. |
| 2.6 | `mobile/flutter/lib/features/chat/chat_screen.dart` | Render tool calls within agent tabs (reuse existing `_buildToolMessage`). |

### Phase 3: Polish and edge cases

| Step | File | Change |
|---|---|---|
| 3.1 | `mobile/flutter/lib/features/chat/chat_screen.dart` | Add elapsed time display for running agents. Auto-scroll per-tab. |
| 3.2 | `mobile/flutter/lib/features/chat/chat_screen.dart` | Tab reorder: newly spawned agents jump to front. |
| 3.3 | `mobile/flutter/lib/features/chat/subagent_panel.dart` | Either remove this file or integrate it as a notification overlay when user is on a different tab. |
| 3.4 | `internal/tunnel/broker.go` + `ggcode-relay/main.go` | Ensure team/agent state reconnects correctly through session-scoped event IDs and relay incremental resume. |

---

## 4. Key Code Snippets

### 4.1 Protocol: Add Kind field and team events

```go
// internal/tunnel/protocol.go

// Add to event constants
EventTeamCreated  = "team_created"
EventTeamDeleted  = "team_deleted"

// Update existing struct
type SubagentSpawnData struct {
    AgentID  string `json:"agent_id"`
    Name     string `json:"name"`
    Task     string `json:"task"`
    Kind     string `json:"kind"`                // "subagent" or "teammate"
    Color    string `json:"color,omitempty"`
    ParentID string `json:"parent_id,omitempty"`
}

// New structs
type TeamCreatedData struct {
    TeamID string `json:"team_id"`
    Name   string `json:"name"`
}

type TeamDeletedData struct {
    TeamID string `json:"team_id"`
}
```

### 4.2 Broker: New push methods

```go
// internal/tunnel/broker.go

func (b *Broker) PushSubagentSpawn(agentID, name, task, kind, color, parentID string) {
    b.send(EventSubagentSpawn, SubagentSpawnData{
        AgentID: agentID, Name: name, Task: task,
        Kind: kind, Color: color, ParentID: parentID,
    })
}

func (b *Broker) PushTeamCreated(teamID, name string) {
    b.send(EventTeamCreated, TeamCreatedData{TeamID: teamID, Name: name})
}

func (b *Broker) PushTeamDeleted(teamID string) {
    b.send(EventTeamDeleted, TeamDeletedData{TeamID: teamID})
}
```

### 4.3 Desktop: Update agent_bridge.go calls

```go
// In setupAgent(), sub-agent onUpdate callback:
// Before:
b.tunnelBroker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
// After:
b.tunnelBroker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "subagent", "", "")

// In swarm onUpdate callback:
case "teammate_spawned":
    // Before:
    b.tunnelBroker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", color, ev.TeamID)
    // After:
    b.tunnelBroker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", "teammate", color, ev.TeamID)

case "team_created":
    b.tunnelBroker.PushTeamCreated(ev.TeamID, ev.TeamName)

case "team_deleted":
    b.tunnelBroker.PushTeamDeleted(ev.TeamID)
```

### 4.4 Mobile: Remove auto-dismiss, add kind

```dart
// session_provider.dart - Remove the 3-second timer in subagent_complete handler:
case 'subagent_complete':
    if (msg.data != null) {
        final data = proto.SubagentCompleteData.fromJson(msg.data!);
        final agents = Map<String, SubagentInfo>.from(_ref.read(subagentProvider));
        if (agents.containsKey(data.agentId)) {
            agents[data.agentId] = agents[data.agentId]!.copyWith(
                status: 'completed',
                completed: true,
                success: data.success,
                summary: data.summary,
            );
            _ref.read(subagentProvider.notifier).state = agents;
        }
        // REMOVED: Future.delayed(...) auto-dismiss
    }
    break;
```

### 4.5 Mobile: Handle team lifecycle

```dart
// session_provider.dart - New cases in _dispatchMessage:
case 'team_created':
    if (msg.data != null) {
        final teamId = msg.data!['team_id'] as String? ?? '';
        final name = msg.data!['name'] as String? ?? '';
        chatNotifier.addSystemMessage('Team "$name" created');
    }
    break;

case 'team_deleted':
    if (msg.data != null) {
        final teamId = msg.data!['team_id'] as String? ?? '';
        // Close all teammate tabs for this team
        final agents = Map<String, SubagentInfo>.from(_ref.read(subagentProvider));
        final removed = agents.keys.where(
            (id) => agents[id]?.parentId == teamId
        ).toList();
        for (final id in removed) {
            agents.remove(id);
        }
        _ref.read(subagentProvider.notifier).state = agents;
        // Remove their messages
        final msgs = ref.read(chatProvider);
        final teamAgentIds = removed.toSet();
        ref.read(chatProvider.notifier).state = msgs
            .where((m) => m.sourceId == null || !teamAgentIds.contains(m.sourceId))
            .toList();
        chatNotifier.addSystemMessage('Team disbanded');
    }
    break;
```

### 4.6 Mobile: Tab header with team indicator

```dart
// chat_screen.dart - Enhanced tab builder
Tab(
    height: 36,
    child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
            // Team indicator for teammates
            if (agent != null && agent.kind == 'teammate')
                Container(
                    width: 6, height: 6,
                    margin: const EdgeInsets.only(right: 4),
                    decoration: BoxDecoration(
                        color: _parseColor(agent.color),
                        shape: BoxShape.circle,
                    ),
                ),
            // Status icon (running/completed)
            if (isRunning)
                SizedBox(
                    width: 10, height: 10,
                    child: CircularProgressIndicator(strokeWidth: 1.5),
                )
            else if (isCompleted)
                Icon(agent?.success == true ? Icons.check_circle : Icons.error, size: 12),
            // Name
            Text(name),
            // Close button for completed
            if (isCompleted && id != 'main')
                GestureDetector(
                    onTap: () => _closeTab(id),
                    child: Icon(Icons.close, size: 14),
                ),
        ],
    ),
)
```

---

## 5. Architecture Summary

```
Desktop                           Tunnel                         Mobile
─────────                         ──────                         ──────

subAgentMgr.SetOnUpdate()────┐
                             │    WebSocket (encrypted)
swarmMgr.SetOnUpdate()───────┤         │
                             │         │  GatewayMessage{type, data}
                             ▼         │
                     tunnel.Broker     │
                       .Push*()────────┤──► ConnectionService.messageStream
                                       │         │
                                       │         ▼
                                       │   _dispatchMessage()
                                       │     ├── subagent_spawn  ──► SubagentInfo provider
                                       │     ├── subagent_text   ──► ChatNotifier (sourceId)
                                       │     ├── subagent_status ──► SubagentInfo.status
                                       │     ├── subagent_complete──► SubagentInfo.completed
                                       │     ├── team_created    ──► System message
                                       │     └── team_deleted    ──► Remove teammate tabs
                                       │
                                       │         │
                                       │         ▼
                                       │   ChatScreen
                                       │     TabBar (dynamic)
                                       │     ├── "Chat"     ──► messages where sourceId == null
                                       │     ├── "Agent1"   ──► messages where sourceId == "sa-1"
                                       │     └── "TeamA-Coder"──► messages where sourceId == "tm-3"
                                       │
                                       │   Each tab:
                                       │     ListView of filtered ChatMessages
                                       │     Streaming: blinking cursor on last message
                                       │     Completed: result card + close button
```

### Key Design Decisions

1. **Single flat message list with sourceId filtering** - Simpler than per-agent message lists. The existing `ChatNotifier` already handles this correctly. O(n) filtering is acceptable for typical agent counts (< 20 agents, < 1000 messages).

2. **Same protocol events for sub-agents and teammates** - The `kind` field distinguishes them. Mobile can choose to render them differently (e.g., team-colored dot for teammates).

3. **No new event types for streaming** - The existing `subagent_text` with incremental chunks already supports per-tab streaming. Each tab sees its own messages filtered by `sourceId`.

4. **Tab lifecycle tied to SubagentInfo** - Tabs appear on `subagent_spawn`, update on `subagent_text`/`subagent_status`, mark completed on `subagent_complete`, and close on manual dismiss or `team_deleted`.

5. **Replay on reconnect works automatically** - Broker emits the canonical session event stream, and relay replays the needed incremental history per client. Mobile reconstructs tab state from ordered replayed events.

---

## 6. Files Changed Summary

| File | Phase | Change Type |
|---|---|---|
| `internal/tunnel/protocol.go` | 1.1 | Add `Kind` field, team event types, team data structs |
| `internal/tunnel/broker.go` | 1.2 | Update `PushSubagentSpawn` signature, add team push methods |
| `desktop/ggcode-desktop/agent_bridge.go` | 1.3 | Pass `kind` param, add team event forwarding |
| `mobile/flutter/lib/core/models/protocol.dart` | 1.4 | Add `kind`, add team data classes |
| `mobile/flutter/lib/core/providers/session_provider.dart` | 1.5 | Remove auto-dismiss, add `kind`, handle team events |
| `mobile/flutter/lib/features/chat/chat_screen.dart` | 1.6 | Team indicator on tabs |
