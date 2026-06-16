package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/suhaanthayyil/entire-judge/internal/judge"
)

func score(v float64) *float64 { return &v }

func sampleReport() *judge.RunReport {
	return &judge.RunReport{
		SubmissionID: "gh/team/syntheci-shipping",
		RepoDir:      "/tmp/team/syntheci",
		Composite:    score(4.75),
		Summary:      "A maritime-intelligence platform built clean from data to demo.",
		Flags:        []string{"prompting_skill_unsupported"},
		Run:          judge.RunMetadata{Agent: "claude-code", LLMStatus: "ok"},
		Deterministic: judge.Metrics{
			TimelineCategory: judge.TimelineCleanStart,
			TimelineReason:   "all sessions after start",
			Sessions:         14,
			Turns:            34,
			FilesTouched:     127,
			AgentHistogram:   map[string]int{"Codex": 14, "Claude Code": 2},
			PrimaryAgent:     "Codex",
		},
		Lenses: []judge.LensResult{
			{Lens: judge.LensOutcome, Score: score(5), Verdict: "Coherent platform.", Bullets: []string{"Built RAG"}, Evidence: []string{"abc1234"}, Supported: true},
			{Lens: judge.LensAgent, Verdict: "Primary agent: Codex."},
		},
	}
}

func TestSubmissionRows(t *testing.T) {
	reports := []judge.RunReport{*sampleReport()}
	ranked := submissionRows(reports, sectionRanked)
	// rank 1 -> centered digit "1" in the rank column, gold medal in the trailing
	// mark column (medal wins over the flag for a top-3 submission).
	if len(ranked) != 1 || !strings.Contains(ranked[0][0], "1") || ranked[0][2] != "4.75" {
		t.Errorf("ranked row = %v, want rank 1 / score 4.75", ranked[0])
	}
	if ranked[0][3] != medal(1) {
		t.Errorf("expected gold medal in the mark column, got %q", ranked[0][3])
	}
	excl := submissionRows(reports, sectionExcluded)
	if !strings.Contains(excl[0][0], "—") || excl[0][2] != "gate" {
		t.Errorf("excluded row = %v, want dash rank / gate score", excl[0])
	}
	if excl[0][3] != "⚑" {
		t.Errorf("expected flag marker on the excluded flagged submission, got %q", excl[0][3])
	}
}

func TestHyperlinkIsZeroWidth(t *testing.T) {
	// OSC 8 hyperlinks must measure as just their visible label, or the viewport
	// and pane padding miscount and the right border misaligns.
	plain := lipgloss.Width("GitHub ↗")
	linked := lipgloss.Width(osc8("https://github.com/some/really-long-owner/really-long-repo", "GitHub ↗"))
	if plain != linked {
		t.Errorf("hyperlink width = %d, want %d (label width); OSC 8 escapes must be zero-width", linked, plain)
	}
}

func TestThemeByName(t *testing.T) {
	if th, ok := ThemeByName("catppuccin"); !ok || th.Name != "catppuccin" {
		t.Errorf("catppuccin not resolved: %+v ok=%v", th, ok)
	}
	if th, ok := ThemeByName("Tokyo Night"); !ok || th.Name != "tokyonight" {
		t.Errorf("separator-folded name not resolved: %+v ok=%v", th, ok)
	}
	if th, ok := ThemeByName("nonsense"); ok || th.Name != "default" {
		t.Errorf("unknown theme should fall back to default with ok=false, got %+v ok=%v", th, ok)
	}
}

func TestRenderDetailSectionsAndSummary(t *testing.T) {
	th, _ := ThemeByName("default")
	out := renderDetail(th, sampleReport(), judge.RunMetadata{Agent: "claude-code"}, false, 1, 80)
	for _, want := range []string{"Score", "Summary", "Findings", "gh/team/syntheci-shipping", "maritime-intelligence", "Codex×14", "Built RAG"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderDetail missing %q", want)
		}
	}
}

func TestViewSmoke(t *testing.T) {
	th, _ := ThemeByName("default")
	ranked := []judge.RunReport{*sampleReport()}
	excl := []judge.RunReport{*sampleReport()}
	m := NewModel(ranked, excl, th, judge.RunMetadata{Agent: "claude-code"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.(Model).View()
	for _, want := range []string{"entire-judge", "Ranked (1)", "Excluded (1)", "Submission"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q", want)
		}
	}
}

// TestDumpDashboard renders the dashboard to /tmp/judge-dashboard.txt (ANSI
// stripped) when JUDGE_DUMP is set, for a shareable static snapshot. It is a
// no-op in normal test runs.
func TestDumpDashboard(t *testing.T) {
	if os.Getenv("JUDGE_DUMP") == "" {
		t.Skip("set JUDGE_DUMP=1 to render a dashboard snapshot")
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	mk := func(id string, comp float64, flags []string) judge.RunReport {
		r := *sampleReport()
		r.SubmissionID = id
		r.Composite = score(comp)
		r.Flags = flags
		return r
	}
	ranked := []judge.RunReport{
		mk("gh/socratesomiliadis/syntheci-shipping", 4.75, nil),
		mk("gh/iraklisp98/NoteAI", 4.25, nil),
		mk("gh/manolisgeo/agentbuilder", 3.60, []string{"prompting_skill_unsupported"}),
	}
	excl := []judge.RunReport{mk("gh/entireio/cli", 0, nil)}
	excl[0].Deterministic.TimelineCategory = judge.TimelinePredatesEvent
	m := NewModel(ranked, excl, mustTheme("default"), judge.RunMetadata{Agent: "claude-code", LLMStatus: "ok"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	if err := os.WriteFile("/tmp/judge-dashboard.txt", []byte(updated.(Model).View()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustTheme(name string) Theme {
	th, _ := ThemeByName(name)
	return th
}

func TestClickSelectsCorrectRow(t *testing.T) {
	th, _ := ThemeByName("default")
	var ranked []judge.RunReport
	for i := 0; i < 4; i++ {
		r := *sampleReport()
		r.SubmissionID = "gh/team/sub" + string(rune('0'+i))
		ranked = append(ranked, r)
	}
	m := NewModel(ranked, nil, th, judge.RunMetadata{Agent: "x"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40}) // tall: all rows fit, no scroll
	m = updated.(Model)

	// First data row renders at Y=5 (title 0, tabs 1, pane border 2, header text
	// 3, header bottom border 4, first row 5). Clicking it must select row 0.
	u, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 5})
	if got := u.(Model).table.Cursor(); got != 0 {
		t.Errorf("click at Y=5 selected row %d, want 0 (off-by-one regression)", got)
	}
	u, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 7})
	if got := u.(Model).table.Cursor(); got != 2 {
		t.Errorf("click at Y=7 selected row %d, want 2", got)
	}
}

func TestRenderDetailExcludedShowsGate(t *testing.T) {
	th, _ := ThemeByName("default")
	r := sampleReport()
	r.Deterministic.TimelineCategory = judge.TimelinePredatesEvent
	out := renderDetail(th, r, judge.RunMetadata{Agent: "claude-code"}, true, 0, 80)
	if !strings.Contains(out, "EXCLUDED") {
		t.Errorf("excluded detail should show the hard-gate banner:\n%s", out)
	}
}
