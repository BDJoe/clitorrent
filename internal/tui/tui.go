package tui

import (
	"errors"
	"fmt"
	torrentFile "gotorrent/internal/torrentfile"
	"gotorrent/internal/util"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type model struct {
	choices      []string
	chosenIndex  int
	filePath     textinput.Model
	filePicker   filepicker.Model
	selectedFile string
	state        uiState
	percentage   float64
	message      string
	program      *tea.Program
	err          error
}

type uiState int

const (
	Main     uiState = 0
	Picker   uiState = 1
	Download uiState = 2
)

var (
	focusedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	blurredStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	focusedButton = focusedStyle.Render("[ Submit ]")
	blurredButton = fmt.Sprintf("[ %s ]", blurredStyle.Render("Submit"))
	header        = "Welcome To cliTorrent!\n\nSave Location:"
)

type clearErrorMsg struct{}

func clearErrorAfter(t time.Duration) tea.Cmd {
	return tea.Tick(t, func(_ time.Time) tea.Msg {
		return clearErrorMsg{}
	})
}

// type TickMsg time.Time

// func doTick() tea.Cmd {
// 	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
// 		return TickMsg(t)
// 	})
// }

func InitModel() model {
	path := textinput.New()
	path.SetVirtualCursor(false)
	path.Focus()
	path.SetWidth(40)
	p, _ := os.UserHomeDir()
	path.SetValue(p)

	picker := filepicker.New()
	picker.AllowedTypes = []string{".torrent"}
	picker.CurrentDirectory = p
	picker.AutoHeight = true

	return model{choices: []string{"path", "file", "start"}, chosenIndex: 0, filePath: path, filePicker: picker, state: uiState(Main)}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.filePicker.Init()) //, doTick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	// case util.ProgressMsg:
	// 	m.percentage = msg.Progress
	// 	m.message = msg.Message
	// 	return m, nil
	// case TickMsg:
	// 	perc, mess := getProgress(m)
	// 	m.percentage = perc
	// 	m.message = mess
	// 	return m, doTick()

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	switch m.state {
	case Picker:
		return pickerUpdate(m, msg)
	case Download:
		return progressUpdate(m, msg)
	}

	return mainUpdate(m, msg)
}

func (m model) View() tea.View {
	var s string
	switch m.state {
	case Main:
		s = mainView(m)
	case Picker:
		s = pickerView(m)
	case Download:
		s = progressView(m)
	}

	view := tea.NewView(s)
	view.AltScreen = true
	var c *tea.Cursor
	if m.filePath.Focused() {
		c = m.filePath.Cursor()
		c.Y += lipgloss.Height(header)
		view.Cursor = c
	}

	return view
}

func Run() (*tea.Program, error) {
	m := InitModel()
	p := tea.NewProgram(&m)
	m.program = p

	if _, err := p.Run(); err != nil {
		return p, err
	}
	return p, nil
}

// func getProgress(m model) (float64, string) {
// 	return p2p.CompletePercentage, m.torrent.Message
// }

func pickerView(m model) string {
	var s strings.Builder
	s.WriteString("\n  ")
	if m.err != nil {
		s.WriteString(m.filePicker.Styles.DisabledFile.Render(m.err.Error()))
	} else if m.selectedFile == "" {
		s.WriteString("Pick a file:")
	} else {
		s.WriteString("Selected file: " + m.filePicker.Styles.Selected.Render(m.selectedFile))
	}
	s.WriteString("\n\n" + m.filePicker.View() + "\n")

	return s.String()
}

func pickerUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "q" {
			m.state = Main
			return m, cmd
		}
	case clearErrorMsg:
		m.err = nil
	}

	m.filePicker, cmd = m.filePicker.Update(msg)

	// Did the user select a file?
	if didSelect, path := m.filePicker.DidSelectFile(msg); didSelect {
		// Get the path of the selected file.
		m.selectedFile = path
		m.state = Main
		return m, cmd
	}

	// Did the user select a disabled file?
	// This is only necessary to display an error to the user.
	if didSelect, path := m.filePicker.DidSelectDisabledFile(msg); didSelect {
		// Let's clear the selectedFile and display an error.
		m.err = errors.New(path + " is not valid.")
		m.selectedFile = ""
		return m, tea.Batch(cmd, clearErrorAfter(2*time.Second))
	}

	return m, cmd
}

func mainView(m model) string {
	var file string

	if len(m.selectedFile) == 0 {
		file = "\nChoose File\n"
	} else {
		file = fmt.Sprintf("\n%s\n", m.selectedFile)
	}

	button := &blurredButton
	switch m.choices[m.chosenIndex] {
	case "start":
		button = &focusedButton
	case "file":
		file = focusedStyle.Render(file)
	}

	s := lipgloss.JoinVertical(lipgloss.Top, header, m.filePath.View(), file, *button)
	// v := tea.NewView(s)
	// var c *tea.Cursor
	// if m.filePath.Focused() {
	// 	c = m.filePath.Cursor()
	// 	c.Y += lipgloss.Height(header)
	// 	v.Cursor = c
	// }
	return s
}

func mainUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		// case "ctrl+c":
		// 	return m, tea.Quit

		case "up", "down", "enter":
			s := msg.String()
			if s == "enter" {
				switch m.choices[m.chosenIndex] {
				case "path":
					m.chosenIndex++
				case "file":
					m.state = Picker
					return m, tea.Batch(m.filePicker.Init(), tea.RequestWindowSize)
				case "start":
					m.state = Download
					cmd = func() tea.Msg { return downloadFile(m) }
					return m, cmd
				}
			}
			if s == "up" {
				if m.chosenIndex != 0 {
					m.chosenIndex--
				}
			}
			if s == "down" {
				if m.chosenIndex < len(m.choices)-1 {
					m.chosenIndex++
				}
			}
			if m.chosenIndex == 0 {
				cmd = m.filePath.Focus()
			} else {
				m.filePath.Blur()
			}

			return m, cmd
		}
	}

	m.filePath, cmd = m.filePath.Update(msg)
	return m, cmd
}

func progressUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case util.ProgressMsg:
		if msg.Progress > 0 {
			m.percentage = msg.Progress
		}
		if len(msg.Message) > 0 {
			m.message = msg.Message
		}
		return m, nil
	}
	return m, nil
}

func progressView(m model) string {
	// s := fmt.Sprintf("Complete: %02f\n", m.percentage)
	// s += m.message
	s := lipgloss.JoinVertical(lipgloss.Top, m.message, fmt.Sprintf("Complete: %%%.02f\n", m.percentage))
	return s
}

func downloadFile(m model) tea.Msg {
	inPath := m.selectedFile
	outPath := m.filePath.Value()
	tf, err := torrentFile.Open(inPath)
	if err != nil {
		return tea.Quit()
	}
	go func() {
		err = tf.DownloadToFile(outPath, m.program)
	}()
	if err != nil {
		return tea.Quit()
	}
	return m
}

// func (m model) SetPath(path string) (tea.Model, tea.Cmd) {
// 	m.selectedPath = path
// 	return m, m.model.Init()
// }

// func (m model) SetFile(file string) (tea.Model, tea.Cmd) {
// 	m.selectedFile = file
// 	model := Home()
// 	model.SetPath(m.selectedPath)
// 	model.SetFile(m.selectedFile)
// 	return m.ChangeView(model)
// }

// func (m rootModel) ChangeView(model tea.Model) (tea.Model, tea.Cmd) {
// 	m.model = model
// 	return m.model, m.model.Init()
// }

// type mainModel struct {
// 	choices     []string
// 	chosenIndex int
// 	filePath    textinput.Model
// 	fileName    string
// }

// func Home() mainModel {
// 	path := textinput.New()
// 	path.SetVirtualCursor(false)
// 	path.Focus()
// 	path.SetWidth(40)

// 	return mainModel{choices: []string{"path", "file", "start"}, chosenIndex: 0,
// 		filePath: path, fileName: ""}
// }

// func (m mainModel) SetPath(path string) {
// 	m.filePath.SetValue(path)
// }

// func (m mainModel) SetFile(file string) {
// 	m.filePath.SetValue(file)
// }

// func (m mainModel) Init() tea.Cmd {
// 	return nil
// }

// func (m model) mainView() tea.View {
// 	header := "Welcome To cliTorrent!\n\nWhere should we save to?\n"

// 	var file string

// 	if len(m.fileName) == 0 {
// 		file = "\nChoose File"
// 	} else {
// 		file = fmt.Sprintf("\n%s", m.fileName)
// 	}

// 	button := &blurredButton
// 	switch m.choices[m.chosenIndex] {
// 	case "start":
// 		button = &focusedButton
// 	case "file":
// 		file = focusedStyle.Render(file)
// 	}

// 	s := lipgloss.JoinVertical(lipgloss.Top, header, m.filePath.View(), file, *button)
// 	v := tea.NewView(s)
// 	var c *tea.Cursor
// 	if m.filePath.Focused() {
// 		c = m.filePath.Cursor()
// 		c.Y += lipgloss.Height(header)
// 		v.Cursor = c
// 	}

// 	v.AltScreen = true
// 	return v
// }

// func (m model) mainUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
// 	var cmd tea.Cmd

// 	switch msg := msg.(type) {
// 	case tea.KeyPressMsg:
// 		switch msg.String() {
// 		case "ctrl+c":
// 			return m, tea.Quit
// 		case "enter":
// 			switch m.choices[m.chosenIndex] {
// 			case "file":
// 				picker := Picker()
// 				InitModel().SetPath(m.filePath.Value())
// 				return InitModel().ChangeView(picker)
// 			case "start":

// 			}
// 		case "up", "down":
// 			s := msg.String()
// 			if s == "up" {
// 				if m.chosenIndex != 0 {
// 					m.chosenIndex--
// 				}
// 			}
// 			if s == "down" {
// 				if m.chosenIndex < len(m.choices)-1 {
// 					m.chosenIndex++
// 				}
// 			}
// 			if m.chosenIndex == 0 {
// 				cmd = m.filePath.Focus()
// 			} else {
// 				m.filePath.Blur()
// 			}

// 			return m, cmd
// 		}
// 	}

// 	m.filePath, cmd = m.filePath.Update(msg)
// 	return m, cmd
// }

// type pickerModel struct {
// 	filepicker   filepicker.Model
// 	selectedFile string
// 	isInit       bool
// 	err          error
// }

// type clearErrorMsg struct{}

// func clearErrorAfter(t time.Duration) tea.Cmd {
// 	return tea.Tick(t, func(_ time.Time) tea.Msg {
// 		return clearErrorMsg{}
// 	})
// }

// func Picker() pickerModel {
// 	picker := filepicker.New()
// 	picker.AllowedTypes = []string{".torrent"}
// 	picker.CurrentDirectory, _ = os.UserHomeDir()
// 	picker.AutoHeight = true

// 	return pickerModel{filepicker: picker, selectedFile: "", isInit: true}
// }

// func (m pickerModel) Init() tea.Cmd {
// 	return tea.Batch(m.filepicker.Init(), tea.RequestWindowSize)
// }

// func (m model) pickerView() tea.View {
// 	var s strings.Builder
// 	s.WriteString("\n  ")
// 	if m.err != nil {
// 		s.WriteString(m.filepicker.Styles.DisabledFile.Render(m.err.Error()))
// 	} else if m.selectedFile == "" {
// 		s.WriteString("Pick a file:")
// 	} else {
// 		s.WriteString("Selected file: " + m.filepicker.Styles.Selected.Render(m.selectedFile))
// 	}
// 	s.WriteString("\n\n" + m.filepicker.View() + "\n")
// 	v := tea.NewView(s.String())
// 	v.AltScreen = true
// 	return v
// }

// func (m model) pickerUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
// 	switch msg := msg.(type) {
// 	case tea.KeyPressMsg:
// 		if msg.String() == "q" {
// 			return m, tea.Quit
// 		}
// 	case clearErrorMsg:
// 		m.err = nil
// 	}

// 	var cmd tea.Cmd
// 	m.filepicker, cmd = m.filepicker.Update(msg)

// 	// Did the user select a file?
// 	if didSelect, path := m.filepicker.DidSelectFile(msg); didSelect {
// 		// Get the path of the selected file.
// 		m.selectedFile = path
// 		return InitModel().SetFile(m.selectedFile)
// 	}

// 	// Did the user select a disabled file?
// 	// This is only necessary to display an error to the user.
// 	if didSelect, path := m.filepicker.DidSelectDisabledFile(msg); didSelect {
// 		// Let's clear the selectedFile and display an error.
// 		m.err = errors.New(path + " is not valid.")
// 		m.selectedFile = ""
// 		return m, tea.Batch(cmd, clearErrorAfter(2*time.Second))
// 	}

// 	return m, cmd
// }
