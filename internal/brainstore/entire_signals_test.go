package brainstore

import (
	"testing"
)

func TestExtractEntireSignals_SkillTool(t *testing.T) {
	raw := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Skill","input":{"skill":"entire"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Skill","input":{"skill":"audit"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_3","name":"Skill","input":{"skill":"skill:entire-graph"}}]}}`
	got := ExtractEntireSignals(raw)
	if len(got) != 2 {
		t.Fatalf("signals = %#v, want 2 Entire skills (audit ignored)", got)
	}
	if got[0].Kind != EntireSignalSkill || got[0].Capability != CapabilitySkill || got[0].Detail != "entire" {
		t.Errorf("signal0 = %#v", got[0])
	}
	if got[0].ToolUseID != "toolu_1" || got[0].Line != 1 {
		t.Errorf("signal0 anchors = line %d id %q", got[0].Line, got[0].ToolUseID)
	}
	if got[1].Capability != CapabilityGraph || got[1].Detail != "entire-graph" {
		t.Errorf("signal1 = %#v, want entire-graph → graph", got[1])
	}
}

func TestExtractEntireSignals_BashCLI(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"entire graph search --repo . --query 'auth'"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b2","name":"Shell","input":{"command":"cd /tmp && entire brain refresh sessions"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b3","name":"Bash","input":{"command":"echo hello && ls"}}]}}`
	got := ExtractEntireSignals(raw)
	if len(got) != 2 {
		t.Fatalf("signals = %#v, want 2 CLI invocations", got)
	}
	if got[0].Kind != EntireSignalCLI || got[0].Capability != CapabilityGraph {
		t.Errorf("signal0 = %#v", got[0])
	}
	if got[1].Capability != CapabilityBrain {
		t.Errorf("signal1 = %#v, want brain", got[1])
	}
}

func TestExtractEntireSignals_MCPTools(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"m1","name":"mcp__plugin-entire-graph__search","input":{"query":"x"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"m2","name":"Read","input":{"path":"foo.go"}}]}}`
	got := ExtractEntireSignals(raw)
	if len(got) != 1 {
		t.Fatalf("signals = %#v, want 1 MCP hit", got)
	}
	if got[0].Kind != EntireSignalMCP || got[0].Capability != CapabilityGraph {
		t.Errorf("signal = %#v", got[0])
	}
}

func TestExtractEntireSignals_SlashPrompt(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":"/entire how do I search the graph?"}}
{"type":"user","message":{"role":"user","content":"/skill:entire-sem doctor"}}
{"type":"user","message":{"role":"user","content":"/Users/me/project run tests"}}
{"type":"user","message":{"role":"user","content":"please use entire somehow"}}`
	got := ExtractEntireSignals(raw)
	if len(got) != 2 {
		t.Fatalf("signals = %#v, want 2 slash Entire skills", got)
	}
	if got[0].Kind != EntireSignalSkill || got[0].Capability != CapabilitySkill {
		t.Errorf("signal0 = %#v", got[0])
	}
	if got[1].Capability != CapabilitySem {
		t.Errorf("signal1 = %#v, want sem", got[1])
	}
}

func TestExtractEntireSignals_PipedCLI(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"entire sem doctor --json | jq .provider"}}]}}`
	got := ExtractEntireSignals(raw)
	if len(got) != 1 || got[0].Capability != CapabilitySem {
		t.Fatalf("signals = %#v, want sem CLI", got)
	}
}
