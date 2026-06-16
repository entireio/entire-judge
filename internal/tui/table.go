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

// submissionColumns lays out the columns for a given inner table width: a rank
// number, a flexible submission id, a fixed score, and a one-glyph flag marker.
func submissionColumns(innerWidth int) []table.Column {
	const rankW, scoreW, flagW = 3, 5, 1
	// bubbles/table pads every column by 1 cell on each side, so 4 columns cost 8
	// columns of padding on top of their widths; budget for that or the header
	// wraps.
	idW := innerWidth - rankW - scoreW - flagW - 8
	if idW < 10 {
		idW = 10
	}
	return []table.Column{
		{Title: "#", Width: rankW},
		{Title: "Submission", Width: idW},
		{Title: "Score", Width: scoreW},
		{Title: "⚑", Width: flagW},
	}
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
		if section == sectionRanked {
			rank = fmt.Sprintf("%d", i+1)
			if m := medal(i + 1); m != "" { // gold/silver/bronze for the top 3
				rank = m
			}
			if r.Composite != nil {
				score = fmt.Sprintf("%.2f", *r.Composite)
			}
		} else {
			score = "gate"
		}
		flag := " "
		if len(r.Flags) > 0 {
			flag = "⚑"
		}
		rows = append(rows, table.Row{rank, r.SubmissionID, score, flag})
	}
	return rows
}
