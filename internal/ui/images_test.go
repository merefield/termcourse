package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"
)

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
