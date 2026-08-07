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

func TestOpenSavedBoardLoadsRanking(t *testing.T) {
	score := 4.5
	rep := judge.RunReport{
		SchemaVersion: 1,
		Kind:          "entire_judge_submission",
		SubmissionID:  "gh/team/proj",
		Composite:     &score,
		Lenses:        []judge.LensResult{},
	}
	board := judge.RankReport{
		SchemaVersion: 1,
		Kind:          "entire_judge_ranking",
		Submissions:   []judge.RankEntry{{Rank: 1, SubmissionID: "gh/team/proj", Composite: &score, Report: &rep}},
	}
	data, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-TTY: the saved board loads and falls back to the rendered text.
	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "t", Runner: fakeRunner{}})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"watch", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch saved board: %v", err)
	}
	if !strings.Contains(buf.String(), "gh/team/proj") {
		t.Errorf("expected the loaded submission in output:\n%s", buf.String())
	}

	// A non-board JSON file is rejected with a clear error.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var b2 bytes.Buffer
	cmd2 := NewRootCommand(Options{Version: "t", Runner: fakeRunner{}})
	cmd2.SetOut(&b2)
	cmd2.SetErr(&b2)
	cmd2.SetArgs([]string{"watch", bad})
	if err := cmd2.Execute(); err == nil || !strings.Contains(err.Error(), "not_a_saved_board") {
		t.Errorf("expected not_a_saved_board error, got %v", err)
	}
}

// TestOpenSavedBoardRegradesAndResorts proves a board saved by an older build
// (stale per-row composites) re-renders under the current model: grades are
// recomputed from the stored lens scores and the table is re-sorted by the
// recomputed combined, not the stale stored composite.
func TestOpenSavedBoardRegradesAndResorts(t *testing.T) {
	s := func(v float64) *float64 { return &v }
	mk := func(id string, auth, eff, outcome float64) judge.RunReport {
		return judge.RunReport{
			SchemaVersion: 1,
			Kind:          "entire_judge_submission",
			SubmissionID:  id,
			Composite:     s(0), // deliberately stale; must be recomputed from lenses
			Lenses: []judge.LensResult{
				{Lens: judge.LensAuthenticity, Score: s(auth), Supported: true},
				{Lens: judge.LensEffort, Score: s(eff), Supported: true},
				{Lens: judge.LensOutcome, Score: s(outcome), Supported: true},
			},
		}
	}
	low := mk("gh/team/low", 2, 2, 2)   // recomputed combined 2.0
	high := mk("gh/team/high", 5, 5, 5) // recomputed combined 5.0
	// Saved in the WRONG order (low first) with stale composites that would, if
	// trusted, keep low on top.
	board := judge.RankReport{
		SchemaVersion: 1,
		Kind:          "entire_judge_ranking",
		Submissions: []judge.RankEntry{
			{Rank: 1, SubmissionID: "gh/team/low", Composite: s(9.9), Report: &low},
			{Rank: 2, SubmissionID: "gh/team/high", Composite: s(0.1), Report: &high},
		},
	}
	data, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "board.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd := NewRootCommand(Options{Version: "t", Runner: fakeRunner{}})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"watch", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("watch saved board: %v", err)
	}
	out := buf.String()
	hi, lo := strings.Index(out, "gh/team/high"), strings.Index(out, "gh/team/low")
	if hi < 0 || lo < 0 || hi > lo {
		t.Errorf("expected high (idx %d) re-sorted above low (idx %d) by recomputed combined\n%s", hi, lo, out)
	}
	// The recomputed Combined (5.00), not the stale stored 0.1, is shown for high.
	if !strings.Contains(out, "Combined: 5.00/5") {
		t.Errorf("expected recomputed Combined 5.00 for high, got:\n%s", out)
	}
}
