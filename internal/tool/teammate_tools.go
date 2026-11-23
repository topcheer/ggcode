package tool

// TeammateDeniedTools is the single source of truth for tools a swarm
// teammate must never hold, no matter what allowed_tools says. It used to be
// duplicated (and drift) between the CLI and desktop tool builders.
//
// Why each group:
//   - no interactive user, no nested agents, no session-scoped control:
//     ask_user / spawn_agent-family / best_of_n / workflow_run /
//     use_namedagent / delegate / wait_agent / list_agents / cancel_agent /
//     compact_context / team lifecycle / teammate_spawn / teammate_shutdown
//     (teammate_list stays: read-only roster, needed to address peers)
//   - leader-seat identity: lanchat (#5917), a2a_remote / a2a_send_task
//     (they act as this workspace's leader toward other instances)
//   - leader-only review + inbox: team_review_* / leader_inbox
var TeammateDeniedTools = []string{
	"ask_user",
	"compact_context",
	"spawn_agent",
	"best_of_n",
	"workflow_run",
	"use_namedagent",
	"delegate",
	"wait_agent",
	"list_agents",
	"cancel_agent",

	"teammate_spawn",
	"teammate_shutdown",
	"team_create",
	"team_delete",
	"team_restore",
	"team_list",
	"team_review_digest",
	"team_review_decide",
	"leader_inbox",

	"lanchat",
	"a2a_remote",
	"a2a_send_task",
}

// TeammateRegistry clones src into the tool set a teammate may use: every
// tool in TeammateDeniedTools is removed first, then - when allowedTools is
// non-empty - everything not named in it is removed as well (allowlist).
// An empty allowedTools means "inherits the full (denied-filtered) surface",
// matching Teammate.AllowedTools semantics.
func TeammateRegistry(src *Registry, allowedTools []string) *Registry {
	if src == nil {
		return nil
	}
	cloned := src.Clone()
	for _, name := range TeammateDeniedTools {
		cloned.Unregister(name)
	}
	if len(allowedTools) > 0 {
		for _, name := range cloned.ToolNames() {
			if !sliceContains(allowedTools, name) {
				cloned.Unregister(name)
			}
		}
	}
	return cloned
}
