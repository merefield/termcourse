package ui

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

type terminalWrites chan string

func (w terminalWrites) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestSixelOverlayFollowsManagedFrame(t *testing.T) {
	output := make(terminalWrites, 16)
	terminal := NewTerminal(nil, output)
	program := tea.NewProgram(terminalModel{state: terminal.state},
		tea.WithInput(nil), tea.WithOutput(output), tea.WithWindowSize(40, 10), tea.WithContext(t.Context()))
	terminal.program = program
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = program.Run()
	}()
	t.Cleanup(func() {
		program.Quit()
		<-done
	})

	const header = "IMAGE FRAME"
	const sixel = "\x1bPq#0;2;100;0;0#0~\x1b\\"
	terminal.render(header, -1, -1, nil, true)
	result := make(chan error, 1)
	go func() { result <- terminal.rawAfterFrame(sixel) }()

	var stream strings.Builder
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case write := <-output:
			stream.WriteString(write)
			if strings.Contains(write, ansi.RequestPrimaryDeviceAttributes) {
				program.Send(uv.PrimaryDeviceAttributesEvent{4})
			}
			if strings.Contains(write, sixel) {
				if frame := strings.Index(stream.String(), header); frame < 0 || frame > strings.Index(stream.String(), sixel) {
					t.Fatalf("Sixel sent before its managed frame: %q", stream.String())
				}
				return
			}
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-timeout.C:
			t.Fatalf("Sixel output never arrived: %q", stream.String())
		}
	}
}

func TestSixelPlacementStaysInsidePane(t *testing.T) {
	pane := paneLayout{x: 2, y: 7, width: 76, height: 19}
	cellSize := image.Pt(10, 20)
	for _, tc := range []struct {
		name          string
		width, height int
		cursor        string
	}{
		{"portrait", 180, 378, "\x1b[8;32H"},
		{"landscape", 760, 90, "\x1b[15;3H"},
		{"fills pane", 760, 378, "\x1b[8;3H"},
		{"too wide", 761, 300, ""},
		{"last band would clip border", 300, 380, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := fmt.Sprintf("\x1bP0;0;0q\"1;1;%d;%d#0!10~\x1b\\", tc.width, tc.height)
			// An encoder may emit extra cursor controls or animation frames.
			encoded := "\x1b[?25l" + frame + "\x1b[?25h" + frame
			sequence, ok := placeSixelInPane([]byte(encoded), pane, cellSize)
			if tc.cursor == "" {
				if ok {
					t.Fatalf("accepted out-of-bounds Sixel raster: %q", sequence)
				}
				return
			}
			if !ok || !strings.Contains(sequence, tc.cursor+frame) ||
				strings.Count(sequence, frame) != 1 || strings.Contains(sequence, "\x1b[?25h") {
				t.Fatalf("want one Sixel frame at %q, got %q", tc.cursor, sequence)
			}
		})
	}
}

func TestKittyPlacementSelectionSurvivesExpansionAndResize(t *testing.T) {
	// Each transition must replace the same explicitly selected placement.
	// An implicit placement can leave stale thumbnail geometry in Ghostty.
	viewports := []struct {
		name                         string
		width, height, columns, rows int
	}{
		{"thumbnail", 48, 6, 21, 6},
		{"expanded", 120, 30, 107, 30},
		{"collapsed", 48, 6, 21, 6},
		{"resized", 64, 14, 50, 14},
		{"expanded-again", 140, 44, 140, 39},
		{"collapsed-again", 48, 6, 21, 6},
	}
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	background := color.RGBA{R: 10, G: 31, B: 2, A: 255}
	renderer := NewScreenRenderer(nil)
	renderer.SetBackground(background)

	// A placement ID is scoped to its image, so different images may reuse it.
	for _, imageID := range []int{0x123456, 0xabcdef} {
		t.Run(strconv.Itoa(imageID), func(t *testing.T) {
			var placementID int
			for _, viewport := range viewports {
				t.Run(viewport.name, func(t *testing.T) {
					columns, rows := kittyCellGeometry(1600, 900, viewport.width, viewport.height, 8, 16)
					if columns != viewport.columns || rows != viewport.rows {
						t.Fatalf("geometry = %dx%d, want %dx%d", columns, rows, viewport.columns, viewport.rows)
					}
					encoded, err := encodeKittyPlacement(source, imageID, columns, rows, nil)
					if err != nil {
						t.Fatal(err)
					}
					control, _, ok := strings.Cut(strings.TrimPrefix(encoded, "\x1b_G"), ";")
					if !ok {
						t.Fatal("missing Kitty graphics control data")
					}
					var options kitty.Options
					if err := options.UnmarshalText([]byte(control)); err != nil {
						t.Fatal(err)
					}
					if options.ID != imageID || options.PlacementID <= 0 || !options.VirtualPlacement || options.Columns != columns || options.Rows != rows {
						t.Fatalf("incorrect virtual placement: %+v", options)
					}
					if placementID == 0 {
						placementID = options.PlacementID
					} else if options.PlacementID != placementID {
						t.Fatalf("placement ID changed from %d to %d", placementID, options.PlacementID)
					}

					// Inspect the cells Bubble Tea receives after frame padding and
					// background filling, including reset boundaries around images.
					lines := kittyPlaceholderLines(imageID, columns, rows)
					frame := renderer.frame(lines, viewport.width+1, viewport.height+1)
					for y, line := range backgroundTestLines(frame) {
						for x, cell := range line {
							assertBackground(t, cell.Style.Bg, background)
							if x >= columns || y >= rows {
								if cell.Style.UnderlineColor != nil {
									t.Fatalf("placement color leaked to padding at %d,%d", x, y)
								}
								continue
							}
							want := string([]rune{kitty.Placeholder, kitty.Diacritic(y), kitty.Diacritic(x)})
							if cell.Content != want || cell.Style.Underline != uv.UnderlineNone {
								t.Fatalf("incorrect placeholder at %d,%d: %+v", x, y, cell)
							}
							if got := kittyTestColorID(cell.Style.Fg); got != imageID {
								t.Fatalf("placeholder image ID = %d, want %d", got, imageID)
							}
							if got := kittyTestColorID(cell.Style.UnderlineColor); got != options.PlacementID {
								t.Fatalf("placeholder placement ID = %d, want %d", got, options.PlacementID)
							}
						}
					}
				})
			}
		})
	}
}

func kittyTestColorID(value color.Color) int {
	if value == nil {
		return 0
	}
	r, g, b, _ := value.RGBA()
	return int(r>>8)<<16 | int(g>>8)<<8 | int(b>>8)
}
