package tui

// Review and promote command handling for the harness slash command.
//
// The /harness review and /harness promote sub-commands are dispatched
// inline within handleHarnessCommand (in commands_harness.go). When these
// handlers grow large enough to warrant extraction into standalone methods,
// they should be placed here.
