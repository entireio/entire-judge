package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suhaanthayyil/entire-judge/internal/judge"
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
