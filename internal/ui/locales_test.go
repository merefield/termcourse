package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/merefield/termcourse/internal/i18n"
)

func TestEveryLocaleFitsNavigationAndKeepsMouseTargets(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	for _, locale := range i18n.AvailableLocales {
		for _, width := range []int{20, 32, 52, 80, 120, 180} {
			t.Run(fmt.Sprintf("%s/%d", locale, width), func(t *testing.T) {
				u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: locale, mouseEnabled: true, displayURL: "forum.example", options: Options{Username: "member"}}
				screen := make([]string, 30)
				header := u.navigationHeader("", primaryTopics, "topics", "top", "monthly", nil, width, len(screen))
				copy(screen, header)
				u.placeControlsFooter(screen, u.t("ui.controls.topic_list"), width, u.t("ui.guidance.topic_list"))
				for _, line := range strings.Split(normalizeScreen(screen, width, len(screen)), "\n") {
					if visibleWidth(line) != width {
						t.Fatalf("line width is %d, want %d: %q", visibleWidth(line), width, line)
					}
				}
				keys := map[string]bool{}
				for _, region := range u.mouseRegions {
					if region.x0 < 0 || region.x1 > width || region.y0 < 0 || region.y1 > len(screen) {
						t.Fatalf("out-of-bounds target: %+v", region)
					}
					for _, x := range []int{region.x0, region.x1 - 1} {
						if key := u.mouseKey(tea.MouseClickMsg{X: x, Y: region.y0, Button: tea.MouseLeft}); key != region.key {
							t.Fatalf("click on %q produced %q", region.key, key)
						}
					}
					keys[region.key] = true
				}
				if !keys[primaryTabKey(primaryTopics)] || !keys[contextTabKey("top")] {
					t.Fatal("selected navigation target missing")
				}
				if width >= 80 {
					for _, key := range []string{"t", "f", "g", "q"} {
						if !keys[key] {
							t.Errorf("footer shortcut %q lost", key)
						}
					}
				}
				// Exercise the other navigation rails, including long composer labels.
				for _, kind := range []string{"search", "compose", "notifications"} {
					line := u.contextNavigationLine(kind, "", "", width)
					if visibleWidth(line.content) != width {
						t.Errorf("%s rail exceeded width", kind)
					}
					for _, tab := range line.tabs {
						if tab.x < 0 || tab.x+tab.width > width {
							t.Errorf("%s tab outside frame: %+v", kind, tab)
						}
					}
				}
			})
		}
	}
}

func TestTurkishCasingReachesTabsAndFooterBeforeLayout(t *testing.T) {
	u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "tr", mouseEnabled: true, options: Options{Username: "member"}}
	u.style.Theme.Name = "fairground"
	nav := stripANSI(u.primaryNavigationLine(primaryNotifications, 180).content)
	if !strings.Contains(nav, "BİLDİRİMLER") {
		t.Fatal("Turkish navigation casing incorrect", nav)
	}
	footer := stripANSI(u.controlsFooter(u.t("ui.controls.composer", "status", 12), 240, 5))
	if !strings.Contains(footer, "FAIRGROUND") {
		t.Fatal("locale casing changed the theme name", footer)
	}
	if !strings.Contains(footer, "BİTİR") {
		t.Fatal("Turkish footer casing incorrect", footer)
	}
}
