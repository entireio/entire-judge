package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/suhaanthayyil/entire-judge/internal/judge"
)

const (
	sectionRanked = iota
	sectionExcluded
)

// newSubmissionTable builds the left-pane table, themed and focused. It starts
// with placeholder columns so rows can be set before the first resize (which
// recomputes the real widths); the table panics rendering rows against zero
// columns otherwise.
func newSubmissionTable(th Theme) table.Model {
	t := table.New(table.WithFocused(true), table.WithColumns(submissionColumns(36)))
	s := table.DefaultStyles()
	s.Header = s.Header.
		Foreground(th.Dim).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(th.Border).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("0")).
		Background(th.Accent).
		Bold(true)
	s.Cell = s.Cell.Foreground(th.Text)
	t.SetStyles(s)
	return t
}

const rankColWidth = 4

// submissionColumns lays out the columns for a given inner table width: a rank
// number (centered), a flexible submission id, a fixed score, and a trailing
// "mark" column for the medal / flag glyph. The medal lives in the LAST column on
// purpose: emoji render at a terminal-dependent width, so keeping them out of the
// inner columns means that variance can never misalign the rank/submission/score
// columns — only the right edge.
func submissionColumns(innerWidth int) []table.Column {
	const scoreW, markW = 5, 2
	// bubbles/table pads every column by 1 cell on each side, so 4 columns cost 8
	// columns of padding on top of their widths; budget for that or the header
	// wraps.
	idW := innerWidth - rankColWidth - scoreW - markW - 8
	if idW < 10 {
		idW = 10
	}
	return []table.Column{
		{Title: centerCell("#"), Width: rankColWidth},
		{Title: "Submission", Width: idW},
		{Title: "Score", Width: scoreW},
		{Title: "", Width: markW},
	}
}

// centerCell centers content within the rank column. bubbles/table renders cells
// left-aligned, so pre-centering to the exact column width is the only way to
// center the column. Ranks are plain digits (width-stable), so 1–2 digit numbers
// line up exactly.
func centerCell(s string) string {
	return lipgloss.PlaceHorizontal(rankColWidth, lipgloss.Center, s)
}

// submissionRows renders the reports for a section into table rows. The ranked
// section shows the rank and composite; the excluded section shows a dash and a
// "gate" marker since hard-gated submissions are not composited.
func submissionRows(reports []judge.RunReport, section int) []table.Row {
	rows := make([]table.Row, 0, len(reports))
	for i := range reports {
		r := reports[i]
		rank := "—"
		score := "—"
		medalGlyph := ""
		if section == sectionRanked {
			rank = fmt.Sprintf("%d", i+1)
			medalGlyph = medal(i + 1) // gold/silver/bronze for the top 3
			if r.Composite != nil {
				score = fmt.Sprintf("%.2f", *r.Composite)
			}
		} else {
			score = "gate"
		}
		// Trailing mark: medal for the top 3, else a flag marker. The medal wins
		// for a flagged top-3 submission; the detail view lists the flags anyway.
		mark := medalGlyph
		if mark == "" && len(r.Flags) > 0 {
			mark = "⚑"
		}
		rows = append(rows, table.Row{centerCell(rank), r.SubmissionID, score, mark})
	}
	return rows
}
