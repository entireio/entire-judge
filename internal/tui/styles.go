package tui

import "github.com/charmbracelet/lipgloss"

// Theme is the color palette for the dashboard. Themes are selectable via the
// --theme flag (or ENTIRE_JUDGE_THEME); the default is a neutral dark scheme that
// works on any 256-color terminal.
type Theme struct {
	Name   string
	Border lipgloss.Color // pane borders
	Accent lipgloss.Color // titles, selected row, active tab
	Text   lipgloss.Color // primary text
	Dim    lipgloss.Color // secondary/muted text
	Good   lipgloss.Color // score >= 4
	OK     lipgloss.Color // score 2.5–4
	Bad    lipgloss.Color // score < 2.5
	Flag   lipgloss.Color // flag pills / warnings
	Link   lipgloss.Color // clickable links
	Gold   lipgloss.Color // rank 1
	Silver lipgloss.Color // rank 2
	Bronze lipgloss.Color // rank 3
}

var themes = map[string]Theme{
	"default": {
		Name:   "default",
		Border: lipgloss.Color("244"),
		Accent: lipgloss.Color("39"),
		Text:   lipgloss.Color("231"), // bright white
		Dim:    lipgloss.Color("250"), // light gray (still readable)
		Good:   lipgloss.Color("47"),
		OK:     lipgloss.Color("214"),
		Bad:    lipgloss.Color("203"),
		Flag:   lipgloss.Color("220"),
		Link:   lipgloss.Color("45"),
		Gold:   lipgloss.Color("220"),
		Silver: lipgloss.Color("252"),
		Bronze: lipgloss.Color("173"),
	},
	"catppuccin": {
		Name:   "catppuccin",
		Border: lipgloss.Color("#6c7086"),
		Accent: lipgloss.Color("#89b4fa"),
		Text:   lipgloss.Color("#ffffff"), // white
		Dim:    lipgloss.Color("#bac2de"),
		Good:   lipgloss.Color("#a6e3a1"),
		OK:     lipgloss.Color("#f9e2af"),
		Bad:    lipgloss.Color("#f38ba8"),
		Flag:   lipgloss.Color("#fab387"),
		Link:   lipgloss.Color("#89dceb"),
		Gold:   lipgloss.Color("#f9e2af"),
		Silver: lipgloss.Color("#cdd6f4"),
		Bronze: lipgloss.Color("#eba0ac"),
	},
	"gruvbox": {
		Name:   "gruvbox",
		Border: lipgloss.Color("#928374"),
		Accent: lipgloss.Color("#83a598"),
		Text:   lipgloss.Color("#fbf1c7"), // bright cream-white
		Dim:    lipgloss.Color("#d5c4a1"),
		Good:   lipgloss.Color("#b8bb26"),
		OK:     lipgloss.Color("#fabd2f"),
		Bad:    lipgloss.Color("#fb4934"),
		Flag:   lipgloss.Color("#fe8019"),
		Link:   lipgloss.Color("#8ec07c"),
		Gold:   lipgloss.Color("#fabd2f"),
		Silver: lipgloss.Color("#ebdbb2"),
		Bronze: lipgloss.Color("#d65d0e"),
	},
	"tokyonight": {
		Name:   "tokyonight",
		Border: lipgloss.Color("#565f89"),
		Accent: lipgloss.Color("#7aa2f7"),
		Text:   lipgloss.Color("#ffffff"), // white
		Dim:    lipgloss.Color("#a9b1d6"),
		Good:   lipgloss.Color("#9ece6a"),
		OK:     lipgloss.Color("#e0af68"),
		Bad:    lipgloss.Color("#f7768e"),
		Flag:   lipgloss.Color("#ff9e64"),
		Link:   lipgloss.Color("#7dcfff"),
		Gold:   lipgloss.Color("#e0af68"),
		Silver: lipgloss.Color("#c0caf5"),
		Bronze: lipgloss.Color("#ff9e64"),
	},
}

// ThemeByName resolves a theme by name (case-insensitive), falling back to the
// default scheme for an empty or unknown name. The bool reports whether the name
// matched a known theme.
func ThemeByName(name string) (Theme, bool) {
	if t, ok := themes[normalizeThemeName(name)]; ok {
		return t, true
	}
	return themes["default"], name == "" || normalizeThemeName(name) == "default"
}

// ThemeNames lists the available theme names (for help text / flag docs).
func ThemeNames() []string {
	return []string{"default", "catppuccin", "gruvbox", "tokyonight"}
}

func normalizeThemeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == ' ' || r == '-' || r == '_':
			// fold separators so "tokyo night" == "tokyo-night" == "tokyonight"
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// scoreColor maps a 0–5 score to the theme's good/ok/bad color.
func (t Theme) scoreColor(score float64) lipgloss.Color {
	switch {
	case score >= 4:
		return t.Good
	case score >= 2.5:
		return t.OK
	default:
		return t.Bad
	}
}

func (t Theme) titleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
}

func (t Theme) dimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Dim)
}

func (t Theme) textStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Text)
}

func (t Theme) flagStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Flag)
}

func (t Theme) linkStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Link).Underline(true)
}

// rankColor returns the medal color for ranks 1–3, else the dim color.
func (t Theme) rankColor(rank int) lipgloss.Color {
	switch rank {
	case 1:
		return t.Gold
	case 2:
		return t.Silver
	case 3:
		return t.Bronze
	default:
		return t.Dim
	}
}

// medal returns the medal glyph for ranks 1–3, else "".
func medal(rank int) string {
	switch rank {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	default:
		return ""
	}
}

// osc8 wraps label in an OSC 8 terminal hyperlink so terminals that support it
// (iTerm2, kitty, WezTerm, modern Terminal.app, VS Code) make it cmd/ctrl-click
// to open. Terminals that don't support it just show the label. The escape
// sequences are zero-width, so callers must not pass the result through a
// width-constrained lipgloss style (it would miscount and wrap).
func osc8(url, label string) string {
	if url == "" {
		return label
	}
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// paneStyle is a rounded-border pane in the theme's border color.
func (t Theme) paneStyle(focused bool) lipgloss.Style {
	color := t.Border
	if focused {
		color = t.Accent
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color)
}
