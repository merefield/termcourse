package ui

import tea "charm.land/bubbletea/v2"

const keyLogoHome = "__logo_home__"

// Register only the visible wordmark, excluding header status, subtitle and
// padding. A compact wordmark may be shortened by the forum address.
func (u *UI) addLogoMouseRegions(title []string, width int) {
	if u.navigationLocked && !u.primaryNavAllowed {
		return
	}
	logo := headerLogo
	if len(title) == 1 {
		logo = []string{compactHeaderLogo}
	}
	for y, line := range logo {
		if y >= len(title) {
			break
		}
		actual := []rune(stripANSI(title[y]))
		visible := 0
		for index, r := range []rune(line) {
			if index >= len(actual) || actual[index] != r {
				break
			}
			visible += displayWidth(string(r))
		}
		u.addMouseRegion(0, y, min(visible, width), 1, keyLogoHome)
	}
}

// Home is distinct from selecting the Topics tab: it also resets the filter
// and exits topics/images opened from Topics itself.
func (u *UI) mouseInputKey(msg tea.MouseMsg) string {
	key := u.mouseKey(msg)
	if key == keyLogoHome {
		u.latestRequested = true
		u.requestPrimary(primaryTopics)
		return primaryTabKey(primaryTopics)
	}
	return key
}

func (u *UI) takeRequestedNavigation(active primaryTabID, filter, period string) (primaryTabID, string, string) {
	active = u.takeRequestedPrimary(active)
	if u.latestRequested {
		u.latestRequested = false
		return primaryTopics, "latest", "monthly"
	}
	return active, filter, period
}
