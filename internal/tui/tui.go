package tui

import (
	"fmt"
	session "gotorrent/internal/torrent"
	"gotorrent/internal/util"
	"image/color"
	"os"
	"strings"
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
	torrents      []*torrentModel
	form          *huh.Form
	styles        func(bool) *Styles
	hasDarkBg     bool
	width         int
	height        int
}

type torrentModel struct {
	SavePath string
	FilePath string
	Progress progress.Model
	Status   string
	Spinner  spinner.Model
	State    torrentState
	Err      string
	Torrent  *session.Session
	Id       int
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

type clearErrorMsg struct {
	id int
}

func clearErrorAfter(t time.Duration, id int) tea.Cmd {
	return tea.Tick(t, func(_ time.Time) tea.Msg {
		return clearErrorMsg{id: id}
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

	m.choices = []string{"downloads", "new", "magnet", "exit"}
	m.menuIndex = 1
	m.downloadIndex = 0
	//torrents, err := getCache()
	//if err == nil {
	//	m.torrents = torrents
	//}
	m.state = uiMain
	return m
}

func newFileForm() *huh.Form {
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

func newMagnetForm() *huh.Form {
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

			huh.NewInput().
				Key("file").
				Title("Magnet Link"),
		),
	)
	form.Init()
	return form
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.torrents {
		cmd := t.Progress.SetPercent(float64(len(t.Torrent.PiecesDone)) / float64(len(t.Torrent.PieceHashes)))
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
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
		cmd = m.torrents[msg.TorrentId].Progress.SetPercent(msg.Progress)
		return m, cmd

	case util.StatusMsg:
		var cmd tea.Cmd
		m.torrents[msg.TorrentId].Status = msg.Status
		return m, cmd

	case util.ErrorMsg:
		var cmd tea.Cmd
		cmd = clearErrorAfter(2*time.Second, msg.TorrentId)
		m.torrents[msg.TorrentId].Err = msg.Err
		return m, cmd

	case clearErrorMsg:
		m.torrents[msg.id].Err = ""
		return m, nil

	case progress.FrameMsg:
		var (
			cmd  tea.Cmd
			cmds []tea.Cmd
		)
		for _, t := range m.torrents {
			t.Progress, cmd = t.Progress.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var (
			cmd  tea.Cmd
			cmds []tea.Cmd
		)
		for _, t := range m.torrents {
			if t.State == torrentStarted {
				t.Spinner, cmd = t.Spinner.Update(msg)
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
	magnetButton := styles.blurredButton("Add Magnet Link")
	exitButton := styles.blurredButton("Exit")
	switch m.choices[m.menuIndex] {
	case "new":
		addButton = styles.focusedButton("Add Torrent")
	case "magnet":
		magnetButton = styles.focusedButton("Add Magnet Link")
	case "exit":
		exitButton = styles.focusedButton("Exit")
	}

	body := downloadView(m)

	footer := lipgloss.JoinHorizontal(lipgloss.Bottom, addButton, magnetButton, exitButton)
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
		torrent := torrentModel{SavePath: path, FilePath: file}
		torrent.initTorrent()
		m.torrents = append(m.torrents, &torrent)
		torrent.Id = len(m.torrents) - 1
		if strings.HasPrefix(file, "magnet") {
			go torrent.openMagnet(m, torrent.Id)
		} else {
			go torrent.openFile(m, torrent.Id)
		}

		//prog := torrent.Progress.SetPercent(float64(len(torrent.Torrent.PiecesDone)) / float64(len(torrent.Torrent.PieceHashes)))
		cmds = append(cmds, tea.RequestWindowSize)
		//torrent.createCacheTorrent()
		m.state = uiMain
	}

	return m, tea.Batch(cmds...)
}

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
				m.torrents[m.downloadIndex].downloadFile(m)
				cmd = func() tea.Msg {
					return m.torrents[m.downloadIndex].Spinner.Tick()
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

			return m, cmd
		}
	}

	return m, cmd
}

func mainUpdate(m model, msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.menuIndex == 0 {
		return downloadUpdate(m, msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "down", "left", "right", "enter":
			s := msg.String()
			if s == "enter" {
				switch m.choices[m.menuIndex] {
				case "new":
					var cmd tea.Cmd
					m.form = newFileForm()
					cmd = func() tea.Msg { return tea.RequestWindowSize() }
					m.state = uiForm
					return m, cmd
				case "magnet":
					var cmd tea.Cmd
					m.form = newMagnetForm()
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
				if m.menuIndex > 1 {
					m.menuIndex--
				}
			}
			if s == "right" {
				if m.menuIndex > 0 && m.menuIndex < len(m.choices)-1 {
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

	case progress.FrameMsg:
		for _, t := range m.torrents {
			var cmd tea.Cmd
			t.Progress, cmd = t.Progress.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		for _, t := range m.torrents {
			var cmd tea.Cmd
			t.Spinner, cmd = t.Spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (t *torrentModel) initTorrent() tea.Cmd {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	t.Spinner = s

	p := progress.New(progress.WithDefaultBlend())
	t.Progress = p
	t.Status = "Not Started"
	t.State = torrentStopped

	return t.Progress.Init()
}

func (t *torrentModel) progressView() string {
	var s string
	if t.State == torrentStarted {
		s = lipgloss.JoinHorizontal(lipgloss.Top, t.Progress.View(), "   ", t.Spinner.View(), t.Status)
	} else {
		s = lipgloss.JoinHorizontal(lipgloss.Top, t.Progress.View(), "   ", t.Status)
	}

	return s
}

func (t *torrentModel) torrentView(m model, i int) string {
	styles := m.styles(m.hasDarkBg)
	var button string
	var view string
	button = styles.blurredButton("Start")

	if m.menuIndex == 0 && i == m.downloadIndex {
		button = styles.focusedButton("Start")
	}
	var name string
	if t.Torrent != nil {
		name = t.Torrent.Name
	} else {
		name = "Loading Metadata"
	}
	info := lipgloss.JoinHorizontal(lipgloss.Top, name, "    ", button)
	if len(t.Err) > 0 {
		err := styles.ErrorHeaderText.Render(t.Err)
		view = lipgloss.JoinVertical(lipgloss.Top, info, t.progressView(), err)
	} else {
		view = lipgloss.JoinVertical(lipgloss.Top, info, t.progressView())
	}

	return view
}

func (t *torrentModel) openFile(m model, id int) tea.Msg {
	tf, err := session.OpenTorrent(t.FilePath, t.SavePath, m.program, id)
	if err != nil {
		return util.ErrorMsg{TorrentId: id, Err: t.Err}
	}
	t.Torrent = tf
	return t
}

func (t *torrentModel) openMagnet(m model, id int) tea.Msg {
	tf, err := session.OpenMagnet(t.FilePath, t.SavePath, m.program, id)
	if err != nil {
		return util.ErrorMsg{TorrentId: id, Err: t.Err}
	}
	t.Torrent = tf
	return t
}

//func (t *torrentModel) createCacheTorrent() error {
//	cache, err := os.UserCacheDir()
//	if err != nil {
//		return err
//	}
//	path := filepath.Join(cache, "cliTorrent")
//	if !util.Exists(path) {
//		util.MakeDir(path)
//	}
//	_, name := filepath.Split(t.FilePath)
//	f, err := os.Create(filepath.Join(path, name))
//	if err != nil {
//		return err
//	}
//	defer f.Close()
//	// c := session.BencodeTorrentModel{FilePath: t.FilePath, SavePath: t.SavePath}
//	// err = bencode.Marshal(f, c)
//	data, err := os.ReadFile(t.FilePath)
//	if err != nil {
//		return err
//	}
//	f.Write(data)
//	return nil
//}
//
//func getCache() ([]*torrentModel, error) {
//	cachePath, err := os.UserCacheDir()
//	if err != nil {
//		return nil, err
//	}
//	path := filepath.Join(cachePath, "cliTorrent")
//	if !util.Exists(path) {
//		return nil, err
//	}
//	var torrents []*torrentModel
//	cache, err := session.GetCachedTorrents()
//	if err != nil {
//		return torrents, err
//	}
//	for _, file := range cache {
//		// f, err := os.Open(filepath.Join(path, file.Name()))
//		// if err != nil {
//		// 	return nil, err
//		// }
//		torrent := torrentModel{}
//		// c := session.BencodeTorrentModel{}
//		// bencode.Unmarshal(f, &c)
//		torrent.initTorrent()
//		torrent.Torrent = file
//		torrent.SavePath = file.Path
//		torrents = append(torrents, &torrent)
//	}
//
//	return torrents, nil
//}

func (t *torrentModel) downloadFile(m model) tea.Msg {
	t.State = torrentStarted
	go func() {
		err := t.Torrent.StartDownload()
		if err != nil {
			t.Status = err.Error()
			t.Err = err.Error()
		}
		t.State = torrentFinished
	}()
	return m
}
