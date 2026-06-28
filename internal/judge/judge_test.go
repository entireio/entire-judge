package judge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suhaanthayyil/entire-judge/internal/agent"
	"github.com/suhaanthayyil/entire-judge/internal/brainstore"
	"github.com/suhaanthayyil/entire-judge/internal/gitutil"
)

// fakeRunner is a gitutil.CommandRunner that replays a canned `git log` reply for
// the seed-coverage call. It lets metric/timeline tests run with no real repo.
type fakeRunner struct {
	gitLog string
}

func (f fakeRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	if name == "git" && len(args) >= 2 && args[0] == "log" {
		return []byte(f.gitLog), nil, nil
	}
	return nil, nil, errNoFakeResponse
}

var errNoFakeResponse = &fakeError{"no fake response"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

// gitLogRecord builds a fake `git log` record matching ParseGitLog's format:
// %H%x00%P%x00%aI%x00%an%x00%ae%x00%B, record-separated by %x1e.
func gitLogRecord(hash, parents, committedAt, author, email, body string) string {
	const fieldSep = "\x00"
	const recordSep = "\x1e"
	return strings.Join([]string{hash, parents, committedAt, author, email, body}, fieldSep) + recordSep
}

// writeBrainFixture lays down a brain dir with a session manifest and a transcript
// per session.
func writeBrainFixture(t *testing.T, now time.Time, sessions []brainstore.Session, transcripts map[string]string) string {
	t.Helper()
	brainDir := t.TempDir()
	for _, s := range sessions {
		if s.TranscriptPath == "" {
			continue
		}
		p := filepath.Join(brainDir, filepath.FromSlash(s.TranscriptPath))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(transcripts[s.SessionID]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := brainstore.Manifest{
		SchemaVersion: brainstore.SchemaVersion,
		GeneratedAt:   now,
		DefaultBranch: "main",
		Sources: &brainstore.Sources{
			Sessions: &brainstore.SessionSource{GeneratedAt: now, DefaultBranch: "main", Sessions: sessions},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brainDir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return brainDir
}

func TestComputeMetrics(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	start := now.Add(-3 * time.Hour)
	sessions := []brainstore.Session{
		{
			SessionID: "s1", Branch: "main", Agent: "claude-code",
			CreatedAt:        now.Add(-2 * time.Hour),
			CheckpointsCount: 3,
			FilesTouched:     []string{"a.go", "b.go"},
			TokenUsage:       &brainstore.TokenUsage{InputTokens: 100, OutputTokens: 50},
			TranscriptPath:   "sessions/main/s1.jsonl",
		},
		{
			SessionID: "s2", Branch: "main", Agent: "codex",
			CreatedAt:        now.Add(-1 * time.Hour),
			CheckpointsCount: 2,
			FilesTouched:     []string{"b.go", "c.go"},
			TokenUsage:       &brainstore.TokenUsage{InputTokens: 200, OutputTokens: 80},
			TranscriptPath:   "sessions/main/s2.jsonl",
		},
	}
	transcripts := map[string]string{
		"s1": `{"type":"user","message":{"role":"user","content":"build a parser"}}`,
		"s2": `{"type":"user","message":{"role":"user","content":"add tests"}}`,
	}
	brainDir := writeBrainFixture(t, now, sessions, transcripts)

	gitLog := gitLogRecord("aaaa111", "", now.Add(-90*time.Minute).Format(time.RFC3339), "dev", "dev@example.com", "first commit")
	runner := fakeRunner{gitLog: gitLog}

	manifest, err := brainstore.LoadManifest(brainDir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	metrics, _ := computeMetrics(context.Background(), runner, t.TempDir(), manifest, brainDir, start)

	if metrics.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", metrics.Sessions)
	}
	if metrics.Turns != 5 {
		t.Errorf("turns = %d, want 5", metrics.Turns)
	}
	if metrics.FilesTouched != 3 {
		t.Errorf("files touched = %d, want 3 (union)", metrics.FilesTouched)
	}
	if metrics.InputTokens != 300 || metrics.OutputTokens != 130 {
		t.Errorf("token totals wrong: in=%d out=%d", metrics.InputTokens, metrics.OutputTokens)
	}
	if metrics.PrimaryAgent != "claude-code" {
		t.Errorf("primary agent = %q, want claude-code (tie broken alphabetically)", metrics.PrimaryAgent)
	}
	if metrics.AgentHistogram["claude-code"] != 1 || metrics.AgentHistogram["codex"] != 1 {
		t.Errorf("agent histogram wrong: %v", metrics.AgentHistogram)
	}
	if metrics.TimeOnTaskMinutes != 60 {
		t.Errorf("time on task = %.0f, want 60", metrics.TimeOnTaskMinutes)
	}
	if metrics.TimelineCategory != TimelineCleanStart {
		t.Errorf("timeline = %q, want clean_start", metrics.TimelineCategory)
	}
}

func TestClassifyTimeline(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	before := now.Add(-5 * time.Hour)
	after := now.Add(-1 * time.Hour)

	cases := []struct {
		name    string
		metrics Metrics
		start   time.Time
		want    string
	}{
		{"no sessions", Metrics{Sessions: 0}, start, TimelineInsufficientBrain},
		{"no event boundary", Metrics{Sessions: 2, OldestSessionAt: &after, LastSessionAt: &after}, time.Time{}, TimelineInsufficientBrain},
		{"predates", Metrics{Sessions: 2, OldestSessionAt: &before, LastSessionAt: &before}, start, TimelinePredatesEvent},
		{"mixed", Metrics{Sessions: 2, OldestSessionAt: &before, LastSessionAt: &after}, start, TimelineMixed},
		{"clean start", Metrics{Sessions: 2, OldestSessionAt: &after, LastSessionAt: &after}, start, TimelineCleanStart},
		{"no session history", Metrics{Sessions: 1, TotalCommits: 5, NoSessionHistory: 5}, start, TimelineNoSessionHistory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := classifyTimeline(tc.metrics, tc.start)
			if got != tc.want {
				t.Errorf("classifyTimeline = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseLensOutputLenient(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantNil    bool
		wantBullet int
		wantWarn   bool
	}{
		{name: "clean json", input: `{"score":4,"verdict":"good","bullets":["a","b"],"evidence":["s1"]}`, wantBullet: 2},
		{name: "fenced json", input: "```json\n{\"score\":3,\"verdict\":\"ok\",\"bullets\":[\"x\"],\"evidence\":[]}\n```", wantBullet: 1},
		{name: "prose wrapped", input: "Here is my verdict:\n{\"score\":5,\"verdict\":\"great\",\"bullets\":[\"y\",\"y\"],\"evidence\":[\"abc\"]}\nThanks!", wantBullet: 1},
		{name: "garbage", input: "I could not produce JSON, sorry.", wantNil: true, wantWarn: true},
		{name: "score clamped above 5", input: `{"score":9,"verdict":"v","bullets":[],"evidence":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseLensOutput(tc.input)
			if tc.wantNil && result.Score != nil {
				t.Errorf("expected nil score, got %v", *result.Score)
			}
			if tc.wantWarn && len(result.Warnings) == 0 {
				t.Errorf("expected a warning")
			}
			if tc.wantBullet > 0 && len(result.Bullets) != tc.wantBullet {
				t.Errorf("bullets = %d, want %d", len(result.Bullets), tc.wantBullet)
			}
			if tc.name == "score clamped above 5" {
				if result.Score == nil || *result.Score != 5 {
					t.Errorf("score not clamped to 5: %v", result.Score)
				}
			}
		})
	}
}

func TestValidateEvidenceAnchors(t *testing.T) {
	sc := submissionContext{
		Sessions: []brainstore.Session{{SessionID: "sess-abc"}, {SessionID: "sess-def"}},
		Coverage: &gitutil.HistoryCoverage{
			UncoveredCommits: []gitutil.Commit{{Hash: "abc1234deadbeef"}},
		},
	}
	evidence := []string{
		"sess-abc",                      // exact session id
		"commit abc1234: fixed the bug", // commit short-hash prefix in prose
		"sess-ghost",                    // unknown session
		"totally made up reference",     // unresolvable
	}
	kept, dropped := validateEvidenceAnchors(sc, evidence)
	if len(kept) != 2 {
		t.Errorf("kept = %v, want 2 resolvable anchors", kept)
	}
	if len(dropped) != 2 {
		t.Errorf("dropped = %v, want 2 unresolvable anchors", dropped)
	}
}

func TestRunLensWithFakeRunner(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 2, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	transcripts := map[string]string{
		"s1": `{"type":"user","message":{"role":"user","content":"please implement the feature"}}`,
	}
	brainDir := writeBrainFixture(t, now, sessions, transcripts)
	runner := fakeRunner{gitLog: gitLogRecord("c0ffee1234567", "", now.Add(-90*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	sc, err := assembleSubmissionContext(context.Background(), runner, t.TempDir(), brainDir, "gh/team/proj", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("assembleSubmissionContext: %v", err)
	}
	if !strings.Contains(sc.Brief, "please implement the feature") {
		t.Errorf("brief missing human prompt excerpt:\n%s", sc.Brief)
	}
	if !strings.Contains(sc.Brief, "Deterministic timeline") {
		t.Errorf("brief missing timeline summary")
	}

	canned := `{"score":4,"verdict":"clear prompting","bullets":["The human gave a concrete goal"],"evidence":["s1"]}`
	var fakeRun agent.Runner = func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
		if len(input) == 0 {
			t.Errorf("lens runner received empty stdin")
		}
		return canned, nil
	}
	result, err := runLens(context.Background(), sc, LensPrompting, templatePrompting, "claude-code", "", "", fakeRun, time.Minute)
	if err != nil {
		t.Fatalf("runLens: %v", err)
	}
	if result.Score == nil || *result.Score != 4 {
		t.Fatalf("score = %v, want 4", result.Score)
	}
	if result.Verdict != "clear prompting" {
		t.Errorf("verdict = %q", result.Verdict)
	}
	validateLensEvidence(&result, sc)
	if !result.Supported {
		t.Errorf("expected supported lens (s1 resolves); evidence=%v", result.Evidence)
	}
}

func TestAuthenticityLensDeterministic(t *testing.T) {
	cases := []struct {
		category string
		min, max float64
	}{
		{TimelineCleanStart, 4, 5},
		{TimelinePredatesEvent, 0, 2},
		{TimelineMixed, 2.5, 3.5},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			res := authenticityLens(Metrics{TimelineCategory: tc.category})
			if res.Score == nil {
				t.Fatal("nil score")
			}
			if *res.Score < tc.min || *res.Score > tc.max {
				t.Errorf("score %.1f outside [%.1f,%.1f] for %s", *res.Score, tc.min, tc.max, tc.category)
			}
		})
	}
}

func TestCompositeExcludesUnsupportedLLMLens(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	// prompting_skill returned a score but no evidence anchor resolved
	// (Supported == false) — e.g. an agent whose sessions expose no prompt text.
	// It must be excluded from the composite and flagged, not folded in at 1/5.
	lenses := []LensResult{
		{Lens: LensAuthenticity, Score: score(4.5), Supported: true},
		{Lens: LensPrompting, Score: score(1), Supported: false},
		{Lens: LensOutcome, Score: score(5), Supported: true},
		{Lens: LensEffort, Score: score(5), Supported: true},
		{Lens: LensAgent, Supported: true},
	}
	composite, flags := compositeAndFlags(lenses, Metrics{TimelineCategory: TimelineCleanStart})
	if composite == nil {
		t.Fatal("composite nil; deterministic lenses keep it computable")
	}
	// Renormalized over authenticity (.30) + outcome (.30) + effort (.20); the
	// unsupported prompting lens (.20) is dropped from both sum and weight.
	want := (4.5*weightAuthenticity + 5*weightOutcome + 5*weightEffort) /
		(weightAuthenticity + weightOutcome + weightEffort)
	if diff := *composite - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("composite = %.4f, want %.4f (unsupported prompting must be excluded)", *composite, want)
	}
	hasFlag := func(f string) bool {
		for _, g := range flags {
			if g == f {
				return true
			}
		}
		return false
	}
	if !hasFlag("prompting_skill_unsupported") {
		t.Errorf("flags %v missing prompting_skill_unsupported", flags)
	}
	if hasFlag("prompting_skill_unscored") {
		t.Errorf("flags %v: scored-but-unsupported lens must not be labeled unscored", flags)
	}
}

func TestSemanticSummarySection(t *testing.T) {
	s := &brainstore.SemanticSummary{
		Symbols:      12,
		Relations:    5,
		Files:        3,
		Capabilities: []string{"go", "routes"},
		ByKind:       []brainstore.LabelCount{{Label: "function", Count: 8}, {Label: "struct", Count: 4}},
		ByLanguage:   []brainstore.LabelCount{{Label: "go", Count: 12}},
		TopFiles:     []brainstore.LabelCount{{Label: "api/routes.go", Count: 7}},
	}
	out := semanticSummarySection(s)
	for _, want := range []string{"What was built", "12 symbols", "function(8)", "routes", "api/routes.go(7)"} {
		if !strings.Contains(out, want) {
			t.Errorf("section missing %q:\n%s", want, out)
		}
	}
	if semanticSummarySection(nil) != "" {
		t.Error("nil summary should yield an empty section")
	}
	if semanticSummarySection(&brainstore.SemanticSummary{Symbols: 0}) != "" {
		t.Error("zero-symbol summary should yield an empty section")
	}
}

func TestRunSummarySuccess(t *testing.T) {
	sc := submissionContext{RepoDir: t.TempDir(), Brief: "the brief", Metrics: Metrics{TimelineCategory: TimelineCleanStart}}
	var run agent.Runner = func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
		return "```json\n{\"summary\":\"a short overview\"}\n```", nil
	}
	got, err := runSummary(context.Background(), sc, Params{Agent: "claude-code"}, run)
	if err != nil {
		t.Fatalf("runSummary: %v", err)
	}
	if got != "a short overview" {
		t.Errorf("summary = %q, want %q", got, "a short overview")
	}
}

func TestParseSummaryOutput(t *testing.T) {
	cases := map[string]string{
		`{"summary":"A clean build."}`:                        "A clean build.",
		"```json\n{\"summary\":\"Fenced  output\"}\n```":      "Fenced output",
		"chatter before {\"summary\":\"mid text\"} and after": "mid text",
		"no json here": "",
		`{"nope":"x"}`: "",
	}
	for in, want := range cases {
		if got := parseSummaryOutput(in); got != want {
			t.Errorf("parseSummaryOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeSummaryAndSummaryText(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	report := &RunReport{
		Deterministic: Metrics{
			TimelineCategory:  TimelineCleanStart,
			Sessions:          14,
			FilesTouched:      127,
			TimeOnTaskMinutes: 433,
			PrimaryAgent:      "Codex",
		},
		Lenses: []LensResult{
			{Lens: LensOutcome, Score: score(5), Verdict: "Coherent maritime-intelligence platform carried from data to demo."},
		},
	}
	composed := ComposeSummary(report)
	for _, want := range []string{"Clean start", "maritime-intelligence", "14 sessions", "127 files", "Codex"} {
		if !strings.Contains(composed, want) {
			t.Errorf("ComposeSummary missing %q:\n%s", want, composed)
		}
	}
	// SummaryText falls back to the composed summary when no LLM summary is set.
	if SummaryText(report) != composed {
		t.Errorf("SummaryText should equal composed summary when report.Summary empty")
	}
	// ...and prefers the LLM summary when present.
	report.Summary = "An LLM-written overview."
	if SummaryText(report) != "An LLM-written overview." {
		t.Errorf("SummaryText should return the LLM summary when set")
	}
}

func TestSubmitPopulatesFallbackSummaryWhenSummaryAgentFails(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	brainDir := writeBrainFixture(t, now, nil, nil)
	runner := fakeRunner{gitLog: gitLogRecord("deadbeef00000", "", now.Add(-80*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	var failing agent.Runner = func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
		return "", errNoFakeResponse
	}
	report, err := Submit(context.Background(), runner, failing, t.TempDir(), brainDir, "gh/team/proj", Params{
		Agent:          "claude-code",
		HackathonStart: now.Add(-2 * time.Hour),
	}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if strings.TrimSpace(report.Summary) == "" {
		t.Fatal("summary should be populated with deterministic fallback")
	}
	if !strings.Contains(report.Summary, "Insufficient brain") {
		t.Fatalf("summary = %q, want insufficient-brain fallback", report.Summary)
	}
}

func TestSubmissionIDFromKey(t *testing.T) {
	cases := map[string]string{
		"github.com/team/project": "gh/team/project",
		"team/project":            "gh/team/project",
		"project":                 "gh/unknown/project",
		"":                        "gh/unknown/unknown",
	}
	for key, want := range cases {
		if got := SubmissionIDFromKey(key); got != want {
			t.Errorf("SubmissionIDFromKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestTemplatesLoad(t *testing.T) {
	for _, name := range []string{templateAuthenticity, templatePrompting, templateOutcome, templateSummary} {
		body, err := loadTemplate(name)
		if err != nil {
			t.Fatalf("loadTemplate(%q): %v", name, err)
		}
		if strings.HasPrefix(body, "-") {
			t.Errorf("template %q body starts with dash (frontmatter not stripped)", name)
		}
		if !strings.Contains(body, "JSON object") {
			t.Errorf("template %q missing strict JSON output instruction", name)
		}
	}
}

func TestSubmitDeterministicPopulatesAndDegradesLLM(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "codex", CreatedAt: now.Add(-90 * time.Minute), CheckpointsCount: 6, FilesTouched: []string{"a.go"}, TranscriptPath: "sessions/main/s1.jsonl"},
		{SessionID: "s2", Branch: "main", Agent: "codex", CreatedAt: now.Add(-30 * time.Minute), CheckpointsCount: 4, FilesTouched: []string{"b.go"}, TranscriptPath: "sessions/main/s2.jsonl"},
	}
	transcripts := map[string]string{
		"s1": `{"type":"user","message":{"role":"user","content":"design the schema"}}`,
		"s2": `{"type":"user","message":{"role":"user","content":"write the tests"}}`,
	}
	brainDir := writeBrainFixture(t, now, sessions, transcripts)
	runner := fakeRunner{gitLog: gitLogRecord("deadbeef00000", "", now.Add(-80*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	// A failing agent runner forces the LLM lenses to degrade gracefully.
	var failing agent.Runner = func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
		return "", &fakeError{"agent unavailable"}
	}
	params := Params{Agent: "codex", HackathonStart: now.Add(-2 * time.Hour)}
	report, err := Submit(context.Background(), runner, failing, t.TempDir(), brainDir, "gh/team/proj", params, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if report.Deterministic.Sessions != 2 {
		t.Errorf("deterministic sessions = %d, want 2", report.Deterministic.Sessions)
	}
	if report.Deterministic.TimelineCategory != TimelineCleanStart {
		t.Errorf("timeline = %q, want clean_start", report.Deterministic.TimelineCategory)
	}
	if !strings.HasPrefix(report.Run.LLMStatus, "degraded") {
		t.Errorf("LLM status = %q, want degraded", report.Run.LLMStatus)
	}
	// Composite is computable from the always-present deterministic lenses.
	if report.Composite == nil {
		t.Errorf("composite nil; deterministic lenses should keep it computable")
	}
}
