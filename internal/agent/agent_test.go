package agent

import (
	"strings"
	"testing"
)

func TestCommandArgs(t *testing.T) {
	codex, err := CommandArgs("codex", nil, "PROMPT")
	if err != nil {
		t.Fatalf("codex: %v", err)
	}
	if codex[0] != "codex" || codex[1] != "exec" || codex[len(codex)-1] != "PROMPT" {
		t.Errorf("codex argv shape wrong: %v", codex)
	}
	if !contains(codex, "--sandbox") || !contains(codex, "read-only") {
		t.Errorf("codex argv missing read-only sandbox: %v", codex)
	}

	claude, err := CommandArgs("claude-code", nil, "PROMPT")
	if err != nil {
		t.Fatalf("claude-code: %v", err)
	}
	if claude[0] != "claude" || claude[len(claude)-1] != "PROMPT" {
		t.Errorf("claude argv shape wrong: %v", claude)
	}
	if !contains(claude, "--system-prompt") || !contains(claude, "--print") {
		t.Errorf("claude argv missing expected flags: %v", claude)
	}

	ollama, err := CommandArgs("ollama", nil, "PROMPT")
	if err != nil {
		t.Fatalf("ollama: %v", err)
	}
	if ollama[0] != "ollama" || ollama[1] != "" || ollama[2] != "PROMPT" {
		t.Errorf("ollama argv shape wrong: %v", ollama)
	}

	if _, err := CommandArgs("none", nil, "P"); err == nil {
		t.Errorf("none should error")
	}
	if _, err := CommandArgs("command", nil, "P"); err == nil {
		t.Errorf("command without --agent-command should error")
	}
	custom, err := CommandArgs("command", []string{"my-agent", "--flag"}, "P")
	if err != nil || custom[0] != "my-agent" {
		t.Errorf("command argv wrong: %v err=%v", custom, err)
	}
}

func TestInjectModelAndEffort(t *testing.T) {
	codex, _ := CommandArgs("codex", nil, "PROMPT")
	withModel := InjectModel(codex, "codex", "gpt-x")
	if !adjacent(withModel, "--model", "gpt-x") {
		t.Errorf("codex --model not injected: %v", withModel)
	}
	withEffort := InjectEffort(withModel, "codex", "low")
	if !contains(withEffort, "--config") || !contains(withEffort, "model_reasoning_effort=low") {
		t.Errorf("codex effort not injected: %v", withEffort)
	}
	if withEffort[len(withEffort)-1] != "PROMPT" {
		t.Errorf("prompt must remain last: %v", withEffort)
	}

	claude, _ := CommandArgs("claude-code", nil, "PROMPT")
	cm := InjectModel(claude, "claude-code", "opus")
	if !adjacent(cm, "--model", "opus") {
		t.Errorf("claude --model not injected: %v", cm)
	}
	ce := InjectEffort(cm, "claude-code", "high")
	if !adjacent(ce, "--effort", "high") {
		t.Errorf("claude --effort not injected: %v", ce)
	}

	ollama, _ := CommandArgs("ollama", nil, "PROMPT")
	om := InjectModel(ollama, "ollama", "llama3")
	if om[1] != "llama3" {
		t.Errorf("ollama model not set into argv[1]: %v", om)
	}

	// Empty model/effort is a no-op.
	if got := InjectModel(codex, "codex", ""); len(got) != len(codex) {
		t.Errorf("empty model should be a no-op")
	}
}

func TestRejectForNoEgress(t *testing.T) {
	t.Setenv("ENTIRE_BRAIN_NO_EGRESS", "1")
	for _, a := range []string{"codex", "claude-code", "command", "auto"} {
		if err := RejectForNoEgress(a); err == nil {
			t.Errorf("%s should be rejected under no-egress", a)
		}
	}
	if err := RejectForNoEgress("ollama"); err != nil {
		t.Errorf("ollama should be allowed under no-egress: %v", err)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func adjacent(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestCommandArgsRejectsUnsupported(t *testing.T) {
	if _, err := CommandArgs("frobnicate", nil, "P"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported agent error, got %v", err)
	}
}
