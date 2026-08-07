package brainstore

import (
	"encoding/json"
	"regexp"
	"strings"
)

// EntireSignalKind classifies how an Entire capability showed up in a transcript.
const (
	EntireSignalSkill = "skill" // Claude Skill tool or /entire slash prompt
	EntireSignalCLI   = "cli"   // Bash/Shell running `entire …`
	EntireSignalMCP   = "mcp"   // MCP/tool name tied to Entire
)

// Capability family labels used for DistinctEntireCapabilities / scoring.
const (
	CapabilitySkill  = "skill"
	CapabilityGraph  = "graph"
	CapabilityBrain  = "brain"
	CapabilitySem    = "sem"
	CapabilityJudge  = "judge"
	CapabilityPlugin = "plugin"
	CapabilityOther  = "other"
)

// EntireSignal is one detected Entire skill/CLI/MCP use in a session transcript.
type EntireSignal struct {
	Kind       string // EntireSignalSkill / EntireSignalCLI / EntireSignalMCP
	Capability string // Capability* family
	Detail     string // skill name, command snippet, or tool name
	Line       int    // 1-based JSONL line number
	ToolUseID  string
}

// entireCLICommand matches an `entire <subcommand>` invocation inside a shell
// command (including after pipes / && / ;). Captures the subcommand token.
var entireCLICommand = regexp.MustCompile(`(?:^|[;&|]|&&|\|\|)\s*(?:sudo\s+)?entire(?:\s+([A-Za-z0-9][\w-]*))?`)

// entireSlashCommand matches a leading /entire… or /skill:entire… prompt token.
var entireSlashCommand = regexp.MustCompile(`(?i)^/(?:skill:)?(entire(?:[-_][A-Za-z0-9][\w-]*)?)(?:\b|$)`)

// ExtractEntireSignals scans a RAW transcript for Entire-specific skill, CLI, and
// MCP usage. It is self-contained (does not import the host CLI) and only counts
// Entire signals — third-party skills and unrelated tools are ignored.
func ExtractEntireSignals(rawTranscript string) []EntireSignal {
	var out []EntireSignal
	lineNo := 0
	for _, raw := range strings.Split(rawTranscript, "\n") {
		lineNo++
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}

		// Human slash prompts: /entire, /skill:entire, /entire-graph, …
		if isHumanTurn(obj) {
			text := strings.TrimSpace(conversationText(obj))
			if sig, ok := entireSignalFromSlashPrompt(text, lineNo); ok {
				out = append(out, sig)
			}
		}

		for _, block := range toolUseBlocks(obj) {
			name := jsonString(block["name"])
			id := jsonString(block["id"])
			input := block["input"]

			switch {
			case name == "Skill":
				skillName := normalizeSkillName(toolInputString(input, "skill"))
				if skillName == "" {
					skillName = normalizeSkillName(toolInputString(input, "name"))
				}
				if cap, ok := entireSkillCapability(skillName); ok {
					out = append(out, EntireSignal{
						Kind:       EntireSignalSkill,
						Capability: cap,
						Detail:     skillName,
						Line:       lineNo,
						ToolUseID:  id,
					})
				}
			case isShellTool(name):
				cmd := firstNonEmptyString(
					toolInputString(input, "command"),
					toolInputString(input, "cmd"),
					jsonString(input),
				)
				out = append(out, entireSignalsFromShell(cmd, lineNo, id)...)
			default:
				if cap, detail, ok := entireMCPTool(name); ok {
					out = append(out, EntireSignal{
						Kind:       EntireSignalMCP,
						Capability: cap,
						Detail:     detail,
						Line:       lineNo,
						ToolUseID:  id,
					})
				}
			}
		}
	}
	return out
}

func entireSignalFromSlashPrompt(text string, line int) (EntireSignal, bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	m := entireSlashCommand.FindStringSubmatch(trimmed)
	if m == nil {
		return EntireSignal{}, false
	}
	name := normalizeSkillName(m[1])
	cap, ok := entireSkillCapability(name)
	if !ok {
		return EntireSignal{}, false
	}
	return EntireSignal{
		Kind:       EntireSignalSkill,
		Capability: cap,
		Detail:     "/" + name,
		Line:       line,
	}, true
}

func entireSignalsFromShell(cmd string, line int, toolUseID string) []EntireSignal {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	matches := entireCLICommand.FindAllStringSubmatch(cmd, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []EntireSignal
	seen := map[string]struct{}{}
	for _, m := range matches {
		sub := ""
		if len(m) > 1 {
			sub = strings.ToLower(strings.TrimSpace(m[1]))
		}
		cap := capabilityFromCLISubcommand(sub)
		key := cap + "|" + sub
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		detail := "entire"
		if sub != "" {
			detail = "entire " + sub
		}
		// Keep a short command excerpt for jury bullets.
		excerpt := strings.Join(strings.Fields(cmd), " ")
		if len(excerpt) > 80 {
			excerpt = excerpt[:77] + "..."
		}
		out = append(out, EntireSignal{
			Kind:       EntireSignalCLI,
			Capability: cap,
			Detail:     detail + " :: " + excerpt,
			Line:       line,
			ToolUseID:  toolUseID,
		})
	}
	return out
}

func capabilityFromCLISubcommand(sub string) string {
	switch sub {
	case "graph":
		return CapabilityGraph
	case "brain":
		return CapabilityBrain
	case "sem", "semantic":
		return CapabilitySem
	case "judge":
		return CapabilityJudge
	case "plugin", "plugins":
		return CapabilityPlugin
	case "":
		return CapabilityOther
	default:
		// Unknown entire subcommand still counts as CLI awareness.
		return CapabilityOther
	}
}

// entireSkillCapability reports whether a skill name is Entire-related and which
// capability family it maps to. Accepts "entire", "entire-graph", "entire_brain", etc.
func entireSkillCapability(name string) (string, bool) {
	name = normalizeSkillName(name)
	if name == "" {
		return "", false
	}
	if name == "entire" {
		return CapabilitySkill, true
	}
	if strings.HasPrefix(name, "entire-") || strings.HasPrefix(name, "entire_") {
		rest := name[len("entire")+1:]
		return capabilityFromCLISubcommand(strings.ToLower(rest)), true
	}
	return "", false
}

func normalizeSkillName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	if rest, ok := strings.CutPrefix(strings.ToLower(name), "skill:"); ok {
		name = rest
	}
	return strings.TrimSpace(name)
}

func isShellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "run_terminal_cmd", "run_command", "terminal":
		return true
	default:
		return false
	}
}

// entireMCPTool maps MCP/tool names that clearly belong to Entire onto a
// capability family. Examples: mcp__plugin-entire-graph__search, entire_graph_search.
func entireMCPTool(name string) (capability, detail string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return "", "", false
	}
	if !strings.Contains(lower, "entire") {
		return "", "", false
	}
	// Avoid counting the judge's own empty MCP config noise; real tools carry a name.
	detail = name
	switch {
	case strings.Contains(lower, "graph"):
		return CapabilityGraph, detail, true
	case strings.Contains(lower, "brain"):
		return CapabilityBrain, detail, true
	case strings.Contains(lower, "sem") || strings.Contains(lower, "semantic"):
		return CapabilitySem, detail, true
	case strings.Contains(lower, "judge"):
		return CapabilityJudge, detail, true
	case strings.Contains(lower, "plugin"):
		return CapabilityPlugin, detail, true
	default:
		return CapabilityOther, detail, true
	}
}

// toolUseBlocks returns tool_use content blocks from an assistant-shaped JSONL
// record (claude type:assistant / type:message, and bare content arrays).
func toolUseBlocks(obj map[string]any) []map[string]any {
	var content any
	switch jsonString(obj["type"]) {
	case "assistant", "user":
		content = jsonMap(obj["message"])["content"]
	case "message":
		payload := jsonMap(obj["message"])
		// Only assistant tool_use blocks count as invocations.
		if role := jsonString(payload["role"]); role != "" && role != "assistant" {
			return nil
		}
		content = payload["content"]
	default:
		// Some agents put content at the top level.
		if c := obj["content"]; c != nil {
			content = c
		} else {
			return nil
		}
	}
	arr, ok := content.([]any)
	if !ok {
		return nil
	}
	var blocks []map[string]any
	for _, item := range arr {
		block := jsonMap(item)
		if jsonString(block["type"]) != "tool_use" {
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func toolInputString(input any, key string) string {
	switch v := input.(type) {
	case map[string]any:
		return jsonString(v[key])
	case string:
		// Sometimes input is a JSON string.
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return jsonString(m[key])
		}
	}
	return ""
}
