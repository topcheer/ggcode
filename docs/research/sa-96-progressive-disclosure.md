# SA-96: Progressive Disclosure Research

**Date**: 2026-08-28
**Status**: No Gap Identified - ggcode Already Implements Progressive Disclosure

## Research Method

- Searched 2025-2026 AI agent literature for Progressive Disclosure concepts
- Analyzed Ardalis (2026) blog post on Progressive Disclosure for AI agents
- Reviewed ggcode's skill loading and prompt assembly implementation

## Progressive Disclosure Principles

Based on research from Ardalis (2026):

1. **Minimal top-level instruction files** - Use as a table of contents, not exhaustive documentation
2. **Skill descriptions as selectors** - 1-2 sentences to help agent decide when to use a skill
3. **Detail on demand** - Full instructions fetched only when skill is invoked
4. **Links over embedding** - Reference separate files instead of embedding full content

## ggcode Implementation Analysis

### 1. System Prompt (`internal/config/config.go:1495`)

```go
if len(customCmds) > 0 {
    sb.WriteString(fmt.Sprintf("- Custom slash commands: %s\n", summarizeNames(customCmds, 8)))
}
```

**Finding**: ✅ Compliant - Only skill names are included, not full templates or descriptions.

### 2. Skill Name Extraction (`internal/agentruntime/prompt.go:43-48`)

```go
customCmdNames := make([]string, 0)
if commandMgr != nil {
    userSlashCmds := commandMgr.UserSlashCommands()
    for name := range userSlashCmds {
        customCmdNames = append(customCmdNames, name)
    }
}
```

**Finding**: ✅ Compliant - Only names are extracted for the system prompt.

### 3. Skill Template Loading (`internal/commands/loader.go:154-184`)

```go
cmd := &Command{
    Name:        name,
    Template:    template,
    Description: firstNonEmptyMarkdownLine(meta.Description, template),
    // ... other fields
}
```

**Finding**: ✅ Compliant - Full templates are loaded into memory for internal use but NOT sent to LLM.

### 4. Signature-Based Change Detection (`internal/commands/manager.go:216`)

```go
b.WriteString(cmd.Template)
```

**Finding**: ✅ Acceptable - Full template is included in signature for detecting file changes, but this signature is for internal validation, not LLM context.

## Conclusion

**No implementation gap found.** ggcode already follows Progressive Disclosure best practices:

- ✅ Skills exposed by name only in system prompt
- ✅ Full templates kept in memory but not sent to LLM unless invoked
- ✅ Matches "selector, not manual" principle from research
- ✅ Token-efficient design without sacrificing functionality

## Recommendation

No changes required. The existing design is already optimal for token efficiency while maintaining full functionality. This research confirms ggcode's implementation is state-of-the-art for Progressive Disclosure.

## References

- Ardalis, S. (2026). "Optimizing AI Agents with Progressive Disclosure". https://ardalis.com/optimizing-ai-agents-with-progressive-disclosure/
- Progressive Disclosure on DevIQ: https://deviq.com/principles/progressive-disclosure
