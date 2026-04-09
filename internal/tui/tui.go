package tui

import tea "charm.land/bubbletea/v2"

type model struct {
	filePath    string
	torrentName string
	progress    int
}

func initModel() model {
	return model{}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m model) View() tea.View {
	return tea.NewView("")
}
