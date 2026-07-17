package judge

import (
	"context"
	"encoding/json"
	"fmt"
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
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name       string
		lens       string
		input      string
		wantNil    bool
		wantScore  *float64
		wantComps  int
		wantBullet int
		wantWarn   bool
	}{
		// single-score lenses
		{name: "clean json", lens: LensPrompting, input: `{"score":4,"verdict":"good","bullets":["a","b"],"evidence":["s1"]}`, wantScore: f(4), wantBullet: 2},
		{name: "fenced json", lens: LensPrompting, input: "```json\n{\"score\":3,\"verdict\":\"ok\",\"bullets\":[\"x\"],\"evidence\":[]}\n```", wantScore: f(3), wantBullet: 1},
		{name: "prose wrapped", lens: LensPrompting, input: "Here is my verdict:\n{\"score\":5,\"verdict\":\"great\",\"bullets\":[\"y\",\"y\"],\"evidence\":[\"abc\"]}\nThanks!", wantScore: f(5), wantBullet: 1},
		{name: "garbage", lens: LensPrompting, input: "I could not produce JSON, sorry.", wantNil: true, wantWarn: true},
		{name: "score clamped above 5", lens: LensPrompting, input: `{"score":9,"verdict":"v","bullets":[],"evidence":[]}`, wantScore: f(5)},
		// outcome lens: idea/plan/execution averaged into the score
		{name: "outcome mean of three", lens: LensOutcome, input: `{"idea":5,"plan":4,"execution":3,"verdict":"v","bullets":[],"evidence":[]}`, wantScore: f(4), wantComps: 3},
		{name: "outcome fractional mean", lens: LensOutcome, input: `{"idea":5,"plan":4,"execution":4}`, wantScore: f(13.0 / 3.0), wantComps: 3},
		{name: "outcome clamp before mean", lens: LensOutcome, input: `{"idea":9,"plan":-2,"execution":3}`, wantScore: f(8.0 / 3.0), wantComps: 3},
		{name: "outcome partial subs", lens: LensOutcome, input: `{"idea":4,"execution":2}`, wantScore: f(3), wantComps: 2},
		{name: "outcome single sub", lens: LensOutcome, input: `{"idea":4}`, wantScore: f(4), wantComps: 1},
		{name: "outcome subs win over score", lens: LensOutcome, input: `{"score":1,"idea":5,"plan":5,"execution":5}`, wantScore: f(5), wantComps: 3},
		{name: "outcome no score no subs", lens: LensOutcome, input: `{"verdict":"x","bullets":["a"]}`, wantNil: true, wantBullet: 1, wantWarn: true},
		{name: "non-outcome ignores stray subs", lens: LensPrompting, input: `{"score":4,"idea":1,"plan":1,"execution":1}`, wantScore: f(4), wantComps: 0},
		// integrity lens: single-score shape (like prompting), LOW = bad
		{name: "integrity low score", lens: LensIntegrity, input: `{"score":1,"verdict":"assistant warned of test-set contamination; team proceeded","bullets":["a"],"evidence":["s1"]}`, wantScore: f(1), wantBullet: 1},
		{name: "integrity clean high score", lens: LensIntegrity, input: `{"score":5,"verdict":"no concerns","bullets":[],"evidence":["s1"]}`, wantScore: f(5)},
		{name: "integrity no score warns", lens: LensIntegrity, input: `{"verdict":"unclear","bullets":["a"]}`, wantNil: true, wantBullet: 1, wantWarn: true},
		{name: "integrity ignores stray subs", lens: LensIntegrity, input: `{"score":2,"idea":5,"plan":5,"execution":5}`, wantScore: f(2), wantComps: 0},
		// a non-scored lens (authenticity/effort/agent) must NOT get the no-score warning
		{name: "non-scored lens gets no warning", lens: LensAuthenticity, input: `{"verdict":"x","bullets":["a"]}`, wantNil: true, wantBullet: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseLensOutput(tc.input, tc.lens)
			if tc.wantNil && result.Score != nil {
				t.Errorf("expected nil score, got %v", *result.Score)
			}
			if !tc.wantNil && tc.wantScore != nil {
				if result.Score == nil {
					t.Fatalf("expected score %.4f, got nil", *tc.wantScore)
				}
				if diff := *result.Score - *tc.wantScore; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("score = %.4f, want %.4f", *result.Score, *tc.wantScore)
				}
			}
			if len(result.Components) != tc.wantComps {
				t.Errorf("components = %d, want %d", len(result.Components), tc.wantComps)
			}
			if tc.wantWarn && len(result.Warnings) == 0 {
				t.Errorf("expected a warning")
			}
			if !tc.wantWarn && len(result.Warnings) != 0 {
				t.Errorf("unexpected warning(s): %v", result.Warnings)
			}
			if tc.wantBullet > 0 && len(result.Bullets) != tc.wantBullet {
				t.Errorf("bullets = %d, want %d", len(result.Bullets), tc.wantBullet)
			}
			if tc.name == "outcome clamp before mean" {
				want := []float64{5, 0, 3}
				for i, c := range result.Components {
					if c.Score != want[i] {
						t.Errorf("component %s = %v, want %v (clamp must precede the mean)", c.Name, c.Score, want[i])
					}
				}
			}
		})
	}
}

func TestValidateEvidenceAnchors(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "docs", "plans"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "docs", "plans", "agent.md"), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := submissionContext{
		RepoDir: repoDir,
		Sessions: []brainstore.Session{
			{SessionID: "sess-abc", CreatedAt: time.Date(2026, 5, 28, 8, 19, 49, 0, time.UTC)},
			{SessionID: "sess-def"},
		},
		Coverage: &gitutil.HistoryCoverage{
			UncoveredCommits: []gitutil.Commit{{Hash: "abc1234deadbeef"}},
		},
	}
	evidence := []string{
		"sess-abc",                      // exact session id
		"session_sess-def",              // session id with model-added prefix
		"2026-05-28T08:19:49Z",          // exact session timestamp
		"commit abc1234: fixed the bug", // commit short-hash prefix in prose
		"docs/plans/agent.md",           // repo-relative file evidence
		"sess-ghost",                    // unknown session
		"totally made up reference",     // unresolvable
		"../outside.md",                 // unsafe path
	}
	kept, dropped := validateEvidenceAnchors(sc, evidence)
	if len(kept) != 5 {
		t.Errorf("kept = %v, want 5 resolvable anchors", kept)
	}
	if len(dropped) != 3 {
		t.Errorf("dropped = %v, want 3 unresolvable anchors", dropped)
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
	composite, flags := compositeAndFlags(lenses, Metrics{TimelineCategory: TimelineCleanStart}, false)
	if composite == nil {
		t.Fatal("composite nil; deterministic lenses keep it computable")
	}
	// Grade A (process) = mean of supported authenticity (4.5) and effort (5); the
	// unsupported prompting lens is dropped. Grade B (solution) = outcome (5).
	// Combined = mean(A, B).
	wantA := (4.5 + 5) / 2.0
	want := (wantA + 5) / 2.0
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
	for _, name := range []string{templateAuthenticity, templatePrompting, templateOutcome, templateIntegrity, templateSummary} {
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
	// Both LLM lenses degraded: Grade B (solution) is unscored, so Combined falls
	// back to Grade A (process) alone.
	if report.GradeSolution != nil {
		t.Errorf("grade_solution = %v, want nil (outcome lens degraded)", *report.GradeSolution)
	}
	if report.GradeProcess == nil {
		t.Fatal("grade_process nil; the deterministic process lenses should set it")
	}
	if report.Composite == nil || *report.Composite != *report.GradeProcess {
		t.Errorf("composite (%v) should equal grade_process (%v) when solution is absent", report.Composite, report.GradeProcess)
	}
	// The grade fields survive a JSON round-trip.
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var rt RunReport
	if err := json.Unmarshal(data, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.GradeProcess == nil || *rt.GradeProcess != *report.GradeProcess || rt.GradeSolution != nil {
		t.Errorf("grade fields did not round-trip: process=%v solution=%v", rt.GradeProcess, rt.GradeSolution)
	}
}

// contaminationTranscript is a synthetic claude-shape transcript where the
// assistant EXPLICITLY warns that the retrieval pool is contaminated with test-set
// samples and the team proceeds anyway — the exact incident the integrity lens
// exists to surface.
const contaminationTranscript = `{"type":"user","message":{"role":"user","content":"build a retrieval system and evaluate it on the benchmark"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Warning: your retrieval pool is contaminated with test-set samples. Evaluating on this is invalid and the results would be illegal for the competition. Remove the held-out test rows before you proceed."}]}}
{"type":"user","message":{"role":"user","content":"just proceed anyway, we do not have time to fix it"}}`

const cleanTranscript = `{"type":"user","message":{"role":"user","content":"build a retrieval system and evaluate it on the benchmark"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"The pipeline looks correct and the train and eval splits are kept separate. Consider adding a couple more unit tests for the ranker."}]}}
{"type":"user","message":{"role":"user","content":"great, added the tests"}}`

// cannedRunner returns the same lens JSON for every agent call (all lenses and the
// summary), letting a Submit test assert flag/grade wiring deterministically.
func cannedRunner(json string) agent.Runner {
	return func(ctx context.Context, dir string, args []string, input []byte, timeout time.Duration) (string, error) {
		return json, nil
	}
}

// gradeAFromLenses recomputes the expected Grade A (process) mean over the SUPPORTED
// process lenses actually present in a report, mirroring the penalty-only rule:
// integrity is counted only when it meets the penalty condition
// (integrityIsPenalty), otherwise it is excluded. It reports whether integrity was
// counted — the assertion that the integrity lens joined Grade A.
func gradeAFromLenses(t *testing.T, lenses []LensResult, integritySignal bool) (mean float64, hasIntegrity bool) {
	t.Helper()
	integrityPenalty := integrityIsPenalty(lenses, integritySignal)
	supported := map[string]float64{}
	for _, l := range lenses {
		if l.Score != nil && l.Supported {
			supported[l.Lens] = *l.Score
		}
	}
	var sum float64
	var n int
	for _, name := range processLenses {
		if name == LensIntegrity && !integrityPenalty {
			continue
		}
		if v, ok := supported[name]; ok {
			sum += v
			n++
			if name == LensIntegrity {
				hasIntegrity = true
			}
		}
	}
	if n == 0 {
		t.Fatal("no supported process lenses")
	}
	return sum / float64(n), hasIntegrity
}

func TestBriefIncludesAssistantIntegrityContext(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 2, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": contaminationTranscript})
	runner := fakeRunner{gitLog: gitLogRecord("c0ffee1234567", "", now.Add(-90*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	sc, err := assembleSubmissionContext(context.Background(), runner, t.TempDir(), brainDir, "gh/team/proj", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("assembleSubmissionContext: %v", err)
	}
	for _, want := range []string{"integrity context", "[assistant]", "contaminated with test-set", "proceed anyway"} {
		if !strings.Contains(sc.Brief, want) {
			t.Errorf("brief missing %q:\n%s", want, sc.Brief)
		}
	}
}

// writeFactsFixture lays down a branch facts.ndjson with n filler facts so a test
// can push the facts section toward its budget cap.
func writeFactsFixture(t *testing.T, brainDir, branch string, n int, text string) {
	t.Helper()
	dir := filepath.Join(brainDir, "facts", filepath.FromSlash(branch))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		rec := brainstore.FactRecord{
			ID:    fmt.Sprintf("f%d", i),
			Kind:  "decision",
			Paths: []string{"pkg/a.go"},
			Text:  fmt.Sprintf("fact %d: %s", i, text),
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.ndjson"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBriefReservesIntegritySectionUnderBudget is the BUG #1 regression: a brain
// whose facts + human prompts alone blow past contextMaxBytes must still keep the
// integrity (assistant-context) section — it may never be the section the tail
// truncate sacrifices. Fails (integrity chopped) if the buildBrief reserve is
// reverted.
func TestBriefReservesIntegritySectionUnderBudget(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 2, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	filler := strings.Repeat("lorem ipsum dolor sit amet consectetur ", 20) // ~780 bytes
	// Many large human prompts to saturate the human-excerpt budget, then the
	// assistant contamination warning at the tail.
	var tb strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&tb, "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":%q}}\n", fmt.Sprintf("prompt %d %s", i, filler))
	}
	tb.WriteString(contaminationTranscript)
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": tb.String()})
	// A large facts store to consume ~half the context budget.
	writeFactsFixture(t, brainDir, "main", 300, filler)

	runner := fakeRunner{gitLog: gitLogRecord("c0ffee1234567", "", now.Add(-90*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}
	sc, err := assembleSubmissionContext(context.Background(), runner, t.TempDir(), brainDir, "gh/team/proj", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("assembleSubmissionContext: %v", err)
	}
	if len(sc.Brief) > contextMaxBytes {
		t.Fatalf("brief exceeds contextMaxBytes: %d > %d", len(sc.Brief), contextMaxBytes)
	}
	// Sanity: the budget pressure is real (facts and human excerpts are present).
	if !strings.Contains(sc.Brief, "Durable facts") || !strings.Contains(sc.Brief, "Human prompts") {
		t.Fatalf("expected facts + human-prompt sections to exercise the budget:\n%.200s", sc.Brief)
	}
	for _, want := range []string{"integrity context", "contaminated"} {
		if !strings.Contains(sc.Brief, want) {
			t.Fatalf("brief missing %q under budget pressure (len=%d); the integrity section was sacrificed by tail truncation", want, len(sc.Brief))
		}
	}
}

// TestIntegritySignalKeywordPrecision is the BUG HIGH-1 regression: the integrity
// signal gate must be high-precision. Ordinary coding chatter that merely shares a
// stem with an integrity term must NOT trip it (else the hallucination backstop is a
// no-op), while phrasings a real contamination/leakage/cheating/rules warning uses
// MUST trip it. Fails if integrityKeywords is reverted to bare stems.
func TestIntegritySignalKeywordPrecision(t *testing.T) {
	// Benign turns that share a stem with an integrity term but are ordinary ML /
	// coding chatter — none may trip the signal. Each was verified against the final
	// integrityKeywords list (case-insensitive substring). NOTE: "we used the same
	// data loader for both splits" is intentionally absent — it legitimately matches
	// "same data" (needed for a real-warning phrasing below), so asserting it as
	// benign would be a false expectation.
	benign := []string{
		"we should fix this memory leak",                               // "leak" is not a keyword; "leakage" is
		"let me re-evaluate the expression",                            // "evaluat" is not a keyword
		"this will invalidate the cache",                               // "invalid" alone is not a keyword
		"here is the test setup for the parser",                        // "test set" alone is not a keyword
		"grab a cheat sheet for the API",                               // "cheating" != "cheat sheet"
		"catch the IllegalStateException",                              // bare "illegal" is not a keyword
		"the ground truth labels look fine",                            // "ground truth" is not a keyword
		"The backbone was trained on ImageNet so it should generalise", // bare "trained on" dropped
		"I added dropout to reduce overfitting on the val set",         // "overfit" dropped
		"submit the run to the leaderboard",                            // "leaderboard" dropped
		"this violates the interface contract",                         // "violates the" dropped
	}
	// Real-violation phrasings — recall must be preserved for every one of these.
	violations := []string{
		"your retrieval pool is contaminated with test-set samples", // "contaminated with"
		"you are testing on the same data you trained on",           // "same data"
		"we're overfitting to the holdout",                          // "holdout"
		"you trained on the test set",                               // "trained on the test"
		"the model already saw these examples during training",      // "saw these"
		"these results would be illegal for the competition",        // "illegal for the competition"
		"you hardcoded the benchmark answers",                       // "hardcoded the"
		"that's basically cheating for the leaderboard",             // "cheating"
		"this violates the competition rules",                       // "competition rules"
		"this is train/test leakage",                                // "train/test" + "leakage"
	}
	for _, s := range benign {
		if containsIntegrityKeyword(s) {
			t.Errorf("benign turn tripped the integrity signal (false positive): %q", s)
		}
	}
	for _, s := range violations {
		if !containsIntegrityKeyword(s) {
			t.Errorf("violation warning did NOT trip the integrity signal (false negative): %q", s)
		}
	}
}

// TestIntegritySignalScopedToCitedSession is the ANCHOR-SCOPING regression: the
// corroborating integrity signal must be true ONLY when a session the integrity
// lens actually cited carries an assistant integrity warning — not when a warning
// lives in some OTHER, uncited session of the brain. Fails (case a would be TRUE)
// if the signal is reverted to a brain-wide scan.
func TestIntegritySignalScopedToCitedSession(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", CreatedAt: now.Add(-2 * time.Hour), TranscriptPath: "sessions/main/s1.jsonl"},
		{SessionID: "s2", Branch: "main", CreatedAt: now.Add(-time.Hour), TranscriptPath: "sessions/main/s2.jsonl"},
		{SessionID: "s3", Branch: "main", CreatedAt: now.Add(-30 * time.Minute), TranscriptPath: "sessions/main/s3.jsonl"},
	}
	// s1: BENIGN — an assistant turn that literally contains "test-set" but is not a
	// warning (bare "test-set" is no longer a keyword). s2: a REAL warning. s3: no
	// integrity keyword at all.
	benign := `{"type":"assistant","message":{"content":[{"type":"text","text":"Nice, we hit 92% on the test-set and the numbers look solid."}]}}`
	warning := `{"type":"assistant","message":{"content":[{"type":"text","text":"Careful — you're testing on the same data you trained on; that's basically cheating for the leaderboard."}]}}`
	noKeyword := `{"type":"assistant","message":{"content":[{"type":"text","text":"Looks good, ship it. Maybe add a README section."}]}}`
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": benign, "s2": warning, "s3": noKeyword})

	lens := func(evidence ...string) LensResult {
		return LensResult{Lens: LensIntegrity, Evidence: evidence}
	}

	// (a) cites only the benign session -> FALSE. A brain-wide scan would leak s2's
	// warning and return TRUE, so this guard fails if scoping is reverted.
	if integritySignalForLens(brainDir, sessions, lens("s1")) {
		t.Error("case (a): signal TRUE for a cited benign session; brain-wide scan leaked an uncited warning")
	}
	// (b) cites the warning session -> TRUE.
	if !integritySignalForLens(brainDir, sessions, lens("s2")) {
		t.Error("case (b): signal FALSE for a cited session that carries a real warning")
	}
	// (c) cites a session with no integrity keyword -> FALSE.
	if integritySignalForLens(brainDir, sessions, lens("s3")) {
		t.Error("case (c): signal TRUE for a cited keyword-free session")
	}
	// Resolving by session TIMESTAMP works too (real warning session).
	if !integritySignalForLens(brainDir, sessions, lens(sessions[1].CreatedAt.UTC().Format(time.RFC3339))) {
		t.Error("signal FALSE when the warning session is cited by its RFC3339 timestamp")
	}
	// No evidence -> FALSE.
	if integritySignalForLens(brainDir, sessions, lens()) {
		t.Error("signal TRUE with no evidence anchors")
	}
	// A commit-only anchor that maps to no session contributes nothing -> FALSE, even
	// though s2 holds a warning.
	if integritySignalForLens(brainDir, sessions, lens("deadbeef1234")) {
		t.Error("signal TRUE for a commit-only anchor that resolves to no session")
	}
}

// TestBriefKeepsIntegritySectionWithLargeFactsAndSemantic is the BUG HIGH-2
// regression: a base of facts + a large semantic layer that on its own exceeds
// contextMaxBytes must not evict the integrity section via the final tail-truncate.
// The base-bounding in buildBrief must keep the integrity marker and warning text
// intact. Fails if the base-bounding is reverted (integrity chopped off the tail).
func TestBriefKeepsIntegritySectionWithLargeFactsAndSemantic(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 2, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": contaminationTranscript})

	// A large facts slice: each line caps at 600 bytes; the section caps at
	// contextMaxBytes/2, so this saturates the facts budget (~48KB).
	factText := strings.Repeat("durable decision detail ", 40) // ~960 bytes -> truncated to 600
	var facts []brainstore.FactRecord
	for i := 0; i < 200; i++ {
		facts = append(facts, brainstore.FactRecord{
			ID:    fmt.Sprintf("f%d", i),
			Kind:  "decision",
			Paths: []string{"pkg/a.go"},
			Text:  factText,
		})
	}

	// A large semantic layer: thousands of ByKind/TopFiles entries so the joined
	// label line dwarfs contextMaxBytes and forces the semantic cap to bind. Base
	// (facts + semantic) alone therefore exceeds contextMaxBytes.
	var byKind, topFiles []brainstore.LabelCount
	for i := 0; i < 3000; i++ {
		byKind = append(byKind, brainstore.LabelCount{Label: fmt.Sprintf("symbol_kind_label_%06d", i), Count: i})
		topFiles = append(topFiles, brainstore.LabelCount{Label: fmt.Sprintf("pkg/module/file_%06d.go", i), Count: i})
	}
	semantic := &brainstore.SemanticSummary{
		Symbols:   9000,
		Relations: 4000,
		Files:     3000,
		ByKind:    byKind,
		TopFiles:  topFiles,
	}

	sc := submissionContext{
		BrainDir: brainDir,
		Sessions: sessions,
		Facts:    facts,
		Semantic: semantic,
		Metrics:  Metrics{TimelineCategory: TimelineCleanStart},
	}
	sc.Brief = buildBrief(sc)

	// Sanity: the base sections really are big enough to blow the budget on their own.
	if len(semanticSummarySection(sc.Semantic)) <= semanticMaxBytes {
		t.Fatalf("test precondition weak: semantic section (%d) must exceed the cap (%d)", len(semanticSummarySection(sc.Semantic)), semanticMaxBytes)
	}
	if len(sc.Brief) > contextMaxBytes {
		t.Fatalf("brief exceeds contextMaxBytes: %d > %d", len(sc.Brief), contextMaxBytes)
	}
	for _, want := range []string{"integrity context", "contaminated"} {
		if !strings.Contains(sc.Brief, want) {
			t.Fatalf("brief missing %q with a large facts+semantic base (len=%d); the integrity section was sacrificed by tail truncation", want, len(sc.Brief))
		}
	}
	// Sanity: the base sections are actually present (this is a real large-base case).
	if !strings.Contains(sc.Brief, "Durable facts") || !strings.Contains(sc.Brief, "What was built") {
		t.Fatalf("expected facts + semantic sections to exercise the base budget:\n%.200s", sc.Brief)
	}
}

func TestSubmitRaisesIntegrityFlagOnContamination(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 4, FilesTouched: []string{"a.go"}, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": contaminationTranscript})
	runner := fakeRunner{gitLog: gitLogRecord("deadbeef00000", "", now.Add(-80*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	// The lens agent returns a low integrity score with a VALID anchor (session s1).
	canned := `{"score":1,"verdict":"assistant warned of test-set contamination; team proceeded without addressing it","bullets":["proceeded anyway"],"evidence":["s1"]}`
	report, err := Submit(context.Background(), runner, cannedRunner(canned), t.TempDir(), brainDir, "gh/team/proj", Params{Agent: "claude-code", HackathonStart: now.Add(-2 * time.Hour)}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !report.IntegrityFlag {
		t.Fatalf("IntegrityFlag = false, want true (integrity score 1 with a valid anchor)")
	}
	if !strings.Contains(report.IntegrityReason, "contamination") || !strings.Contains(report.IntegrityReason, "@s1") {
		t.Errorf("IntegrityReason = %q, want the verdict plus @s1 anchor", report.IntegrityReason)
	}
	// The integrity lens must be present, supported, scored — and (penalty condition
	// met: low score + real assistant warning) folded into Grade A.
	mean, hasIntegrity := gradeAFromLenses(t, report.Lenses, report.IntegritySignal)
	if !hasIntegrity {
		t.Fatal("integrity lens not among the supported Grade A (process) lenses")
	}
	if report.GradeProcess == nil {
		t.Fatal("GradeProcess nil")
	}
	if diff := *report.GradeProcess - mean; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess = %.4f, want %.4f (mean of supported process lenses incl. integrity)", *report.GradeProcess, mean)
	}
	// JSON exposes the flag + reason.
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"integrity_flag":true`) || !strings.Contains(string(data), `"integrity_reason"`) {
		t.Errorf("emitted JSON missing integrity_flag/integrity_reason:\n%s", data)
	}
}

func TestSubmitNoIntegrityFlagWhenClean(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 4, FilesTouched: []string{"a.go"}, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": cleanTranscript})
	runner := fakeRunner{gitLog: gitLogRecord("deadbeef00000", "", now.Add(-80*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	// No warning raised: the integrity lens scores high with a valid anchor.
	canned := `{"score":5,"verdict":"no integrity concerns raised","bullets":["clean splits"],"evidence":["s1"]}`
	report, err := Submit(context.Background(), runner, cannedRunner(canned), t.TempDir(), brainDir, "gh/team/proj", Params{Agent: "claude-code", HackathonStart: now.Add(-2 * time.Hour)}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if report.IntegrityFlag {
		t.Errorf("IntegrityFlag = true, want false (integrity score 5)")
	}
	if report.IntegrityReason != "" {
		t.Errorf("IntegrityReason = %q, want empty when not flagged", report.IntegrityReason)
	}
	// Penalty-only rule: a clean (high-score) integrity lens is EXCLUDED from Grade A,
	// so it never inflates the process grade — even though it stays a scored,
	// Supported lens. Grade A equals the mean of the other supported process lenses.
	mean, hasIntegrity := gradeAFromLenses(t, report.Lenses, report.IntegritySignal)
	if hasIntegrity {
		t.Error("clean high-score integrity must NOT feed Grade A (penalty-only)")
	}
	if report.GradeProcess == nil {
		t.Fatal("GradeProcess nil")
	}
	if diff := *report.GradeProcess - mean; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess = %.4f, want %.4f (clean integrity excluded)", *report.GradeProcess, mean)
	}
}

// TestSubmitNoIntegrityFlagWithoutWarningSignal is the BUG #2 regression: a CLEAN
// transcript (no assistant integrity warning) must NOT raise the flag even when the
// LLM hallucinates a low integrity score citing a valid, resolvable anchor. Only the
// FLAG is gated — the lens still scores and stays Supported. Fails (false positive)
// if the DeriveIntegrityFlag signal gate is reverted.
func TestSubmitNoIntegrityFlagWithoutWarningSignal(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	sessions := []brainstore.Session{
		{SessionID: "s1", Branch: "main", Agent: "claude-code", CreatedAt: now.Add(-time.Hour), CheckpointsCount: 4, FilesTouched: []string{"a.go"}, TranscriptPath: "sessions/main/s1.jsonl"},
	}
	brainDir := writeBrainFixture(t, now, sessions, map[string]string{"s1": cleanTranscript})
	runner := fakeRunner{gitLog: gitLogRecord("deadbeef00000", "", now.Add(-80*time.Minute).Format(time.RFC3339), "dev", "dev@x.com", "init")}

	// The LLM HALLUCINATES a low integrity score citing a VALID anchor (session s1),
	// but no assistant warning exists in the transcript — the flag must NOT fire.
	canned := `{"score":1,"verdict":"claims test-set contamination","bullets":["fabricated concern"],"evidence":["s1"]}`
	report, err := Submit(context.Background(), runner, cannedRunner(canned), t.TempDir(), brainDir, "gh/team/proj", Params{Agent: "claude-code", HackathonStart: now.Add(-2 * time.Hour)}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if report.IntegritySignal {
		t.Fatal("IntegritySignal = true, want false for a clean transcript (no assistant integrity keyword)")
	}
	if report.IntegrityFlag {
		t.Fatalf("IntegrityFlag = true, want false: no assistant integrity warning in the brain (false positive)")
	}
	if report.IntegrityReason != "" {
		t.Errorf("IntegrityReason = %q, want empty when not flagged", report.IntegrityReason)
	}
	// The lens score / Supported behavior must be UNCHANGED — only the flag is gated.
	var found bool
	for _, l := range report.Lenses {
		if l.Lens != LensIntegrity {
			continue
		}
		found = true
		if l.Score == nil || *l.Score != 1 {
			t.Errorf("integrity score = %v, want 1 (score unchanged by the flag gate)", l.Score)
		}
		if !l.Supported {
			t.Error("integrity lens should still be Supported (anchor s1 resolves); only the flag is gated")
		}
	}
	if !found {
		t.Fatal("integrity lens missing from report")
	}
}

func TestDeriveIntegrityFlag(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	cases := []struct {
		name     string
		lenses   []LensResult
		wantFlag bool
	}{
		{"low + supported flags", []LensResult{{Lens: LensIntegrity, Score: s(1), Supported: true, Verdict: "warned", Evidence: []string{"s1"}}}, true},
		{"at threshold flags", []LensResult{{Lens: LensIntegrity, Score: s(2), Supported: true, Evidence: []string{"s1"}}}, true},
		{"above threshold no flag", []LensResult{{Lens: LensIntegrity, Score: s(2.5), Supported: true, Evidence: []string{"s1"}}}, false},
		{"low but unsupported no flag", []LensResult{{Lens: LensIntegrity, Score: s(1), Supported: false, Evidence: []string{"s1"}}}, false},
		{"missing lens no flag", []LensResult{{Lens: LensPrompting, Score: s(1), Supported: true}}, false},
		{"nil score no flag", []LensResult{{Lens: LensIntegrity, Supported: true}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// signal=true isolates the lens-based gating this table exercises; the
			// signal gate itself is covered by TestSubmitNoIntegrityFlagWithoutWarningSignal.
			got, reason := DeriveIntegrityFlag(tc.lenses, true)
			if got != tc.wantFlag {
				t.Errorf("DeriveIntegrityFlag flag = %v, want %v", got, tc.wantFlag)
			}
			if got && reason == "" {
				t.Error("flagged result must carry a non-empty reason")
			}
			if !got && reason != "" {
				t.Errorf("unflagged result must have empty reason, got %q", reason)
			}
		})
	}
}

func TestGrades(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	lens := func(name string, score float64, supported bool) LensResult {
		return LensResult{Lens: name, Score: &score, Supported: supported}
	}
	eq := func(got, want *float64) bool {
		if got == nil || want == nil {
			return got == want
		}
		d := *got - *want
		return d < 1e-9 && d > -1e-9
	}
	cases := []struct {
		name                string
		lenses              []LensResult
		wantP, wantS, wantC *float64
	}{
		{
			name:   "both grades present",
			lenses: []LensResult{lens(LensAuthenticity, 4, true), lens(LensPrompting, 5, true), lens(LensEffort, 3, true), lens(LensOutcome, 5, true)},
			wantP:  s(4), wantS: s(5), wantC: s(4.5), // process mean(4,5,3)=4, solution 5, combined 4.5
		},
		{
			name:   "integrity joins grade A and drags it down",
			lenses: []LensResult{lens(LensAuthenticity, 4, true), lens(LensPrompting, 5, true), lens(LensEffort, 3, true), lens(LensIntegrity, 0, true), lens(LensOutcome, 5, true)},
			wantP:  s(3), wantS: s(5), wantC: s(4), // process mean(4,5,3,0)=3, solution 5, combined 4
		},
		{
			name:   "only process (outcome unsupported -> solution nil, combined = process)",
			lenses: []LensResult{lens(LensAuthenticity, 4, true), lens(LensEffort, 2, true), lens(LensOutcome, 5, false)},
			wantP:  s(3), wantS: nil, wantC: s(3),
		},
		{
			name:   "only solution",
			lenses: []LensResult{lens(LensOutcome, 4, true)},
			wantP:  nil, wantS: s(4), wantC: s(4),
		},
		{
			name:   "none supported",
			lenses: []LensResult{lens(LensAuthenticity, 4, false), lens(LensOutcome, 5, false)},
			wantP:  nil, wantS: nil, wantC: nil,
		},
		{
			name:   "empty",
			lenses: nil,
			wantP:  nil, wantS: nil, wantC: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// signal=true so the "integrity joins grade A" case exercises the penalty
			// path; the non-integrity cases are unaffected by the signal.
			p, sol, c := Grades(tc.lenses, true)
			if !eq(p, tc.wantP) || !eq(sol, tc.wantS) || !eq(c, tc.wantC) {
				t.Errorf("Grades() = (%v, %v, %v), want (%v, %v, %v)", p, sol, c, tc.wantP, tc.wantS, tc.wantC)
			}
		})
	}
}

// TestGradeAExcludesCleanIntegrity: a clean, high-scoring integrity lens (even with
// the signal present, since a high score fails the penalty gate) is EXCLUDED from
// Grade A, so it cannot inflate the process grade. Grade A is the mean of the other
// three supported process lenses. Fails (integrity folded in at 5) if the
// penalty-gate is reverted.
func TestGradeAExcludesCleanIntegrity(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	lenses := []LensResult{
		{Lens: LensAuthenticity, Score: s(4), Supported: true},
		{Lens: LensPrompting, Score: s(3), Supported: true},
		{Lens: LensEffort, Score: s(2), Supported: true},
		{Lens: LensIntegrity, Score: s(5), Supported: true, Evidence: []string{"s1"}},
	}
	p, _, _ := Grades(lenses, true)
	if p == nil {
		t.Fatal("GradeProcess nil")
	}
	want := (4.0 + 3.0 + 2.0) / 3.0
	if diff := *p - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess = %.4f, want %.4f (clean integrity excluded)", *p, want)
	}
	if flag, _ := DeriveIntegrityFlag(lenses, true); flag {
		t.Error("clean high-score integrity must not raise the red flag")
	}
}

// TestGradeAIncludesFlaggedIntegrity: a supported low integrity score WITH a real
// assistant warning (signal) meets the penalty condition, so it is folded into Grade
// A and drags it down (mean of all four). The companion assertion — the same lens
// set WITHOUT the signal excludes integrity — makes this fail if the penalty-gate is
// reverted (revert would fold integrity in regardless of signal).
func TestGradeAIncludesFlaggedIntegrity(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	lenses := []LensResult{
		{Lens: LensAuthenticity, Score: s(4), Supported: true},
		{Lens: LensPrompting, Score: s(3), Supported: true},
		{Lens: LensEffort, Score: s(2), Supported: true},
		{Lens: LensIntegrity, Score: s(1), Supported: true, Verdict: "warned", Evidence: []string{"s1"}},
	}
	p, _, _ := Grades(lenses, true)
	if p == nil {
		t.Fatal("GradeProcess nil")
	}
	wantWith := (4.0 + 3.0 + 2.0 + 1.0) / 4.0
	if diff := *p - wantWith; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess (signal) = %.4f, want %.4f (flagged integrity dragged in)", *p, wantWith)
	}
	if flag, _ := DeriveIntegrityFlag(lenses, true); !flag {
		t.Error("flagged low integrity must raise the red flag")
	}
	// Same lenses, no signal: integrity is excluded (penalty gate keys off the signal).
	pNoSignal, _, _ := Grades(lenses, false)
	if pNoSignal == nil {
		t.Fatal("GradeProcess nil")
	}
	wantWithout := (4.0 + 3.0 + 2.0) / 3.0
	if diff := *pNoSignal - wantWithout; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess (no signal) = %.4f, want %.4f (integrity excluded without signal)", *pNoSignal, wantWithout)
	}
}

// TestGradeAExcludesHallucinatedLowIntegrity: a supported LOW integrity score but NO
// assistant warning (signal false) is a hallucinated concern — it must NOT drag Grade
// A and must NOT raise the flag. Fails (integrity folded in at 1) if the penalty-gate
// is reverted.
func TestGradeAExcludesHallucinatedLowIntegrity(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	lenses := []LensResult{
		{Lens: LensAuthenticity, Score: s(4), Supported: true},
		{Lens: LensPrompting, Score: s(3), Supported: true},
		{Lens: LensEffort, Score: s(2), Supported: true},
		{Lens: LensIntegrity, Score: s(1), Supported: true, Evidence: []string{"s1"}},
	}
	p, _, _ := Grades(lenses, false)
	if p == nil {
		t.Fatal("GradeProcess nil")
	}
	want := (4.0 + 3.0 + 2.0) / 3.0
	if diff := *p - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("GradeProcess = %.4f, want %.4f (hallucinated low integrity excluded)", *p, want)
	}
	if flag, _ := DeriveIntegrityFlag(lenses, false); flag {
		t.Error("hallucinated low integrity (no signal) must not raise the red flag")
	}
}

// TestGradeADeterministicAcrossAnchorLuck: two teams identical except one's clean
// integrity lens resolved its anchor (Supported, score 5) and the other's did not
// (unsupported) — Grade A must be identical, because a clean integrity read is
// excluded either way. Fails (grades diverge: 3.5 vs 3.0) if the penalty-gate is
// reverted, since revert would fold the resolved 5 into Grade A.
func TestGradeADeterministicAcrossAnchorLuck(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	base := []LensResult{
		{Lens: LensAuthenticity, Score: s(4), Supported: true},
		{Lens: LensPrompting, Score: s(3), Supported: true},
		{Lens: LensEffort, Score: s(2), Supported: true},
	}
	withAnchor := append(append([]LensResult{}, base...), LensResult{Lens: LensIntegrity, Score: s(5), Supported: true, Evidence: []string{"s1"}})
	withoutAnchor := append(append([]LensResult{}, base...), LensResult{Lens: LensIntegrity, Score: s(5), Supported: false})
	pa, _, _ := Grades(withAnchor, false)
	pb, _, _ := Grades(withoutAnchor, false)
	if pa == nil || pb == nil {
		t.Fatal("GradeProcess nil")
	}
	if diff := *pa - *pb; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Grade A differs by anchor luck: %.4f vs %.4f", *pa, *pb)
	}
	want := (4.0 + 3.0 + 2.0) / 3.0
	if diff := *pa - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Grade A = %.4f, want %.4f (clean integrity excluded)", *pa, want)
	}
}
