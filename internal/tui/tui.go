// Package tui renders the interactive jury dashboard for entire-judge: a ranked
// submission table on the left and a per-submission page (Score / Summary /
// Findings) on the right, with vim+arrow navigation, section tabs, a fuzzy
// filter, a help bar, and selectable themes. On a non-TTY the caller falls back
// to the rendered text summary, so the TUI is only constructed for a real
// terminal.
package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/entireio/entire-judge/internal/judge"
)

// Model is the dashboard model over a ranked + excluded set of submissions.
type Model struct {
	ranked   []judge.RunReport
	excluded []judge.RunReport
	meta     judge.RunMetadata
	theme    Theme

	keys   keyMap
	help   help.Model
	table  table.Model
	vp     viewport.Model
	filter textinput.Model

	section     int // sectionRanked | sectionExcluded
	visible     []judge.RunReport
	focusDetail bool
	filtering   bool

	width, height int
	leftTotal     int
	leftInner     int
	rightInner    int
	paneContentH  int
	tableCapacity int // data rows the table shows without scrolling
	detailWidth   int
	ready         bool
}

// NewModel builds a dashboard over the ranked and excluded submissions.
func NewModel(ranked, excluded []judge.RunReport, theme Theme, meta judge.RunMetadata) Model {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.CharLimit = 64

	m := Model{
		ranked:   ranked,
		excluded: excluded,
		meta:     meta,
		theme:    theme,
		keys:     defaultKeys(),
		help:     help.New(),
		table:    newSubmissionTable(theme),
		vp:       viewport.New(60, 20),
		filter:   ti,
		width:    100,
		height:   30,
		section:  sectionRanked,
	}
	m.rebuildVisible()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resize()
		return m, nil
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if m.filtering {
			return m.updateFiltering(msg)
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.resize()
			return m, nil
		case key.Matches(msg, m.keys.Filter):
			m.filtering = true
			m.filter.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.GitHub):
			return m, m.openSelected("github")
		case key.Matches(msg, m.keys.Entire):
			return m, m.openSelected("entire")
		case key.Matches(msg, m.keys.Section):
			m.section = sectionRanked + sectionExcluded - m.section
			m.focusDetail = false
			m.table.SetCursor(0)
			m.rebuildVisible()
			return m, nil
		}
		if m.focusDetail {
			if key.Matches(msg, m.keys.Back) {
				m.focusDetail = false
				return m, nil
			}
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		// list focus
		if key.Matches(msg, m.keys.Enter) {
			if len(m.visible) > 0 {
				m.focusDetail = true
			}
			return m, nil
		}
		prev := m.table.Cursor()
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		if m.table.Cursor() != prev {
			m.refreshDetail()
		}
		return m, cmd
	}
	return m, nil
}

// updateMouse handles wheel scrolling (scrolls the detail page) and left-click
// (click a list row to open it, or click the detail pane to focus it). The chrome
// above the first list data row is: title(0) tabs(1) pane-top-border(2)
// header-text(3) header-bottom-border(4) first-row(5) — the bubbles/table header
// is two physical lines because of its bottom border. bubbles v1.0.0 exposes no
// scroll offset, so a click is only hit-tested when the whole list fits unscrolled
// (len <= tableCapacity); a scrolled list just focuses the page for the current
// selection rather than risk selecting the wrong row.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		m.focusDetail = true
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if msg.X >= m.leftTotal {
			m.focusDetail = true
			return m, nil
		}
		if row := msg.Y - 5; len(m.visible) <= m.tableCapacity && row >= 0 && row < len(m.visible) {
			m.table.SetCursor(row)
			m.refreshDetail()
		}
		m.focusDetail = true
	}
	return m, nil
}

func (m Model) updateFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.filter.SetValue("")
		m.rebuildVisible()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.rebuildVisible()
	return m, cmd
}

// resize recomputes the pane geometry from the terminal size and re-flows the
// table, viewport, and detail content.
func (m *Model) resize() {
	leftTotal := m.width / 3
	if leftTotal < 30 {
		leftTotal = 30
	}
	if leftTotal > 52 {
		leftTotal = 52
	}
	rightTotal := m.width - leftTotal
	m.leftTotal = leftTotal
	m.leftInner = max(leftTotal-2, 12)
	m.rightInner = max(rightTotal-2, 20)

	// Measure the footer's real height (the help line can wrap on a narrow
	// terminal) so the body never overruns it — the source of the earlier
	// content-cut-into-footer bug.
	m.help.Width = m.width
	footerH := lipgloss.Height(m.footerView())
	bodyTotalH := m.height - 2 /*title+tabs*/ - footerH
	if bodyTotalH < 8 {
		bodyTotalH = 8
	}
	m.paneContentH = bodyTotalH - 2 // pane borders

	tableHeight := max(m.paneContentH-1, 3)
	m.table.SetColumns(submissionColumns(m.leftInner))
	m.table.SetWidth(m.leftInner)
	m.table.SetHeight(tableHeight)
	// Data rows shown = table height minus the 2-line header (text + bottom
	// border). Used to decide whether a click can be safely hit-tested.
	m.tableCapacity = max(tableHeight-2, 0)

	m.vp.Width = m.rightInner
	m.vp.Height = m.paneContentH
	m.filter.Width = m.leftInner - len(m.filter.Prompt) - 1
	m.detailWidth = m.rightInner
	m.refreshDetail()
}

// selectedReport returns the report under the cursor in the active section, or
// nil when the section is empty.
func (m Model) selectedReport() *judge.RunReport {
	if len(m.visible) == 0 {
		return nil
	}
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(m.visible) {
		return nil
	}
	return &m.visible[idx]
}

// openSelected opens the selected submission's GitHub or entire.io page in the
// browser via the OS opener. This is the reliable path: with mouse capture on,
// terminals can't deliver plain clicks to the OSC 8 links, so the keybindings
// (g / e) drive navigation regardless of terminal support.
func (m Model) openSelected(which string) tea.Cmd {
	r := m.selectedReport()
	if r == nil {
		return nil
	}
	owner, repo := ownerRepo(r.SubmissionID)
	if owner == "" {
		return nil
	}
	url := "https://entire.io/gh/" + owner + "/" + repo + "/commits"
	if which == "github" {
		url = "https://github.com/" + owner + "/" + repo
	}
	return openURLCmd(url)
}

// openURLCmd returns a command that opens url in the default browser without
// blocking the UI. A failure is silent — the URL is also shown as an OSC 8 link.
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var name string
		var args []string
		switch runtime.GOOS {
		case "darwin":
			name, args = "open", []string{url}
		case "windows":
			name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
		default:
			name, args = "xdg-open", []string{url}
		}
		_ = exec.Command(name, args...).Start()
		return nil
	}
}

func (m *Model) source() []judge.RunReport {
	if m.section == sectionExcluded {
		return m.excluded
	}
	return m.ranked
}

// rebuildVisible recomputes the filtered rows for the active section and refreshes
// the table + detail.
func (m *Model) rebuildVisible() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visible = m.visible[:0]
	for _, r := range m.source() {
		if q == "" || strings.Contains(strings.ToLower(r.SubmissionID), q) {
			m.visible = append(m.visible, r)
		}
	}
	m.table.SetRows(submissionRows(m.visible, m.section))
	if m.table.Cursor() >= len(m.visible) {
		m.table.SetCursor(max(0, len(m.visible)-1))
	}
	m.refreshDetail()
}

func (m *Model) refreshDetail() {
	if len(m.visible) == 0 {
		m.vp.SetContent(m.theme.dimStyle().Render("No submissions in this section."))
		m.vp.GotoTop()
		return
	}
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(m.visible) {
		idx = 0
	}
	rank := 0
	if m.section == sectionRanked {
		rank = idx + 1
	}
	content := renderDetail(m.theme, &m.visible[idx], m.meta, m.section == sectionExcluded, rank, m.detailWidth)
	m.vp.SetContent(content)
	m.vp.GotoTop()
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	if len(m.ranked) == 0 && len(m.excluded) == 0 {
		return "No submissions to display.\n"
	}

	// ---- header + tabs/filter (clipped to one line each so the layout math
	// never drifts) ----
	clip := lipgloss.NewStyle().MaxWidth(m.width)
	title := clip.Render(m.theme.titleStyle().Render("entire-judge") +
		m.theme.dimStyle().Render(fmt.Sprintf("  ·  %d ranked · %d excluded  ·  agent %s",
			len(m.ranked), len(m.excluded), m.meta.Agent)))

	var secondRow string
	if m.filtering {
		secondRow = clip.Render(m.filter.View())
	} else {
		secondRow = clip.Render(m.renderTabs())
	}

	// ---- body panes ----
	leftPane := m.theme.paneStyle(!m.focusDetail).
		Width(m.leftInner).Height(m.paneContentH).MaxHeight(m.paneContentH + 2).Render(m.table.View())
	rightPane := m.theme.paneStyle(m.focusDetail).
		Width(m.rightInner).Height(m.paneContentH).MaxHeight(m.paneContentH + 2).Render(m.vp.View())
	// Clip the joined body to the terminal width so the two min-width panes can't
	// overflow (and wrap) on a narrow terminal.
	body := lipgloss.NewStyle().MaxWidth(m.width).Render(
		lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane))

	return strings.Join([]string{title, secondRow, body, m.footerView()}, "\n")
}

// footerView renders the help line (plus a scroll hint when the detail pane has
// more below the fold) and the advisory disclaimer.
func (m Model) footerView() string {
	help := m.help.View(m.keys)
	if m.focusDetail && !m.vp.AtBottom() {
		help += m.theme.dimStyle().Render("   ↓ more")
	}
	disclaimer := lipgloss.NewStyle().Foreground(m.theme.Dim).Italic(true).
		MaxWidth(m.width).Render(judge.Disclaimer)
	return help + "\n" + disclaimer
}

func (m Model) renderTabs() string {
	active := lipgloss.NewStyle().Background(m.theme.Accent).Foreground(lipgloss.Color("0")).Bold(true)
	inactive := m.theme.dimStyle()
	ranked := fmt.Sprintf(" Ranked (%d) ", len(m.ranked))
	excluded := fmt.Sprintf(" Excluded (%d) ", len(m.excluded))
	if m.section == sectionRanked {
		return active.Render(ranked) + " " + inactive.Render(excluded)
	}
	return inactive.Render(ranked) + " " + active.Render(excluded)
}

// ScoreBar renders a score as a "█████░░ 4.0/5" style bar; an unscored lens shows
// a dash bar. It is plain text (no ANSI) so the non-TTY plain renderer can reuse
// it; the TUI uses its own colored bar.
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

// truncate shortens value to at most max display columns, on rune boundaries,
// appending an ellipsis when it cuts. It is display-width aware (multi-byte runes
// and wide glyphs are measured correctly), so it never splits a UTF-8 rune.
func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	return runewidth.Truncate(value, max, "...")
}
