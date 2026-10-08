package tui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/createform"
)

// openCreate shows the "new TAW site" form (n).
func (m Model) openCreate() (tea.Model, tea.Cmd) {
	if m.deps.Actions == nil {
		m.setFlash("unavailable: "+errText(m.deps.ActionsErr), true)
		return m, nil
	}
	if m.task != nil && m.task.running {
		m.setFlash(m.task.title+" is still running (o shows it)", true)
		return m, nil
	}
	f := createform.Defaults(m.deps.CreateDefaults)
	m.fields = &f
	m.form = createform.New(m.deps.Paths, m.dark, m.fields).WithWidth(m.formWidth())
	m.mode = modeCreate
	init := m.form.Init()
	m = m.sizeForm()
	return m, init
}

func (m Model) formWidth() int { return min(max(m.width-8, 40), 96) }

// sizeForm tells the form how much room it has: huh lays out each page from
// the window size it's sent, and shows only titles without one.
func (m Model) sizeForm() Model {
	if m.form == nil {
		return m
	}
	model, _ := m.form.Update(tea.WindowSizeMsg{Width: m.formWidth(), Height: max(m.bodyHeight()-6, 8)})
	if f, ok := model.(*huh.Form); ok {
		m.form = f
	}
	return m
}

// formOwns reports whether msg goes to the open form rather than the
// dashboard (huh sends itself messages between fields).
func (m Model) formOwns(msg tea.Msg) bool {
	if m.mode != modeCreate || m.form == nil {
		return false
	}
	switch msg.(type) {
	case scanDoneMsg, tickMsg, taskEventMsg, spinner.TickMsg, siteOpDoneMsg, actionDoneMsg, tea.WindowSizeMsg, tea.BackgroundColorMsg:
		return false
	}
	return true
}

// onForm passes a message to the form and acts when it's done.
func (m Model) onForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			return m.closeCreate("Nothing changed.", false), nil
		}
	}
	model, cmd := m.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.form = f
	}
	switch m.form.State {
	case huh.StateAborted:
		return m.closeCreate("Nothing changed.", false), nil
	case huh.StateCompleted:
		return m.finishCreate()
	}
	return m, cmd
}

// finishCreate starts the creation the form asked for.
func (m Model) finishCreate() (tea.Model, tea.Cmd) {
	if !m.fields.Confirmed {
		return m.closeCreate("Nothing changed.", false), nil
	}
	r, err := create.Normalize(m.deps.Paths, m.fields.Request())
	if err == nil {
		err = create.Check(m.deps.Paths, r)
	}
	if err != nil {
		return m.closeCreate(err.Error(), true), nil
	}
	t, err := m.deps.Actions.CreateTask(r)
	if err != nil {
		return m.closeCreate(err.Error(), true), nil
	}
	m = m.closeCreate("", false)
	m.selectAfterScan = r.Slug
	return m.startTask(t)
}

func (m Model) closeCreate(flash string, isErr bool) Model {
	m.mode, m.form, m.fields = modeTable, nil, nil
	if flash != "" {
		m.setFlash(flash, isErr)
	}
	return m
}

// createScreen is the form, centered in a rounded box.
func (m Model) createScreen(h int) string {
	p := m.pal
	title := createform.Header(p) + p.Fg(p.Muted).Render(" · esc cancels")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Faint).Padding(1, 2)
	content := title + "\n\n" + m.form.View()
	return lipgloss.Place(m.width, h, lipgloss.Center, lipgloss.Center, box.Render(content))
}
