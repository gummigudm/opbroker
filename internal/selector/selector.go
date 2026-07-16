// Package selector renders an interactive account picker.
package selector

import (
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gummigudm/opbroker/internal/agent"
)

// ErrNoTTY is returned when Pick needs to prompt but no controlling terminal
// is available (e.g. running under CI or with all fds redirected).
var ErrNoTTY = fmt.Errorf("no controlling terminal available for account picker; pass --account to select non-interactively")

// Pick prompts the user to choose an account from opts. If len(opts) == 1 it
// auto-selects. It uses the terminal via /dev/tty so it works even when
// stdout/stderr are piped to another process.
func Pick(opts []agent.AccountOption, prompt string) (agent.AccountOption, error) {
	if len(opts) == 0 {
		return agent.AccountOption{}, fmt.Errorf("no options to pick from")
	}
	if len(opts) == 1 {
		return opts[0], nil
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return agent.AccountOption{}, ErrNoTTY
	}
	defer tty.Close()

	m := newModel(opts, prompt)
	p := tea.NewProgram(m, tea.WithInput(tty), tea.WithOutput(tty))
	result, err := p.Run()
	if err != nil {
		return agent.AccountOption{}, fmt.Errorf("picker: %w", err)
	}
	final := result.(*model)
	if final.cancelled {
		return agent.AccountOption{}, fmt.Errorf("selection cancelled")
	}
	return final.opts[final.cursor], nil
}

// Filter returns the subset of opts whose Account matches account. If account
// is empty, all opts are returned.
func Filter(opts []agent.AccountOption, account string) []agent.AccountOption {
	if account == "" {
		return opts
	}
	out := make([]agent.AccountOption, 0, 1)
	for _, o := range opts {
		if o.Account == account {
			out = append(out, o)
		}
	}
	return out
}

// --- bubbletea model ---

type model struct {
	prompt    string
	opts      []agent.AccountOption
	cursor    int
	cancelled bool
	done      bool
}

func newModel(opts []agent.AccountOption, prompt string) *model {
	return &model{prompt: prompt, opts: opts}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.opts)-1 {
				m.cursor++
			}
		case "enter":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	selStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
)

func (m *model) View() string {
	var b []byte
	b = fmt.Appendf(b, "%s\n\n", titleStyle.Render(m.prompt))
	for i, o := range m.opts {
		cursor := "  "
		line := fmt.Sprintf("%s (%s)", o.Account, o.Title)
		if i == m.cursor {
			cursor = "▸ "
			line = selStyle.Render(line)
		} else {
			line = dimStyle.Render(line)
		}
		b = fmt.Appendf(b, "%s%s\n", cursor, line)
	}
	b = fmt.Appendf(b, "\n%s\n", dimStyle.Render("↑/↓ to move, enter to select, q/esc to cancel"))
	return string(b)
}

// Debug is helpful when integrating; writes the picker's state to w.
func (m *model) Debug(w io.Writer) { fmt.Fprintf(w, "cursor=%d cancelled=%v\n", m.cursor, m.cancelled) }
