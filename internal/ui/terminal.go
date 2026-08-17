package ui

import (
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

const (
	keyTick = "__tick__"
)

type terminalState struct {
	inputs chan terminalInput
	ready  chan struct{}
	done   chan error
	width  atomic.Int32
	height atomic.Int32
	once   sync.Once
}

type Terminal struct {
	In      *os.File
	Out     io.Writer
	state   *terminalState
	program *tea.Program
	raw     bool
}

func NewTerminal(in *os.File, out io.Writer) *Terminal {
	return &Terminal{
		In: in, Out: out,
		state: &terminalState{inputs: make(chan terminalInput, 256), ready: make(chan struct{}), done: make(chan error, 1)},
	}
}

func (t *Terminal) EnterRaw() error {
	if t.raw {
		return nil
	}
	profile, _ := resolveColorProfile(t.Out)
	model := terminalModel{state: t.state}
	t.program = tea.NewProgram(model,
		tea.WithInput(t.In),
		tea.WithOutput(t.Out),
		tea.WithEnvironment(os.Environ()),
		tea.WithColorProfile(profile),
	)
	t.raw = true
	go func() {
		_, err := t.program.Run()
		t.state.done <- err
	}()
	select {
	case <-t.state.ready:
		return nil
	case err := <-t.state.done:
		t.raw = false
		if err == nil {
			return io.EOF
		}
		return err
	case <-time.After(2 * time.Second):
		// Some remote terminals do not report their size immediately. The
		// fallback in Size keeps the application usable until the first report.
		return nil
	}
}

func (t *Terminal) Restore() {
	if !t.raw || t.program == nil {
		return
	}
	t.program.Quit()
	t.program.Wait()
	t.raw = false
}

func (t *Terminal) Size() (int, int) {
	if width, height := int(t.state.width.Load()), int(t.state.height.Load()); width > 0 && height > 0 {
		return width, height
	}
	if width, height, err := term.GetSize(t.In.Fd()); err == nil && width > 0 && height > 0 {
		return width, height
	}
	width, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	height, _ := strconv.Atoi(os.Getenv("LINES"))
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (t *Terminal) ReadKey(timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case input := <-t.state.inputs:
		switch msg := input.msg.(type) {
		case tea.KeyPressMsg:
			return msg.String(), nil
		case tea.PasteMsg:
			return msg.Content, nil
		default:
			return keyTick, nil
		}
	case err := <-t.state.done:
		if err == nil {
			err = io.EOF
		}
		return "", err
	case <-timer.C:
		return keyTick, nil
	}
}

func (t *Terminal) ReadMsg(timeout time.Duration) (tea.Msg, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case input := <-t.state.inputs:
		return input.msg, nil
	case err := <-t.state.done:
		if err == nil {
			err = io.EOF
		}
		return nil, err
	case <-timer.C:
		return terminalTickMsg{}, nil
	}
}

func (t *Terminal) Run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			t.state.inputs <- terminalInput{msg: msg}
		}
	}()
}

func (t *Terminal) render(content string, cursorX, cursorY int, progress *tea.ProgressBar, force bool) {
	if t.program == nil {
		return
	}
	if force {
		t.invalidate()
	}
	t.program.Send(terminalRenderMsg{content: content, cursorX: cursorX, cursorY: cursorY, progress: progress})
}

func (t *Terminal) clear() {
	t.invalidate()
}

func (t *Terminal) invalidate() {
	if t.program != nil {
		t.program.Send(tea.ClearScreen())
		t.program.Send(terminalInvalidateMsg{})
	}
}

func (t *Terminal) Raw(sequence string) {
	if t.program != nil {
		t.program.Send(tea.RawMsg{Msg: sequence})
	}
}

func (t *Terminal) Query(sequence string, accept func(any) bool, timeout time.Duration) (any, error) {
	if t.program == nil {
		return nil, errors.New("terminal program is not running")
	}
	response := make(chan any, 1)
	t.program.Send(terminalQueryMsg{sequence: sequence, accept: accept, response: response})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-response:
		return msg, nil
	case err := <-t.state.done:
		if err == nil {
			err = io.EOF
		}
		return nil, err
	case <-timer.C:
		t.program.Send(terminalCancelQueryMsg{response: response})
		return nil, os.ErrDeadlineExceeded
	}
}

type terminalRenderMsg struct {
	content          string
	cursorX, cursorY int
	progress         *tea.ProgressBar
}

type terminalInput struct {
	msg tea.Msg
}

type terminalTickMsg struct{}

type terminalInvalidateMsg struct{}

type terminalQueryMsg struct {
	sequence string
	accept   func(any) bool
	response chan any
}

type terminalCancelQueryMsg struct {
	response chan any
}

type terminalModel struct {
	state    *terminalState
	content  string
	cursor   *tea.Cursor
	progress *tea.ProgressBar
	queries  []terminalQueryMsg
	revision bool
}

func (m terminalModel) Init() tea.Cmd { return tea.RequestWindowSize }

func (m terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.state.width.Store(int32(msg.Width))
		m.state.height.Store(int32(msg.Height))
		m.state.once.Do(func() { close(m.state.ready) })
	case tea.KeyPressMsg:
		m.enqueue(msg)
	case tea.PasteMsg:
		m.enqueue(msg)
	case terminalRenderMsg:
		m.content = msg.content
		m.progress = msg.progress
		if msg.cursorX >= 0 && msg.cursorY >= 0 {
			m.cursor = tea.NewCursor(msg.cursorX, msg.cursorY)
		} else {
			m.cursor = nil
		}
	case terminalInvalidateMsg:
		m.revision = !m.revision
	case terminalQueryMsg:
		m.queries = append(m.queries, msg)
		return m, tea.Raw(msg.sequence)
	case terminalCancelQueryMsg:
		for index, query := range m.queries {
			if query.response == msg.response {
				m.queries = append(m.queries[:index], m.queries[index+1:]...)
				break
			}
		}
	default:
		for index, query := range m.queries {
			if query.accept != nil && query.accept(msg) {
				query.response <- msg
				m.queries = append(m.queries[:index], m.queries[index+1:]...)
				break
			}
		}
	}
	return m, nil
}

func (m terminalModel) View() tea.View {
	content := m.content
	if m.revision {
		// A zero-width style reset makes the view observably different to the
		// Bubble Tea renderer without changing its cells. This lets an explicit
		// invalidation repaint content after unmanaged terminal graphics.
		content += xansi.ResetStyle
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "termcourse"
	view.Cursor = m.cursor
	view.ProgressBar = m.progress
	return view
}

func (m terminalModel) enqueue(msg tea.Msg) {
	select {
	case m.state.inputs <- terminalInput{msg: msg}:
	default:
		// Do not stall terminal rendering when a key-repeat burst outruns a
		// network-bound screen. A large buffer preserves normal interactive use.
	}
}

func isEOF(err error) bool { return errors.Is(err, io.EOF) }
