package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/entire-judge/internal/judge"
)

func TestFeedbackFilename(t *testing.T) {
	if got := feedbackFilename("gh/acme/cool-app"); got != "feedback-acme-cool-app.md" {
		t.Errorf("filename = %q", got)
	}
}

func TestRenderFeedbackMarkdownAllTeams(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	winner := judge.RunReport{
		Kind:         "entire_judge_submission",
		SubmissionID: "gh/team/winner",
		Disclaimer:   judge.Disclaimer,
		Composite:    s(4.5),
		GradeProcess: s(4.0),
		Summary:      "Built a solid demo with clear prompting.",
		Deterministic: judge.Metrics{
			EntireSkillInvocations:     2,
			EntireCLIInvocations:       3,
			EntireSignalSessions:       2,
			DistinctEntireCapabilities: []string{"graph", "skill"},
		},
		Lenses: []judge.LensResult{
			{Lens: judge.LensCLIAwareness, Score: s(5), Supported: true, Verdict: "Strong Entire CLI use.", Bullets: []string{"graph + skill"}},
		},
	}
	body := renderFeedbackMarkdown(&winner, 1, false, "", 3)
	if !strings.Contains(body, "Congratulations") {
		t.Errorf("winner header missing:\n%s", body)
	}
	if !strings.Contains(body, "Entire CLI awareness") || !strings.Contains(body, "Skill invocations: 2") {
		t.Errorf("cli breakdown missing:\n%s", body)
	}
	if !strings.Contains(body, judge.Disclaimer) {
		t.Error("disclaimer missing")
	}

	nonWinner := winner
	nonWinner.SubmissionID = "gh/team/builder"
	nonBody := renderFeedbackMarkdown(&nonWinner, 5, false, "", 3)
	if !strings.Contains(nonBody, "Keep building") {
		t.Errorf("non-winner header missing:\n%s", nonBody)
	}

	excluded := winner
	excluded.SubmissionID = "gh/team/early"
	exBody := renderFeedbackMarkdown(&excluded, 0, true, "captured work predates the event start", 3)
	if !strings.Contains(exBody, "excluded") || !strings.Contains(exBody, "predates") {
		t.Errorf("excluded status missing:\n%s", exBody)
	}
}

func TestFeedbackCommandFromSavedBoard(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	mk := func(id string, composite float64) judge.RunReport {
		return judge.RunReport{
			SchemaVersion: 1,
			Kind:          "entire_judge_submission",
			SubmissionID:  id,
			Disclaimer:    judge.Disclaimer,
			Composite:     s(composite),
			Summary:       "Team summary for " + id,
			Lenses: []judge.LensResult{
				{Lens: judge.LensAuthenticity, Score: s(4.5), Supported: true, Verdict: "Clean start."},
				{Lens: judge.LensCLIAwareness, Score: s(0), Supported: true, Verdict: "No Entire usage."},
			},
		}
	}
	board := judge.RankReport{
		SchemaVersion: 1,
		Kind:          "entire_judge_ranking",
		Submissions: []judge.RankEntry{
			{Rank: 1, SubmissionID: "gh/a/one", Composite: s(4.5), Report: ptrReport(mk("gh/a/one", 4.5))},
			{Rank: 2, SubmissionID: "gh/b/two", Composite: s(3.0), Report: ptrReport(mk("gh/b/two", 3.0))},
		},
		Excluded: []judge.RankExcluded{
			{SubmissionID: "gh/c/out", Reason: "insufficient brain", Report: ptrReport(mk("gh/c/out", 0))},
		},
	}
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "board.json")
	data, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "feedback")

	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "t", Runner: fakeRunner{}})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"feedback", boardPath, "--out", outDir, "--winners", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("feedback: %v\n%s", err, buf.String())
	}
	// All three teams get a file.
	for _, name := range []string{"feedback-a-one.md", "feedback-b-two.md", "feedback-c-out.md"} {
		p := filepath.Join(outDir, name)
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("missing %s: %v", name, rerr)
		}
		if !strings.Contains(string(body), "Entire Judge Feedback") {
			t.Errorf("%s missing title", name)
		}
	}
	winnerBody, _ := os.ReadFile(filepath.Join(outDir, "feedback-a-one.md"))
	if !strings.Contains(string(winnerBody), "Congratulations") {
		t.Errorf("rank-1 should congratulate:\n%s", winnerBody)
	}
	secondBody, _ := os.ReadFile(filepath.Join(outDir, "feedback-b-two.md"))
	if !strings.Contains(string(secondBody), "Keep building") {
		t.Errorf("rank-2 should keep building:\n%s", secondBody)
	}
}

func ptrReport(r judge.RunReport) *judge.RunReport { return &r }
