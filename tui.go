package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

var tuiModes = []string{"Balance", "Login + Hot Spin (until gift ends)"}

type tuiState int

const (
	tuiMenu tuiState = iota
	tuiInput
	tuiRunning
	tuiDone
)

type tuiField struct {
	key      string
	label    string
	isArea   bool
	password bool
}

type tuiInputs struct {
	numbers  textarea.Model
	password textinput.Model
	phone    textinput.Model
}

type tuiRuntime struct {
	send func(tea.Msg)
}

type tuiModel struct {
	state        tuiState
	mode         int
	cursor       int
	focus        int
	fields       []tuiField
	inputs       tuiInputs
	formErr      string
	logs         []string
	started      time.Time
	elapsed      time.Duration
	spinner      int
	width        int
	height       int
	runtime      *tuiRuntime
	quitting     bool
	done         int
	total        int
	numbersReady bool
	accounts     []account
	needPassword bool
}

type tuiLineMsg string
type tuiDoneMsg struct{}
type tuiTickMsg time.Time

var (
	tuiTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tuiSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	tuiDimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	tuiErrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	tuiLabelStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	tuiProgressRe    = regexp.MustCompile(`\((\d+)/(\d+)\)`)
	ansiRe           = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

func newTUIInputs() tuiInputs {
	mk := func(placeholder string, password bool) textinput.Model {
		t := textinput.New()
		t.Placeholder = placeholder
		t.CharLimit = 4096
		t.Width = 60
		t.Prompt = ""
		if password {
			t.EchoMode = textinput.EchoPassword
			t.EchoCharacter = '-'
		}
		return t
	}
	ta := textarea.New()
	ta.Placeholder = "CSV: phone,password"
	ta.SetWidth(60)
	ta.SetHeight(6)
	ta.CharLimit = 0
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.FocusedStyle.Base = lipgloss.NewStyle().Padding(0, 1)
	ta.BlurredStyle.Base = lipgloss.NewStyle().Padding(0, 1)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	return tuiInputs{
		numbers:  ta,
		password: mk("Password for rows without one", true),
		phone:    mk("Phone number", false),
	}
}

func newTUIModel() tuiModel {
	return tuiModel{
		state:   tuiMenu,
		inputs:  newTUIInputs(),
		runtime: &tuiRuntime{send: func(tea.Msg) {}},
		width:   100,
		height:  30,
	}
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m *tuiModel) blurAll() {
	m.inputs.numbers.Blur()
	m.inputs.password.Blur()
	m.inputs.phone.Blur()
}

func (m *tuiModel) fieldForKey(key string) *textinput.Model {
	switch key {
	case "password":
		return &m.inputs.password
	case "phone":
		return &m.inputs.phone
	}
	return nil
}

func (m *tuiModel) currentField() tuiField {
	if len(m.fields) == 0 {
		return tuiField{}
	}
	return m.fields[m.focus]
}

func (m *tuiModel) focusCurrent() tea.Cmd {
	m.blurAll()
	f := m.currentField()
	if f.isArea {
		return m.inputs.numbers.Focus()
	}
	if ti := m.fieldForKey(f.key); ti != nil {
		return ti.Focus()
	}
	return nil
}

func (m *tuiModel) parseAccountsInput() {
	raw := strings.TrimSpace(m.inputs.numbers.Value())
	m.accounts = parseAccounts(raw)
	m.needPassword = false
	for _, a := range m.accounts {
		if a.Password == "" {
			m.needPassword = true
		}
	}
	m.numbersReady = len(m.accounts) > 0
}

func (m *tuiModel) computeFields() []tuiField {
	return []tuiField{{key: "numbers", label: "Accounts CSV", isArea: true}}
}

func (m *tuiModel) buildFields() tea.Cmd {
	m.blurAll()
	m.fields = m.computeFields()
	m.focus = 0
	return m.focusCurrent()
}

func (m *tuiModel) refreshFields() {
	m.fields = m.computeFields()
	if m.focus >= len(m.fields) && len(m.fields) > 0 {
		m.focus = len(m.fields) - 1
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width - 10
		if w > 90 {
			w = 90
		}
		if w < 20 {
			w = 20
		}
		m.inputs.password.Width = w
		m.inputs.phone.Width = w
		m.inputs.numbers.SetWidth(w)
		return m, nil
	case tuiLineMsg:
		line := ansiRe.ReplaceAllString(strings.TrimRight(string(msg), "\r"), "")
		m.logs = append(m.logs, line)
		if len(m.logs) > 5000 {
			m.logs = m.logs[len(m.logs)-5000:]
		}
		if matches := tuiProgressRe.FindAllStringSubmatch(line, -1); len(matches) > 0 {
			last := matches[len(matches)-1]
			if n, err := strconv.Atoi(last[1]); err == nil {
				m.done = n
			}
			if n, err := strconv.Atoi(last[2]); err == nil {
				m.total = n
			}
		}
		return m, nil
	case tuiDoneMsg:
		m.state = tuiDone
		m.elapsed = time.Since(m.started)
		return m, nil
	case tuiTickMsg:
		if m.state == tuiRunning {
			m.spinner++
			m.elapsed = time.Since(m.started)
			return m, tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return tuiTickMsg(t) })
		}
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+z" {
			m.quitting = true
			return m, tea.Quit
		}
		switch m.state {
		case tuiMenu:
			return m.updateMenu(msg)
		case tuiInput:
			return m.updateInput(msg)
		case tuiRunning:
			if msg.String() == "ctrl+c" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		case tuiDone:
			switch msg.String() {
			case "esc":
				m.state = tuiMenu
				m.logs = nil
				m.formErr = ""
				return m, nil
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m tuiModel) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(tuiModes)-1 {
			m.cursor++
		}
	case "enter":
		m.mode = m.cursor
		m.state = tuiInput
		m.formErr = ""
		m.logs = nil
		m.parseAccountsInput()
		cmd := m.buildFields()
		return m, cmd
	case "esc", "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.state = tuiMenu
		m.blurAll()
		m.formErr = ""
		return m, nil
	case "ctrl+s":
		return m.startJob()
	case "tab":
		if m.currentField().key == "numbers" && !m.numbersReady {
			m.formErr = "Drop a CSV or paste at least one account"
			return m, nil
		}
		if len(m.fields) > 0 {
			m.focus = (m.focus + 1) % len(m.fields)
			return m, m.focusCurrent()
		}
		return m, nil
	case "enter":
		if m.currentField().isArea {
			if !strings.Contains(m.inputs.numbers.Value(), "\n") {
				if !m.numbersReady {
					m.formErr = "Drop a CSV or paste at least one account"
					return m, nil
				}
				return m.startJob()
			}
		} else {
			return m.startJob()
		}
	}
	if m.currentField().isArea {
		var cmd tea.Cmd
		m.inputs.numbers, cmd = m.inputs.numbers.Update(msg)
		m.parseAccountsInput()
		m.formErr = ""
		m.refreshFields()
		return m, cmd
	}
	if ti := m.fieldForKey(m.currentField().key); ti != nil {
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) startJob() (tea.Model, tea.Cmd) {
	m.formErr = ""
	m.done = 0
	m.total = 0
	m.blurAll()

	raw := strings.TrimSpace(m.inputs.numbers.Value())
	if raw == "" {
		m.formErr = "Drop a CSV or paste account rows"
		return m, nil
	}
	m.accounts = parseAccounts(raw)
	if len(m.accounts) == 0 {
		m.formErr = "No accounts found in that input"
		return m, nil
	}
	for i := range m.accounts {
		if m.accounts[i].Password == "" {
			m.accounts[i].Password = defaultPassword(m.accounts[i].Phone, "")
		}
	}

	m.state = tuiRunning
	m.logs = nil
	m.started = time.Now()
	m.elapsed = 0
	m.spinner = 0

	pr, pw := io.Pipe()
	setJobOutput(pw)
	go func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			m.runtime.send(tuiLineMsg(scanner.Text()))
		}
	}()
	go func() {
		modeName := []string{"balance", "login"}[m.mode]
		runBatch(m.accounts, modeName)
		pw.Close()
		setJobOutput(os.Stdout)
		m.runtime.send(tuiDoneMsg{})
	}()
	return m, tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return tuiTickMsg(t) })
}

func (m tuiModel) View() string {
	if m.quitting {
		return ""
	}
	switch m.state {
	case tuiMenu:
		return m.viewMenu()
	case tuiInput:
		return m.viewInput()
	case tuiRunning:
		return m.viewRunning()
	default:
		return m.viewDone()
	}
}

func (m tuiModel) viewMenu() string {
	var b strings.Builder
	b.WriteString(tuiTitleStyle.Render("GAMEMANIA"))
	b.WriteString("\n")
	b.WriteString(tuiDimStyle.Render("Select a mode"))
	b.WriteString("\n\n")
	for i, mode := range tuiModes {
		if i == m.cursor {
			b.WriteString(tuiSelectedStyle.Render("> " + mode))
		} else {
			b.WriteString("  " + mode)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if len(proxyURLs) == 0 {
		b.WriteString(tuiDimStyle.Render("direct connection - no proxies"))
	} else {
		b.WriteString(tuiDimStyle.Render(fmt.Sprintf("direct first - %d proxy sessions available", len(proxyURLs))))
	}
	b.WriteString("\n")
	b.WriteString(tuiDimStyle.Render("up/down: choose - enter: continue - esc/q/ctrl+z: quit"))
	return b.String()
}

func (m tuiModel) viewInput() string {
	var b strings.Builder
	b.WriteString(tuiTitleStyle.Render(tuiModes[m.mode]))
	b.WriteString("\n\n")
	if m.formErr != "" {
		b.WriteString(tuiErrStyle.Render("! " + m.formErr))
		b.WriteString("\n\n")
	}
	for i, f := range m.fields {
		label := f.label
		if i == m.focus {
			label = tuiLabelStyle.Render("> " + label)
		} else {
			label = "  " + label
		}
		b.WriteString(label)
		b.WriteString("\n")
		if f.isArea {
			b.WriteString(m.inputs.numbers.View())
		}
		b.WriteString("\n")
		b.WriteString(tuiDimStyle.Render("  CSV columns: phone,password. Include passwords to skip extra steps."))
		b.WriteString("\n\n")
	}
	b.WriteString(tuiDimStyle.Render("tab: next - ctrl+s: start - esc: back - ctrl+z: stop"))
	return b.String()
}

func (m tuiModel) viewRunning() string {
	frames := []string{"|", "/", "-", "\\"}
	frame := frames[m.spinner%len(frames)]
	progress := ""
	if m.total > 0 {
		pct := float64(m.done) * 100 / float64(m.total)
		progress = fmt.Sprintf("  %d/%d (%.0f%%)", m.done, m.total, pct)
	}
	head := fmt.Sprintf("%s %s  %.1fs  %d lines%s",
		tuiTitleStyle.Render(tuiModes[m.mode]), frame, m.elapsed.Seconds(), len(m.logs), progress)
	return head + "\n\n" + m.tailLines() + "\n\n" + tuiDimStyle.Render("ctrl+z / ctrl+c: stop")
}

func (m tuiModel) viewDone() string {
	extra := ""
	if m.total > 0 {
		extra = fmt.Sprintf("  (%d/%d)", m.done, m.total)
	}
	head := fmt.Sprintf("%s OK  completed in %.1fs%s", tuiTitleStyle.Render(tuiModes[m.mode]), m.elapsed.Seconds(), extra)
	return head + "\n\n" + m.tailLines() + "\n\n" + tuiDimStyle.Render("esc: menu - q/ctrl+z: quit")
}

func (m tuiModel) tailLines() string {
	limit := m.height - 8
	if limit < 5 {
		limit = 5
	}
	start := len(m.logs) - limit
	if start < 0 {
		start = 0
	}
	return strings.Join(m.logs[start:], "\n")
}

func runTUI() error {
	m := newTUIModel()
	rt := m.runtime
	p := tea.NewProgram(m, tea.WithAltScreen())
	rt.send = p.Send
	_, err := p.Run()
	return err
}

var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) string {
	fmt.Print(prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil || strings.ContainsRune(line, '\x1a') {
		fmt.Println()
		os.Exit(0)
	}
	return strings.TrimSpace(line)
}

func runCLI() {
	stdinReader = bufio.NewReader(os.Stdin)
	for {
		fmt.Println()
		fmt.Println(boldColor + cyanColor + "=== GAMEMANIA ===" + resetColor)
		fmt.Println(greenColor + "1. Balance" + resetColor)
		fmt.Println(greenColor + "2. Login + Hot Spin (until gift ends)" + resetColor)
		fmt.Println("0. Exit")
		choice := readLine("Select option: ")
		switch choice {
		case "0", "":
			return
		case "1", "2":
			accounts := readAccountsInteractive()
			if len(accounts) == 0 {
				fmt.Println("No accounts found.")
				continue
			}
			mode := "balance"
			if choice == "2" {
				mode = "login"
			}
			runBatch(accounts, mode)
		default:
			fmt.Println("Invalid option")
		}
	}
}

func readAccountsInteractive() []account {
	path := strings.TrimSpace(strings.Trim(readLine("Drag/drop a CSV file or press Enter to paste: "), "\"'"))
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if acc := parseAccounts(string(b)); len(acc) > 0 {
				return acc
			}
		}
		fmt.Println("Could not read that file. Switching to paste mode.")
	}
	fmt.Println("Paste rows (phone,password). Blank line to finish:")
	var lines []string
	for {
		line := readLine("")
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	return parseAccounts(strings.Join(lines, "\n"))
}

func main() {
	setConsoleIcon()
	loadProxies()
	if isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()) {
		if err := runTUI(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	runCLI()
}
