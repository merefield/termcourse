package ui

import (
	"math"
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	startupFrameInterval = time.Second / 30
	startupEntrance      = 900 * time.Millisecond
	startupHold          = 180 * time.Millisecond
	startupDock          = 800 * time.Millisecond
	startupDuration      = startupEntrance + startupHold + startupDock
	startupBlinkHalf     = 180 * time.Millisecond
	startupBlinks        = 3
	startupTypingStep    = 90 * time.Millisecond
	startupShuffleStep   = 110 * time.Millisecond
	startupShuffleFrame  = 70 * time.Millisecond
	startupTitle         = "TERMCOURSE"
	startupAlphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

type startupAnimation int

const (
	startupSlide startupAnimation = iota
	startupTyping
	startupShuffle
	startupAnimationCount
)

type startupState struct {
	pending   bool
	started   time.Time
	elapsed   time.Duration
	animation startupAnimation
	seed      uint64
	order     [len(startupTitle)]int
}

func (s startupState) active() bool { return !s.started.IsZero() }

type startupFrameMsg struct{ started, at time.Time }

func startupTick(started time.Time) tea.Cmd {
	return tea.Tick(startupFrameInterval, func(now time.Time) tea.Msg {
		return startupFrameMsg{started: started, at: now}
	})
}

func (m *terminalModel) beginStartup(now time.Time) tea.Cmd {
	// Resize recomputes geometry, but never restarts or adds a second tick chain.
	if m.width < 68 || m.height < 8 {
		if m.width > 0 && m.height > 0 {
			m.startup = startupState{}
		}
		return nil
	}
	if !m.startup.pending {
		return nil
	}
	m.startup = newStartupState(now, startupAnimation(rand.IntN(int(startupAnimationCount))), rand.Uint64())
	return startupTick(now)
}

func (m terminalModel) updateStartup(message tea.Msg) (terminalModel, tea.Cmd, bool) {
	if tick, ok := message.(startupFrameMsg); ok {
		if !m.startup.active() || tick.started != m.startup.started {
			return m, nil, true
		}
		// Wall-clock progress catches up after slow frames instead of delaying startup.
		m.startup.elapsed = max(m.startup.elapsed, tick.at.Sub(m.startup.started))
		if m.startup.elapsed >= m.startup.duration() {
			m.startup = startupState{}
			return m, nil, true
		}
		return m, startupTick(m.startup.started), true
	}
	if !m.startup.active() {
		return m, nil, false
	}
	switch message := message.(type) {
	case tea.KeyPressMsg:
		m.startup = startupState{}
		if message.String() == "ctrl+c" || strings.EqualFold(message.String(), "q") {
			return m, tea.Quit, true
		}
		// Consume the skip key so it cannot activate an unseen forum control.
		return m, nil, true
	case tea.PasteMsg:
		m.startup = startupState{}
		return m, nil, true
	case tea.MouseMsg:
		if click, ok := message.(tea.MouseClickMsg); ok && click.Mouse().Button == tea.MouseLeft {
			m.startup = startupState{}
		}
		return m, nil, true
	}
	// Incoming frames and terminal queries keep their normal handlers.
	return m, nil, false
}

func newStartupState(now time.Time, animation startupAnimation, seed uint64) startupState {
	s := startupState{started: now, animation: animation, seed: seed}
	random := rand.New(rand.NewPCG(seed, ^seed))
	copy(s.order[:], random.Perm(len(startupTitle)))
	return s
}

func (s startupState) entranceDuration() time.Duration {
	switch s.animation {
	case startupTyping:
		return startupBlinkHalf*2*startupBlinks + startupTypingStep*time.Duration(len(startupTitle))
	case startupShuffle:
		return startupShuffleStep * time.Duration(len(startupTitle))
	default:
		return startupEntrance
	}
}

func (s startupState) duration() time.Duration {
	return s.entranceDuration() + startupHold + startupDock
}

// Keep random content stable within a frame and across redraws/resizes. Only
// elapsed time advances the effect; rendering never consumes global randomness.
func (s startupState) lettering() (text string, cursor int, dot bool) {
	elapsed := max(s.elapsed, 0)
	switch s.animation {
	case startupTyping:
		blinkDuration := startupBlinkHalf * 2 * startupBlinks
		typed := 0
		if elapsed < blinkDuration {
			dot = (elapsed/startupBlinkHalf)%2 == 0
		} else {
			typed = min(int((elapsed-blinkDuration)/startupTypingStep)+1, len(startupTitle))
			dot = typed < len(startupTitle)
		}
		return startupTitle[:typed] + strings.Repeat(" ", len(startupTitle)-typed), typed, dot
	case startupShuffle:
		locked := min(int(elapsed/startupShuffleStep), len(startupTitle))
		characters := []byte(startupTitle)
		random := rand.New(rand.NewPCG(s.seed, uint64(elapsed/startupShuffleFrame)))
		for index := range characters {
			character := byte(0)
			// Unresolved slots stay visibly unresolved, even when a random draw happens
			// to equal their target. Locked positions never change again.
			for character == 0 || character == startupTitle[index] {
				character = startupAlphabet[random.IntN(len(startupAlphabet))]
			}
			characters[index] = character
		}
		for _, position := range s.order[:locked] {
			characters[position] = startupTitle[position]
		}
		return string(characters), -1, false
	default:
		return startupTitle, -1, false
	}
}

// The header font uses three columns per letter except its five-column M.
// Reserve these same slots for every variant so typing and random glyphs cannot
// change the wordmark's scale, spacing or final geometry.
type startupLetterSlot struct{ start, width int }

var startupLetterSlots = func() (slots []startupLetterSlot) {
	column := 0
	for _, letter := range startupTitle {
		width := 3
		if letter == 'M' {
			width = 5
		}
		slots = append(slots, startupLetterSlot{column, width})
		column += width + 1
	}
	return slots
}()

var startupRandomGlyphs = map[byte][4]string{
	'A': {"010", "101", "111", "101"}, 'B': {"110", "111", "101", "110"},
	'C': {"111", "100", "100", "111"}, 'D': {"110", "101", "101", "110"},
	'E': {"111", "100", "110", "111"}, 'F': {"111", "100", "110", "100"},
	'G': {"111", "100", "101", "111"}, 'H': {"101", "101", "111", "101"},
	'I': {"111", "010", "010", "111"}, 'J': {"001", "001", "101", "111"},
	'K': {"101", "110", "110", "101"}, 'L': {"100", "100", "100", "111"},
	'M': {"101", "111", "101", "101"}, 'N': {"101", "111", "111", "101"},
	'O': {"111", "101", "101", "111"}, 'P': {"111", "101", "111", "100"},
	'Q': {"111", "101", "111", "001"}, 'R': {"111", "101", "110", "101"},
	'S': {"111", "100", "001", "111"}, 'T': {"111", "010", "010", "010"},
	'U': {"101", "101", "101", "111"}, 'V': {"101", "101", "101", "010"},
	'W': {"101", "101", "111", "101"}, 'X': {"101", "010", "010", "101"},
	'Y': {"101", "101", "010", "010"}, 'Z': {"111", "001", "100", "111"},
	'0': {"111", "101", "101", "111"}, '1': {"110", "010", "010", "111"},
	'2': {"111", "001", "110", "111"}, '3': {"111", "011", "001", "111"},
	'4': {"101", "101", "111", "001"}, '5': {"111", "110", "001", "111"},
	'6': {"100", "111", "101", "111"}, '7': {"111", "001", "010", "010"},
	'8': {"111", "111", "101", "111"}, '9': {"111", "101", "111", "001"},
}

func (s startupState) bitmap() [][]bool {
	text, cursor, dot := s.lettering()
	if text == startupTitle && !dot {
		return startupLogoBitmap
	}
	pixels := make([][]bool, len(startupLogoBitmap))
	for row := range pixels {
		pixels[row] = make([]bool, len(startupLogoBitmap[row]))
		for position, slot := range startupLetterSlots {
			for col := range slot.width {
				if text[position] == startupTitle[position] {
					pixels[row][slot.start+col] = startupLogoBitmap[row][slot.start+col]
				} else if text[position] != ' ' {
					glyph := startupRandomGlyphs[text[position]]
					pixels[row][slot.start+col] = glyph[row][col*len(glyph[row])/slot.width] == '1'
				}
			}
		}
	}
	if dot && cursor < len(startupLetterSlots) {
		slot := startupLetterSlots[cursor]
		pixels[len(pixels)-1][slot.start+slot.width/2] = true
	}
	return pixels
}

// Decode the existing two-row half-block lettering into four pixel rows. The
// same bitmap scales up for the entrance and returns exactly to the header art.
func startupLogoPixels() [][]bool {
	width := len([]rune(headerLogo[0]))
	pixels := make([][]bool, len(headerLogo)*2)
	for row, line := range headerLogo {
		pixels[row*2], pixels[row*2+1] = make([]bool, width), make([]bool, width)
		for col, cell := range []rune(line) {
			pixels[row*2][col] = cell == '█' || cell == '▀'
			pixels[row*2+1][col] = cell == '█' || cell == '▄'
		}
	}
	return pixels
}

var startupLogoBitmap = startupLogoPixels()

type startupGeometry struct{ x, y, width, pixelHeight int }

func (m terminalModel) startupGeometry() startupGeometry {
	width := max(m.width, 1)
	baseWidth := len(startupLogoBitmap[0])
	pixels := min(max(int(math.Round(float64(len(startupLogoBitmap))*float64(width)/float64(baseWidth))), 4), max((m.height-2)*2, 4))
	g := startupGeometry{width: width, pixelHeight: pixels, y: max((m.height-(pixels+1)/2)/2, 0)}
	elapsed := m.startup.elapsed
	entrance := m.startup.entranceDuration()
	if elapsed < entrance {
		if m.startup.animation == startupSlide {
			progress := float64(max(elapsed, 0)) / float64(entrance)
			g.x = int(math.Round(float64(width) * math.Pow(1-progress, 3)))
		}
	} else if elapsed > entrance+startupHold {
		progress := min(float64(elapsed-entrance-startupHold)/float64(startupDock), 1)
		eased := progress * progress * (3 - 2*progress)
		g.width = int(math.Round(float64(width) + (float64(baseWidth)-float64(width))*eased))
		g.pixelHeight = int(math.Round(float64(pixels) + (float64(len(startupLogoBitmap))-float64(pixels))*eased))
		g.x = 0
		g.y = int(math.Round(float64(g.y) * (1 - eased)))
	}
	return g
}

func scaledStartupLogo(width, pixelHeight int) []string {
	return scaledStartupBitmap(startupLogoBitmap, width, pixelHeight)
}

func scaledStartupBitmap(bitmap [][]bool, width, pixelHeight int) []string {
	width, pixelHeight = max(width, 1), max(pixelHeight, 1)
	sourceHeight, sourceWidth := len(bitmap), len(bitmap[0])
	rows := make([]string, (pixelHeight+1)/2)
	for row := range rows {
		var line strings.Builder
		for col := range width {
			sourceX := col * sourceWidth / width
			top := bitmap[(row*2)*sourceHeight/pixelHeight][sourceX]
			bottom := row*2+1 < pixelHeight && bitmap[(row*2+1)*sourceHeight/pixelHeight][sourceX]
			switch {
			case top && bottom:
				line.WriteRune('█')
			case top:
				line.WriteRune('▀')
			case bottom:
				line.WriteRune('▄')
			default:
				line.WriteByte(' ')
			}
		}
		rows[row] = line.String()
	}
	return rows
}

func (m terminalModel) renderStartup() string {
	g := m.startupGeometry()
	logo := scaledStartupBitmap(m.startup.bitmap(), g.width, g.pixelHeight)
	rows := make([]string, max(m.height, 1))
	for row := range rows {
		rows[row] = strings.Repeat(" ", max(m.width, 1))
		logoRow := row - g.y
		if logoRow < 0 || logoRow >= len(logo) || g.x >= m.width {
			continue
		}
		// Each block occupies one terminal cell. Clip before styling to prevent any
		// incoming frame wrapping onto the next row or leaving stray background cells.
		cells := []rune(logo[logoRow])
		visible := min(len(cells), m.width-g.x)
		rows[row] = strings.Repeat(" ", g.x) + m.style.brand.Render(string(cells[:visible])) + strings.Repeat(" ", m.width-g.x-visible)
	}
	return strings.Join(rows, "\n")
}
