package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the dashboard keymap. It implements help.KeyMap so bubbles/help can
// render the short and full help views.
type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Enter   key.Binding
	Back    key.Binding
	Section key.Binding
	Filter  key.Binding
	GitHub  key.Binding
	Entire  key.Binding
	Help    key.Binding
	Quit    key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter:   key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("enter/→", "open")),
		Back:    key.NewBinding(key.WithKeys("esc", "left", "h"), key.WithHelp("esc/←", "back")),
		Section: key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "section")),
		Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		GitHub:  key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "github")),
		Entire:  key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "entire.io")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp is the single-line help shown in the footer.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.GitHub, k.Entire, k.Filter, k.Help, k.Quit}
}

// FullHelp is the expanded help shown when ? is toggled.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.GitHub, k.Entire, k.Section, k.Filter},
		{k.Help, k.Quit},
	}
}
