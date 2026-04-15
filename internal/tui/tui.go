package tui

import (
	"fmt"
	torrentFile "gotorrent/internal/torrentfile"
	"gotorrent/internal/util"
	"image/color"
	"os"
	"path"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type model struct {
	choices       []string
	menuIndex     int
	downloadIndex int
	state         uiState
	program       *tea.Program
	err           error
	torrents      []*torrent
	form          *huh.Form
	styles        func(bool) *Styles
	hasDarkBg     bool
	width         int
	height        int
}

type torrent struct {
	path     string
	file     string
	progress progress.Model
	status   string
	spinner  spinner.Model
	state    torrentState
}

const (
	padding = 2
)

type torrentState int

const (
	torrentStopped  torrentState = 0
	torrentStarted  torrentState = 1
	torrentFinished torrentState = 2
)

type uiState int

const (
	uiMain     uiState = 0
	uiPicker   uiState = 1
	uiDownload uiState = 2
	uiForm     uiState = 3
)

type clearErrorMsg struct{}

func clearErrorAfter(t time.Duration) tea.Cmd {
	return tea.Tick(t, func(_ time.Time) tea.Msg {
		return clearErrorMsg{}
	})
}

type Styles struct {
	Base,
	BlurredStyle,
	BorderStyle,
	FocusedStyle,
	HeaderText,
	Status,
	StatusHeader,
	Highlight,
	ErrorHeaderText,
	Help lipgloss.Style

	BlurredButton,
	FocusedButton,
	Header string

	Red, Indigo, Green color.Color
}

func NewStyles(hasDarkBg bool) *Styles {
	var (
		s         = Styles{}
		lightDark = lipgloss.LightDark(hasDarkBg)
	)

	s.Red = lightDark(lipgloss.Color("#FE5F86"), lipgloss.Color("#FE5F86"))
	s.Indigo = lightDark(lipgloss.Color("#5A56E0"), lipgloss.Color("#7571F9"))
	s.Green = lightDark(lipgloss.Color("#02BA84"), lipgloss.Color("#02BF87"))
	s.Base = lipgloss.NewStyle().
		Padding(1, 4, 0, 1)
	s.HeaderText = lipgloss.NewStyle().
		Foreground(s.Indigo).
		Bold(true).
		Padding(0, 1, 0, 2)
	s.Status = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(s.Indigo).
		PaddingLeft(1).
		MarginTop(1)
	s.StatusHeader = lipgloss.NewStyle().
		Foreground(s.Green).
		Bold(true)
	s.Highlight = lipgloss.NewStyle().
		Foreground(lipgloss.Color("212"))
	s.ErrorHeaderText = s.HeaderText.
		Foreground(s.Red)
	s.Help = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))
	s.BorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	s.FocusedStyle = lipgloss.NewStyle().Foreground(s.Red)
	s.BlurredStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	s.Header = s.HeaderText.Render("Welcome To cliTorrent!\n\n")

	return &s
}

func (s *Styles) focusedButton(str string) string {
	return fmt.Sprintf("[ %s ]", s.FocusedStyle.Render(str))
}

func (s *Styles) blurredButton(str string) string {
	return fmt.Sprintf("[ %s ]", s.BlurredStyle.Render(str))
}

func initModel() model {
	m := model{styles: NewStyles}

	//progress := progress.New(progress.WithDefaultBlend())

	m.choices = []string{"downloads", "new", "exit"}
	m.menuIndex = 1
	m.downloadIndex = 0
	m.state = uiState(uiMain)
	return m
}

func newForm() *huh.Form {
	p, _ := os.UserHomeDir()
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewFilePicker().
				Key("path").
				Title("Download Location").
				Description("Select a save location").
				DirAllowed(true).
				FileAllowed(false).
				CurrentDirectory(p),

			huh.NewFilePicker().
				Key("file").
				Title("Filename").
				Description("Select a .torrent file").
				AllowedTypes([]string{".torrent"}).
				CurrentDirectory(p),
		),
	)
	form.Init()
	return form
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	styles := m.styles(m.hasDarkBg)
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.hasDarkBg = msg.IsDark()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.form != nil {
			m.form.WithHeight(min(msg.Height, 20) - styles.Base.GetHorizontalFrameSize()).WithWidth(msg.Width - styles.Base.GetHorizontalFrameSize())
		}

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

	case util.ProgressMsg:
		var cmd tea.Cmd
		if msg.Progress > 0 {
			cmd = m.torrents[msg.TorrentId].progress.SetPercent(msg.Progress)
		}
		if len(msg.Message) > 0 {
			m.torrents[msg.TorrentId].status = msg.Message
		}
		return m, cmd

	case progress.FrameMsg:
		var (
			cmd  tea.Cmd
			cmds []tea.Cmd
		)
		for _, t := range m.torrents {
			if t.state == torrentStarted {
				t.progress, cmd = t.progress.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var (
			cmd  tea.Cmd
			cmds []tea.Cmd
		)
		for _, t := range m.torrents {
			if t.state == torrentStarted {
				t.spinner, cmd = t.spinner.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		return m, tea.Batch(cmds...)
	}

	switch m.state {
	case uiForm:
		return formUpdate(m, msg)
	default:
		return mainUpdate(m, msg)
	}
}

func (m model) View() tea.View {
	styles := m.styles(m.hasDarkBg)

	addButton := styles.blurredButton("Add Torrent")
	exitButton := styles.blurredButton("Exit")
	switch m.choices[m.menuIndex] {
	case "new":
		addButton = styles.focusedButton("Add Torrent")
	case "exit":
		exitButton = styles.focusedButton("Exit")
	}

	body := downloadView(m)

	footer := lipgloss.JoinHorizontal(lipgloss.Bottom, addButton, exitButton)
	s := lipgloss.JoinVertical(lipgloss.Top, styles.Header, styles.BorderStyle.Width(m.width).Render(body), footer)

	if m.state == uiForm {
		popUp := lipgloss.NewLayer(
			lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				Align(lipgloss.Center, lipgloss.Center).
				Render(m.form.View()),
		).X(m.width/2 - lipgloss.Width(m.form.View())/2)
		comp := lipgloss.NewCompositor(lipgloss.NewLayer(s), popUp)
		s = comp.Render()
	}

	view := tea.NewView(s)
	view.AltScreen = true

	return view
}

func Run() (*tea.Program, error) {
	m := initModel()
	p := tea.NewProgram(&m)
	m.program = p

	if _, err := p.Run(); err != nil {
		return p, err
	}
	return p, nil
}

func formUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Process the form
	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
		cmds = append(cmds, cmd)
	}

	if m.form.State == huh.StateCompleted {
		path := m.form.GetString("path") + "/"
		file := m.form.GetString("file")
		torrent := torrent{path: path, file: file}
		torrent.initTorrent()
		m.torrents = append(m.torrents, &torrent)
		m.state = uiMain
	}

	return m, tea.Batch(cmds...)
}

// func pickerView(m model) string {
// 	var s strings.Builder
// 	s.WriteString("\n  ")
// 	if m.err != nil {
// 		s.WriteString(m.filePicker.Styles.DisabledFile.Render(m.err.Error()))
// 	} else if m.selectedFile == "" {
// 		s.WriteString("Pick a file:")
// 	} else {
// 		s.WriteString("Selected file: " + m.filePicker.Styles.Selected.Render(m.selectedFile))
// 	}
// 	s.WriteString("\n\n" + m.filePicker.View() + "\n")

// 	return s.String()
// }

// func pickerUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
// 	var cmd tea.Cmd
// 	switch msg := msg.(type) {
// 	case tea.KeyPressMsg:
// 		if msg.String() == "q" {
// 			m.state = Main
// 			return m, cmd
// 		}
// 	case clearErrorMsg:
// 		m.err = nil
// 	}

// 	m.filePicker, cmd = m.filePicker.Update(msg)

// 	// Did the user select a file?
// 	if didSelect, path := m.filePicker.DidSelectFile(msg); didSelect {
// 		// Get the path of the selected file.
// 		m.selectedFile = path
// 		m.state = Main
// 		return m, cmd
// 	}

// 	// Did the user select a disabled file?
// 	// This is only necessary to display an error to the user.
// 	if didSelect, path := m.filePicker.DidSelectDisabledFile(msg); didSelect {
// 		// Let's clear the selectedFile and display an error.
// 		m.err = errors.New(path + " is not valid.")
// 		m.selectedFile = ""
// 		return m, tea.Batch(cmd, clearErrorAfter(2*time.Second))
// 	}

// 	return m, cmd
// }

func downloadView(m model) string {
	downloads := []string{}

	for i, torrent := range m.torrents {
		downloads = append(downloads, torrent.torrentView(m, i))
	}
	return lipgloss.JoinVertical(lipgloss.Top, downloads...)
}

func downloadUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		// case "ctrl+c":
		// 	return m, tea.Quit

		case "up", "down", "left", "right", "enter":
			s := msg.String()
			if s == "enter" {
				m.torrents[m.downloadIndex].downloadFile(m, m.downloadIndex)
				cmd = func() tea.Msg {
					return m.torrents[m.downloadIndex].spinner.Tick()
				}
			}
			if s == "up" {
				if m.downloadIndex > 0 {
					m.downloadIndex--
				}
			}
			if s == "down" {
				if m.downloadIndex < len(m.torrents)-1 {
					m.downloadIndex++
				} else if m.downloadIndex == len(m.torrents)-1 {
					m.menuIndex++
				}
			}
			// if s == "left" {
			// 	if m.menuIndex == len(m.torrents)+len(m.choices)-1 {
			// 		m.menuIndex--
			// 	}
			// }
			// if s == "right" {
			// 	if m.menuIndex == len(m.torrents)+len(m.choices)-2 {
			// 		m.menuIndex++
			// 	}
			// }

			return m, cmd
		}
	}

	return m, cmd
}

// func mainView(m model) string {
// 	styles := m.styles(m.hasDarkBg)
// 	// _, file := path.Split(m.file)
// 	// path := m.path

// 	addButton := styles.blurredButton("Add Torrent")
// 	exitButton := styles.blurredButton("Exit")
// 	switch m.choices[m.chosenIndex] {
// 	case "new":
// 		addButton = styles.focusedButton("Add Torrent")
// 	case "exit":
// 		exitButton = styles.focusedButton("Exit")
// 	}

// 	footer := lipgloss.JoinHorizontal(lipgloss.Bottom, addButton, exitButton)
// 	s := lipgloss.JoinVertical(lipgloss.Top, styles.Header, styles.BorderStyle.Width(m.width).Render(body), footer)
// 	// v := tea.NewView(s)
// 	// var c *tea.Cursor
// 	// if m.filePath.Focused() {
// 	// 	c = m.filePath.Cursor()
// 	// 	c.Y += lipgloss.Height(header)
// 	// 	v.Cursor = c
// 	// }

// 	if m.state == Form {
// 		popUp := lipgloss.NewLayer(
// 			lipgloss.NewStyle().
// 				Border(lipgloss.RoundedBorder()).
// 				Align(lipgloss.Center, lipgloss.Center).
// 				Render(m.form.View()),
// 		).X(m.width/2 - lipgloss.Width(m.form.View())/2)
// 		comp := lipgloss.NewCompositor(lipgloss.NewLayer(s), popUp)
// 		return comp.Render()
// 	}

// 	return s
// }

func mainUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.menuIndex == 0 {
		return downloadUpdate(m, msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		// case "ctrl+c":
		// 	return m, tea.Quit
		case "up", "down", "left", "right", "enter":
			s := msg.String()
			if s == "enter" {
				switch m.choices[m.menuIndex] {
				case "new":
					var cmd tea.Cmd
					m.form = newForm()
					cmd = func() tea.Msg { return tea.RequestWindowSize() }
					m.state = uiForm
					return m, cmd
				case "exit":
					return m, tea.Quit
				}
			}
			if s == "up" {
				if m.menuIndex > 0 && len(m.torrents) > 0 {
					m.downloadIndex = len(m.torrents) - 1
					m.menuIndex = 0
				}
			}
			if s == "down" {
				if m.menuIndex == 0 {
					m.menuIndex++
				}
			}
			if s == "left" {
				if m.menuIndex == 2 {
					m.menuIndex--
				}
			}
			if s == "right" {
				if m.menuIndex == 1 {
					m.menuIndex++
				}
			}

			return m, cmd
		}
	}
	return m, cmd
}

func progressUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{}
	switch msg := msg.(type) {
	// case tea.WindowSizeMsg:
	// 	t.progress.SetWidth(msg.Width - padding*2 - 4)
	// 	return m, nil

	// case util.ProgressMsg:
	// 	var cmd tea.Cmd
	// 	if msg.Progress > 0 {
	// 		cmd = t.progress.SetPercent(msg.Progress)
	// 	}
	// 	if len(msg.Message) > 0 {
	// 		t.status = msg.Message
	// 	}
	// 	return m, cmd

	case progress.FrameMsg:
		for _, t := range m.torrents {
			var cmd tea.Cmd
			t.progress, cmd = t.progress.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		for _, t := range m.torrents {
			var cmd tea.Cmd
			t.spinner, cmd = t.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (t *torrent) initTorrent() tea.Cmd {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	t.spinner = s

	p := progress.New(progress.WithDefaultBlend())
	t.progress = p
	t.status = "Not Started"
	t.state = torrentStopped
	return t.progress.Init()
}

func (t *torrent) progressView() string {
	s := lipgloss.JoinHorizontal(lipgloss.Top, t.spinner.View(), t.progress.View(), "   ", t.status)
	return s
}

func (t *torrent) torrentView(m model, i int) string {
	_, name := path.Split(t.file)
	styles := m.styles(m.hasDarkBg)
	var button string
	button = styles.blurredButton("Start")

	if m.menuIndex == 0 && i == m.downloadIndex {
		button = styles.focusedButton("Start")
	}

	info := lipgloss.JoinHorizontal(lipgloss.Top, name, "    ", button)
	return lipgloss.JoinVertical(lipgloss.Top, info, t.progressView())
}

func (t *torrent) downloadFile(m model, id int) tea.Msg {
	tf, err := torrentFile.Open(t.file)
	if err != nil {
		return tea.Quit()
	}
	t.state = torrentStarted
	go func() {
		err = tf.DownloadToFile(t.path, m.program, id)
		if err != nil {
			t.status = err.Error()
		}
		t.state = torrentFinished
	}()
	if err != nil {
		return tea.Quit()
	}
	return m
}
