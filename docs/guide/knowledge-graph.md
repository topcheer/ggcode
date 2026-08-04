# Knowledge Graph

ggcode maintains a persistent, project-scoped knowledge graph that accumulates structured facts about the codebase across sessions. Unlike flat memory (`save_memory`), the knowledge graph supports typed entities, typed relationships, status lifecycle tracking, and relationship traversal.

## Overview

The knowledge graph tool (`knowledge_graph`) lets the agent record and query:

- **Decisions**: Architectural or technical choices (with status: proposed, accepted, superseded, rejected)
- **Patterns**: Design patterns and conventions adopted in the codebase
- **Entities**: Key modules, files, or components
- **Issues**: Known issues and workarounds
- **Notes**: General knowledge entries

Each node can be linked to other nodes via typed edges:

- `depends-on`: Node A depends on node B
- `supersedes`: Node A replaces/supersedes node B
- `relates-to`: Node A is related to node B
- `implements`: Node A implements node B
- `contradicts`: Node A contradicts node B
- `evolves-from`: Node A evolved from node B

## Storage

Data is persisted at `<project>/.ggcode/knowledge-graph.json`. The file is loaded lazily and cached in memory after first access. Writes are atomic (write to temp file, then rename).

## Actions

| Action | Description |
|--------|-------------|
| `add` | Create or update a knowledge node |
| `link` | Create a typed edge between two nodes |
| `query` | Search nodes by text (title/content/tags) or filter by type |
| `list` | Show all nodes grouped by type |
| `delete` | Remove a node and its connected edges |
| `trace` | BFS traversal of outgoing relationships from a node |
| `stats` | Summary of nodes/edges by type and status |

## Usage Examples

### Recording an architectural decision

```json
{
  "action": "add",
  "type": "decision",
  "title": "Use repository pattern for data access",
  "content": "All data access goes through repository structs, not direct DB calls",
  "status": "accepted",
  "tags": ["architecture", "data-layer"]
}
```

### Linking related concepts

```json
{
  "action": "link",
  "id": "repository-pattern",
  "to": "user-service",
  "type": "implements"
}
```

### Tracing relationships

```json
{
  "action": "trace",
  "id": "auth-module"
}
```

Output shows the relationship chain:
```
[entity] Auth Module
  --[depends-on]--> [entity] OAuth Provider
    --[relates-to]--> [entity] Session Manager
```

## Competitor Comparison

No major AI coding agent maintains a structured, queryable knowledge graph:

| Product | Knowledge Persistence |
|---------|----------------------|
| Claude Code | Flat CLAUDE.md + ephemeral memory |
| Cursor | .cursorrules only |
| Cline/OpenHands | No cross-session knowledge |
| Aider | Static repo map, not accumulated |
| Windsurf | Flat rules files |

ggcode's knowledge graph is unique in supporting typed entities, typed relationships, lifecycle status tracking, and graph traversal - all persisting across sessions.

## Limits

- Maximum 500 nodes per project
- Maximum 1000 edges per project
- Maximum 8 tags per node
- Maximum 200 chars for title, 4000 chars for content
- Trace traversal capped at depth 5 and 50 nodes
