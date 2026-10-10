package ui

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/merefield/termcourse/internal/theme"
)

// Use the same screen-buffer path as Bubble Tea to inspect the actual cells.
func backgroundTestLines(text string) []uv.Line {
	width, height := lipgloss.Width(text), lipgloss.Height(text)
	screen := uv.NewScreenBuffer(width, height)
	uv.NewStyledString(text).Draw(&screen, image.Rect(0, 0, width, height))
	plainLines := strings.Split(ansi.Strip(text), "\n")
	lines := make([]uv.Line, height)
	for y := 0; y < height; y++ {
		for x := 0; x < ansi.StringWidth(plainLines[y]); x++ {
			if x > 0 && screen.CellAt(x-1, y).Width > 1 {
				continue
			}
			lines[y] = append(lines[y], *screen.CellAt(x, y))
		}
	}
	return lines
}

func assertBackground(t *testing.T, got, want color.Color) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("missing background: got %v want %v", got, want)
	}
	r, g, b, a := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	if r != wr || g != wg || b != wb || a != wa {
		t.Fatalf("background = %v, want %v", got, want)
	}
}

func TestBackgroundFillPreservesStylesAndLinks(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	background, primary := lipgloss.Color("#0a1f02"), lipgloss.Color("#2cfc03")
	label := lipgloss.NewStyle().Foreground(primary).Bold(true)
	link := ansi.SetHyperlink("https://example.com", "")
	input := label.Render("A") + " " + link +
		label.Background(primary).Render("B") + ansi.ResetHyperlink() +
		"\x1b[0;38;2;0;49;0mC\x1b[49mD\x1b[48:2::0:49:0mE\x1b[mF"
	out := withDefaultBackground(input, background)
	if ansi.Strip(out) != "A BCDEF" {
		t.Fatal("background fill changed text", ansi.Strip(out))
	}
	cells := backgroundTestLines(out)[0]
	for _, index := range []int{0, 1, 3, 4, 6} {
		assertBackground(t, cells[index].Style.Bg, background)
	}
	assertBackground(t, cells[2].Style.Bg, primary)
	assertBackground(t, cells[5].Style.Bg, color.RGBA{0, 49, 0, 255})
	if cells[0].Style.Attrs&uv.AttrBold == 0 || cells[2].Link.URL != "https://example.com" || cells[3].Link.URL != "" {
		t.Fatal("background fill changed text attributes or hyperlink boundaries")
	}
	following := backgroundTestLines(out + "Z")[0]
	if following[len(following)-1].Style.Bg != nil {
		t.Fatal("background leaked outside rendered component")
	}
}

func TestPanelBackgroundSurvivesNestedStyles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	catalog, err := theme.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range catalog.All() {
		for _, mode := range []string{"truecolor", "256", "16"} {
			t.Run(value.Name+"/"+mode, func(t *testing.T) {
				t.Setenv("TERMCOURSE_COLOR_MODE", mode)
				s := NewStyle(value, &bytes.Buffer{})
				body := s.Text("label", roleAccent) + "  " + s.Text("value", roleListMeta) + " tail"
				for _, lines := range [][]string{s.HeaderBox("PANEL", []string{body, ""}, 36), s.BarBox("BAR", []string{body, ""}, 36)} {
					for _, line := range backgroundTestLines(strings.Join(lines, "\n")) {
						if len(line) != 36 {
							t.Fatalf("panel width = %d", len(line))
						}
						for _, cell := range line {
							assertBackground(t, cell.Style.Bg, s.header.GetBackground())
						}
					}
				}
			})
		}
	}
}

func TestRendererPaintsCanvasAndPreservesPanelAndSelection(t *testing.T) {
	for _, mode := range []string{"truecolor", "256", "16"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TERMCOURSE_COLOR_MODE", mode)
			value := testTheme()
			value.Background = "#101820"
			u := New(nil, Options{Theme: value, Output: &bytes.Buffer{}})
			s := u.style
			for _, size := range [][2]int{{20, 8}, {80, 24}, {120, 40}} {
				width, height := size[0], size[1]
				content := append(s.HeaderBox("PANEL", []string{s.Text("nested", roleAccent) + " tail"}, width), s.Selected("selected"))
				out := u.renderer.frame(content, width, height)
				if lipgloss.Width(out) != width || lipgloss.Height(out) != height {
					t.Fatal("canvas geometry changed")
				}
				for y, line := range backgroundTestLines(out) {
					for x, cell := range line {
						want := s.body.GetBackground()
						if y < 3 {
							want = s.header.GetBackground()
						}
						if y == 3 && x < len("selected") {
							want = s.selected.GetBackground()
						}
						assertBackground(t, cell.Style.Bg, want)
					}
				}
			}
		})
	}
}

func TestThemeSwitchUpdatesCanvasBackgroundAndNone(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	first, second := testTheme(), testTheme()
	first.Name, second.Name = "first", "second"
	first.Background = "#101820"
	second.Background = "#203040"
	none := testTheme()
	none.Name = "none"
	u := New(nil, Options{Theme: first, Themes: []theme.Theme{first, second, none}, Output: &bytes.Buffer{}})
	for _, value := range []theme.Theme{first, second, none, first} {
		out := u.renderer.frame([]string{"text"}, 12, 4)
		if value.Background == "" {
			if out != normalizeScreen([]string{"text"}, 12, 4) {
				t.Fatal("background none added canvas styling")
			}
		} else {
			for _, line := range backgroundTestLines(out) {
				for _, cell := range line {
					assertBackground(t, cell.Style.Bg, lipgloss.Color(string(value.Background)))
				}
			}
		}
		u.cycleTheme()
	}
	input := "\x1b[31mred\x1b[m"
	for _, background := range []color.Color{nil, lipgloss.NoColor{}} {
		if withDefaultBackground(input, background) != input {
			t.Fatal("absent background changed text")
		}
	}
}

func TestExplicitColorModesOverrideAutomaticProfile(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("TERM", "xterm-256color")
	for _, tc := range []struct {
		mode    string
		profile colorprofile.Profile
	}{
		{"truecolor", colorprofile.TrueColor}, {"256", colorprofile.ANSI256}, {"16", colorprofile.ANSI},
	} {
		t.Setenv("TERMCOURSE_COLOR_MODE", tc.mode)
		got, mode := resolveColorProfile(&bytes.Buffer{})
		if got != tc.profile || mode != tc.mode {
			t.Fatalf("profile = %v/%s", got, mode)
		}
	}
	t.Setenv("TERMCOURSE_COLOR_MODE", "auto")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("FORCE_COLOR", "")
	got, _ := resolveColorProfile(&bytes.Buffer{})
	if got != colorprofile.NoTTY {
		t.Fatalf("redirected output profile = %v", got)
	}
}
