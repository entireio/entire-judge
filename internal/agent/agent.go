// Package agent builds the argv that runs an LLM "judge lens" agent and supplies
// the no-egress-aware exec runner that drives it. The agent's system prompt is
// the lens template; the assembled brief is delivered on stdin.
package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// MaxOutputBytes caps an agent's captured stdout. A run that exceeds it is an
// error: the lens JSON may have been cut mid-stream.
const MaxOutputBytes = 256 * 1024

// NoEgressMode reports whether the brain's local-only mode is enabled via env.
func NoEgressMode() bool {
	return envBool("ENTIRE_BRAIN_NO_EGRESS") || envBool("ENTIRE_BRAIN_LOCAL_ONLY")
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// RejectForNoEgress returns an error when no-egress mode is on and the requested
// agent can send brain context outside the local loopback.
func RejectForNoEgress(agent string) error {
	if !NoEgressMode() {
		return nil
	}
	switch agent {
	case "codex", "claude-code", "command", "auto":
		return fmt.Errorf("no_egress: --agent %s can send selected brain context outside local loopback; use --agent ollama for local loopback models, --agent none where supported, or unset ENTIRE_BRAIN_NO_EGRESS/ENTIRE_BRAIN_LOCAL_ONLY", agent)
	default:
		return nil
	}
}

// CommandArgs builds the argv that runs a lens agent with the rendered prompt as
// its system prompt and the brief on stdin. It honors no-egress.
func CommandArgs(agent string, agentCommand []string, prompt string) ([]string, error) {
	if err := RejectForNoEgress(agent); err != nil {
		return nil, err
	}
	switch agent {
	case "command":
		if len(agentCommand) == 0 {
			return nil, errors.New("--agent-command is required when --agent command")
		}
		return append([]string(nil), agentCommand...), nil
	case "codex":
		return []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--sandbox", "read-only", prompt}, nil
	case "claude-code":
		return []string{"claude", "--print", "--no-session-persistence", "--setting-sources", "user", "--strict-mcp-config", "--mcp-config", "{\"mcpServers\":{}}", "--disable-slash-commands", "--permission-mode", "dontAsk", "--tools", "", "--system-prompt", prompt}, nil
	case "ollama":
		return []string{"ollama", "", prompt}, nil
	case "none", "":
		return nil, errors.New("a lens requires an agent; --agent none has nothing to run")
	default:
		return nil, fmt.Errorf("unsupported --agent %q", agent)
	}
}

// InjectModel pins a specific model into a codex/claude-code/ollama argv. It is a
// no-op for an empty model or the `command` agent. The flag is inserted early,
// ahead of the trailing prompt argument.
func InjectModel(args []string, agent, model string) []string {
	if model == "" {
		return args
	}
	switch agent {
	case "codex":
		if len(args) >= 2 && args[0] == "codex" && args[1] == "exec" {
			return append(args[:2:2], append([]string{"--model", model}, args[2:]...)...)
		}
	case "claude-code":
		if len(args) >= 1 && args[0] == "claude" {
			return append(args[:1:1], append([]string{"--model", model}, args[1:]...)...)
		}
	case "ollama":
		if len(args) >= 3 && args[0] == "ollama" {
			out := append([]string(nil), args...)
			out[1] = model
			return out
		}
	}
	return args
}

// InjectEffort pins the reasoning effort for a codex/claude-code argv — codex via
// `--config model_reasoning_effort=<e>`, claude-code via `--effort <e>`. It is a
// no-op for an empty effort, the `command` agent, or an unrecognized shape.
func InjectEffort(args []string, agent, effort string) []string {
	if effort == "" {
		return args
	}
	switch agent {
	case "codex":
		if len(args) >= 2 && args[0] == "codex" && args[1] == "exec" {
			return append(args[:2:2], append([]string{"--config", "model_reasoning_effort=" + effort}, args[2:]...)...)
		}
	case "claude-code":
		if len(args) >= 1 && args[0] == "claude" {
			return append(args[:1:1], append([]string{"--effort", effort}, args[1:]...)...)
		}
	}
	return args
}
