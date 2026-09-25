// Package hookio handles stdin/JSON parsing for Claude Code hooks and writes
// PreToolUse permission decisions.
package hookio

import (
	"encoding/json"
	"fmt"
	"io"
)

// Input is the subset of the PreToolUse payload gitsize-guard uses.
type Input struct {
	HookEventName string    `json:"hook_event_name"`
	ToolName      string    `json:"tool_name"`
	ToolInput     ToolInput `json:"tool_input"`
	Cwd           string    `json:"cwd"`
}

// ToolInput holds the fields of Bash and Write tool inputs.
type ToolInput struct {
	Command  string `json:"command"`   // Bash
	FilePath string `json:"file_path"` // Write
	Content  string `json:"content"`   // Write
}

// Read decodes a hook payload from r.
func Read(r io.Reader) (Input, error) {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Input{}, fmt.Errorf("decode hook input: %w", err)
	}
	return in, nil
}

// Decision is a PreToolUse permission decision. There is deliberately no
// "allow": that would skip Claude Code's own permission prompt, and a guard
// that finds nothing wrong should simply stay silent.
type Decision string

const (
	Deny Decision = "deny" // block the tool call; the reason is shown to Claude
	Ask  Decision = "ask"  // ask the user to confirm; the reason is shown to them
)

type output struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName            string   `json:"hookEventName"`
	PermissionDecision       Decision `json:"permissionDecision"`
	PermissionDecisionReason string   `json:"permissionDecisionReason"`
}

// WriteDecision writes a PreToolUse decision as JSON to w.
func WriteDecision(w io.Writer, d Decision, reason string) error {
	return json.NewEncoder(w).Encode(output{hookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       d,
		PermissionDecisionReason: reason,
	}})
}
