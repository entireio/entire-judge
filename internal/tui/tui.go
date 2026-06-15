// Package tui renders the interactive submission browser for entire-judge. On a
// non-TTY the caller falls back to the rendered text summary, so the TUI is only
// constructed for a real terminal.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/suhaanthayyil/entire-judge/internal/judge"
)

// Model is the bubbletea model browsing a set of submission reports.
type Model struct {
	reports  []judge.RunReport
	meta     judge.RunMetadata
	selected int
	expanded bool
	width    int
	height   int
	keys     keyMap
}

type keyMap struct {
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Quit  key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:    key.NewBinding(key.WithKeys("up", "k")),
		Down:  key.NewBinding(key.WithKeys("down", "j")),
		Enter: key.NewBinding(key.WithKeys("enter")),
		Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c", "esc")),
	}
}

// NewModel builds a browser model over the given reports.
func NewModel(reports []judge.RunReport, meta judge.RunMetadata) Model {
	return Model{
		reports: reports,
		meta:    meta,
		width:   100,
		height:  30,
		keys:    defaultKeys(),
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.selected > 0 {
				m.selected--
				m.expanded = false
			}
		case key.Matches(msg, m.keys.Down):
			if m.selected < len(m.reports)-1 {
				m.selected++
				m.expanded = false
			}
		case key.Matches(msg, m.keys.Enter):
			m.expanded = !m.expanded
		}
	}
	return m, nil
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	flagStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
)

func (m Model) View() string {
	if len(m.reports) == 0 {
		return "No submissions to display.\n"
	}
	left := m.renderList()
	right := m.renderDetail()

	leftWidth := m.width / 3
	if leftWidth < 24 {
		leftWidth = 24
	}
	rightWidth := m.width - leftWidth - 3
	if rightWidth < 30 {
		rightWidth = 30
	}
	leftPanel := lipgloss.NewStyle().Width(leftWidth).Render(left)
	rightPanel := lipgloss.NewStyle().Width(rightWidth).Render(right)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", rightPanel)

	footer := footerStyle.Render(judge.Disclaimer + "  |  ↑/↓ select · enter expand · q quit")
	return body + "\n\n" + footer + "\n"
}

func (m Model) renderList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Submissions"))
	b.WriteString("\n\n")
	for i, r := range m.reports {
		composite := "  n/a"
		if r.Composite != nil {
			composite = fmt.Sprintf("%4.2f", *r.Composite)
		}
		line := fmt.Sprintf("%s  %s", composite, truncate(r.SubmissionID, 22))
		if i == m.selected {
			line = selectedStyle.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
		if len(r.Flags) > 0 {
			b.WriteString("    " + flagStyle.Render(strings.Join(r.Flags, ", ")))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m Model) renderDetail() string {
	r := m.reports[m.selected]
	var b strings.Builder
	b.WriteString(titleStyle.Render(r.SubmissionID))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(r.BrainPath))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("agent %s · LLM %s", m.meta.Agent, r.Run.LLMStatus)))
	b.WriteString("\n\n")

	for _, lens := range r.Lenses {
		b.WriteString(fmt.Sprintf("%s %s\n", ScoreBar(lens.Score), lens.Lens))
		if lens.Verdict != "" {
			b.WriteString("  " + lens.Verdict + "\n")
		}
		for _, bullet := range lens.Bullets {
			b.WriteString("  - " + bullet + "\n")
		}
		if m.expanded {
			for _, e := range lens.Evidence {
				b.WriteString(dimStyle.Render("    [evidence] "+e) + "\n")
			}
			if !lens.Supported && lens.Score != nil {
				b.WriteString(dimStyle.Render("    (unsupported: no valid evidence anchors)") + "\n")
			}
		}
		b.WriteString("\n")
	}
	if !m.expanded {
		b.WriteString(dimStyle.Render("enter: expand evidence"))
		b.WriteString("\n")
	}
	return b.String()
}

// ScoreBar renders a score as a "█████░░ 4.0/5" style bar; an unscored lens shows
// a dash bar.
func ScoreBar(score *float64) string {
	const cells = 5
	if score == nil {
		return strings.Repeat("░", cells) + " -/5"
	}
	filled := int(*score + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > cells {
		filled = cells
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", cells-filled) + fmt.Sprintf(" %.1f/5", *score)
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}
