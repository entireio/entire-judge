package brainstore

import (
	"encoding/json"
	"strings"
)

// ExtractHumanPrompts pulls the user-authored turns out of a RAW transcript. The
// role label lives on the raw JSONL object, so this operates on the original
// lines (not a preprocessed plain-text form, which collapses each record and
// drops the role). It reuses conversationText to lift the human turn's text out
// of the matched envelope. A non-JSONL transcript has no role to filter on, so
// its preprocessed plain-text form is kept wholesale as a fallback.
func ExtractHumanPrompts(rawTranscript string) []string {
	var prompts []string
	sawJSON := false
	for _, raw := range strings.Split(rawTranscript, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue // not a JSONL record; handled by the fallback below
		}
		sawJSON = true
		if isHumanTurn(obj) {
			text := strings.TrimSpace(conversationText(obj))
			if text != "" {
				prompts = append(prompts, text)
			}
		}
	}
	if !sawJSON {
		for _, line := range strings.Split(preprocessTranscript(rawTranscript), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				prompts = append(prompts, line)
			}
		}
	}
	return prompts
}

// Turn is one attributed human/assistant conversation turn lifted from a raw
// transcript, in original order. Tool calls, tool results, reasoning, and
// session/meta records are dropped — only turns carrying real conversation text
// survive.
type Turn struct {
	Role string // "user" or "assistant"
	Text string
}

// ExtractConversationTurns pulls the ordered human AND assistant turns out of a
// RAW transcript, reusing the same envelope-shape handling as ExtractHumanPrompts
// (bare {role}, {type:"user"/"assistant"}, {type:"message"}, {type:"response_item"},
// {type:"event_msg"} for claude/codex/pi) plus conversationText to lift the turn's
// text. Each returned Turn carries a normalized role ("user" or "assistant"). A
// non-JSONL transcript yields no attributed turns — its role is unknown — matching
// ExtractHumanPrompts' JSONL-only role filtering. This is what the integrity lens
// reads: the assistant turns (where a warning lives) and the adjacent human turns
// (whether the team addressed it or overrode it).
func ExtractConversationTurns(rawTranscript string) []Turn {
	var turns []Turn
	for _, raw := range strings.Split(rawTranscript, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue // not a JSONL record; no role to attribute
		}
		role := turnRole(obj)
		if role == "" {
			continue
		}
		text := strings.TrimSpace(conversationText(obj))
		if text == "" {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	return turns
}

// turnRole normalizes a transcript JSONL object's author to "user", "assistant",
// or "" (tool/meta/unknown). It mirrors isHumanTurn's envelope coverage but also
// resolves assistant/agent turns, across the claude/codex/pi shapes.
func turnRole(obj map[string]any) string {
	if role := jsonString(obj["role"]); role == "user" || role == "assistant" {
		return role
	}
	switch jsonString(obj["type"]) {
	case "user":
		return "user"
	case "assistant", "agent_message":
		return "assistant"
	case "message":
		if r := jsonString(jsonMap(obj["message"])["role"]); r == "user" || r == "assistant" {
			return r
		}
	case "response_item":
		payload := jsonMap(obj["payload"])
		if jsonString(payload["type"]) == "message" {
			if r := jsonString(payload["role"]); r == "user" || r == "assistant" {
				return r
			}
		}
	case "event_msg":
		switch jsonString(jsonMap(obj["payload"])["type"]) {
		case "user_message":
			return "user"
		case "agent_message", "task_complete":
			return "assistant"
		}
	}
	return ""
}

// isHumanTurn reports whether a transcript JSONL object is a human-authored
// turn. It covers the common envelope shapes seen across agents (bare {role},
// {type:"user"}, {type:"message",message:{role}}, {type:"response_item",
// payload:{role}}, {type:"event_msg",payload:{type:"user_message"}}).
func isHumanTurn(obj map[string]any) bool {
	if role := jsonString(obj["role"]); role == "user" {
		return true
	}
	switch jsonString(obj["type"]) {
	case "user":
		return true
	case "message":
		return jsonString(jsonMap(obj["message"])["role"]) == "user"
	case "response_item":
		return jsonString(jsonMap(obj["payload"])["role"]) == "user"
	case "event_msg":
		return jsonString(jsonMap(obj["payload"])["type"]) == "user_message"
	}
	return false
}

// preprocessTranscript collapses each JSONL record onto a single line of its
// conversation text, preserving line count. Non-JSONL lines pass through
// unchanged so a plain-text transcript is kept as-is.
func preprocessTranscript(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue // not JSONL; leave the line as-is
		}
		lines[i] = strings.Join(strings.Fields(conversationText(obj)), " ")
	}
	return strings.Join(lines, "\n")
}

// conversationText returns the human/assistant text worth keeping from one
// transcript record, or "" for tool calls, tool outputs, reasoning, and
// session/meta records. It covers codex (event_msg/response_item), Claude
// (assistant/user), and pi (message) envelope shapes.
func conversationText(obj map[string]any) string {
	switch jsonString(obj["type"]) {
	case "agent_message":
		return jsonString(obj["message"])
	case "event_msg":
		payload := jsonMap(obj["payload"])
		switch jsonString(payload["type"]) {
		case "agent_message", "user_message":
			return jsonString(payload["message"])
		case "task_complete":
			return jsonString(payload["last_agent_message"])
		}
		return ""
	case "response_item":
		payload := jsonMap(obj["payload"])
		if jsonString(payload["type"]) != "message" {
			return ""
		}
		if role := jsonString(payload["role"]); role != "assistant" && role != "user" {
			return ""
		}
		return textBlocks(payload["content"])
	case "assistant", "user":
		return textBlocks(jsonMap(obj["message"])["content"])
	case "message":
		payload := jsonMap(obj["message"])
		if role := jsonString(payload["role"]); role != "assistant" && role != "user" {
			return ""
		}
		return textBlocks(payload["content"])
	case "session_meta", "turn_context", "permission-mode", "progress":
		return ""
	default:
		return jsonString(obj["message"])
	}
}

// textBlocks pulls plain text out of a message "content" field, keeping text
// blocks and dropping tool_use / tool_result blocks. content may be a plain
// string or an array of typed blocks.
func textBlocks(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			block := jsonMap(item)
			if len(block) == 0 {
				continue
			}
			switch jsonString(block["type"]) {
			case "tool_use", "tool_result":
				continue
			default:
				if text := firstNonEmptyString(block["text"], block["input_text"], block["output_text"], block["content"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func jsonMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func jsonString(value any) string {
	s, _ := value.(string)
	return s
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		if text := jsonString(value); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
