package judge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
)

func TestCLIAwarenessLensRubric(t *testing.T) {
	cases := []struct {
		name string
		m    Metrics
		min  float64
		max  float64
	}{
		{"none", Metrics{}, 0, 0},
		{"single skill", Metrics{EntireSkillInvocations: 1, EntireSignalSessions: 1, DistinctEntireCapabilities: []string{"skill"}}, 1.5, 1.5},
		{"multi session", Metrics{EntireCLIInvocations: 2, EntireSignalSessions: 2, DistinctEntireCapabilities: []string{"graph"}}, 3.0, 3.0},
		{"multi capability sessions", Metrics{EntireCLIInvocations: 3, EntireSignalSessions: 2, DistinctEntireCapabilities: []string{"brain", "graph"}}, 4.5, 4.5},
		{"skill plus multi cap", Metrics{
			EntireSkillInvocations: 1, EntireCLIInvocations: 2, EntireSignalSessions: 2,
			DistinctEntireCapabilities: []string{"graph", "sem", "skill"},
		}, 5.0, 5.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := cliAwarenessLens(tc.m)
			if res.Lens != LensCLIAwareness || !res.Supported || res.Score == nil {
				t.Fatalf("lens = %#v", res)
			}
			if *res.Score < tc.min || *res.Score > tc.max {
				t.Errorf("score = %v, want in [%v,%v]", *res.Score, tc.min, tc.max)
			}
		})
	}
}

func TestCollectEntireUsageFromTranscripts(t *testing.T) {
	brainDir := t.TempDir()
	sessDir := filepath.Join(brainDir, "sessions", "main")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Skill","input":{"skill":"entire"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"entire graph search --repo ."}}]}}`
	if err := os.WriteFile(filepath.Join(sessDir, "s1.jsonl"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := []brainstore.Session{{
		SessionID: "s1", CreatedAt: time.Now(), TranscriptPath: "sessions/main/s1.jsonl",
	}}
	var m Metrics
	collectEntireUsage(brainDir, sessions, &m)
	if m.EntireSkillInvocations != 1 || m.EntireCLIInvocations != 1 {
		t.Fatalf("invocations skill=%d cli=%d, want 1/1", m.EntireSkillInvocations, m.EntireCLIInvocations)
	}
	if m.EntireSignalSessions != 1 {
		t.Fatalf("sessions = %d, want 1", m.EntireSignalSessions)
	}
	if len(m.DistinctEntireCapabilities) < 2 {
		t.Fatalf("capabilities = %v, want skill+graph", m.DistinctEntireCapabilities)
	}
	res := cliAwarenessLens(m)
	if res.Score == nil || *res.Score < 1.5 {
		t.Errorf("score = %v, want >= 1.5", res.Score)
	}
}
