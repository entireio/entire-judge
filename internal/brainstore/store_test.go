package brainstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeManifest(t *testing.T, m Manifest) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSessionListPrefersStructuredSource(t *testing.T) {
	m := Manifest{
		Sessions: []Session{{SessionID: "flat"}},
		Sources:  &Sources{Sessions: &SessionSource{Sessions: []Session{{SessionID: "structured"}}}},
	}
	got := m.SessionList()
	if len(got) != 1 || got[0].SessionID != "structured" {
		t.Errorf("SessionList = %v, want structured source", got)
	}
}

func TestOldestSessionAtFallsBackToCreatedAt(t *testing.T) {
	early := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	late := early.Add(2 * time.Hour)
	m := Manifest{Sources: &Sources{Sessions: &SessionSource{Sessions: []Session{
		{SessionID: "b", CreatedAt: late},
		{SessionID: "a", CreatedAt: early},
	}}}}
	got := m.OldestSessionAt()
	if got == nil || !got.Equal(early) {
		t.Errorf("OldestSessionAt = %v, want %v", got, early)
	}
}

func TestExportedCheckpointIDs(t *testing.T) {
	m := Manifest{Sources: &Sources{Sessions: &SessionSource{Sessions: []Session{
		{SessionID: "s1", LatestCheckpoint: "abc"},
		{SessionID: "s2", LatestCheckpoint: ""},
		{SessionID: "s3", LatestCheckpoint: "def"},
	}}}}
	ids := m.ExportedCheckpointIDs()
	if len(ids) != 2 {
		t.Errorf("checkpoint ids = %v, want 2", ids)
	}
}

func TestLoadManifestMissingIsEmpty(t *testing.T) {
	m, err := LoadManifest(t.TempDir())
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m == nil || len(m.SessionList()) != 0 {
		t.Errorf("missing manifest should yield empty session list")
	}
}

func TestLoadFactsMissingIsEmpty(t *testing.T) {
	facts, err := LoadFacts(t.TempDir(), "main")
	if err != nil {
		t.Fatalf("LoadFacts: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("missing facts dir should yield zero facts, got %d", len(facts))
	}
}

func TestLoadFactsSimpleLayout(t *testing.T) {
	dir := writeManifest(t, Manifest{SchemaVersion: SchemaVersion})
	factsPath := filepath.Join(dir, factsDirName, "main", factsFileName)
	if err := os.MkdirAll(filepath.Dir(factsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"f1","paths":["arch/decision"],"kind":"decision","text":"the team chose go","provenance":[{"session_id":"s1"}]}` + "\n"
	if err := os.WriteFile(factsPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	facts, err := LoadFacts(dir, "main")
	if err != nil {
		t.Fatalf("LoadFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].Kind != "decision" {
		t.Errorf("facts = %v, want one decision fact", facts)
	}
}

func TestExtractHumanPromptsJSONL(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":"first ask"}}
{"type":"assistant","message":{"role":"assistant","content":"reply"}}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"second ask"},{"type":"tool_use","name":"x"}]}}`
	prompts := ExtractHumanPrompts(raw)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %v, want 2 human turns", prompts)
	}
	if prompts[0] != "first ask" || prompts[1] != "second ask" {
		t.Errorf("prompts = %v", prompts)
	}
}

func TestExtractConversationTurns(t *testing.T) {
	// Mixed shapes: claude assistant (content blocks with a tool_use dropped),
	// codex response_item assistant, pi message user, and a bare-role user.
	raw := `{"type":"user","message":{"role":"user","content":"do the thing"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"I warn you: this is contaminated"},{"type":"tool_use","name":"x"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"proceeding"}]}}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"ok proceed"}]}}
{"role":"user","content":"bare role turn"}
{"type":"event_msg","payload":{"type":"reasoning","text":"drop me"}}`
	turns := ExtractConversationTurns(raw)
	want := []Turn{
		{Role: "user", Text: "do the thing"},
		{Role: "assistant", Text: "I warn you: this is contaminated"},
		{Role: "assistant", Text: "proceeding"},
		{Role: "user", Text: "ok proceed"},
	}
	// The bare-role user turn has no text via conversationText's default branch
	// (obj["message"] is absent), so it is dropped; reasoning is dropped too.
	if len(turns) != len(want) {
		t.Fatalf("turns = %v, want %d attributed turns", turns, len(want))
	}
	for i, w := range want {
		if turns[i].Role != w.Role || turns[i].Text != w.Text {
			t.Errorf("turn %d = %+v, want %+v", i, turns[i], w)
		}
	}
}

func TestExtractHumanPromptsPlainTextFallback(t *testing.T) {
	raw := "just some notes\nmore notes"
	prompts := ExtractHumanPrompts(raw)
	if len(prompts) != 2 {
		t.Errorf("plain-text fallback should keep non-empty lines, got %v", prompts)
	}
}

func TestExists(t *testing.T) {
	dir := writeManifest(t, Manifest{SchemaVersion: SchemaVersion})
	if !Exists(dir) {
		t.Errorf("Exists should be true for a dir with manifest.json")
	}
	if Exists(t.TempDir()) {
		t.Errorf("Exists should be false for an empty dir")
	}
}
