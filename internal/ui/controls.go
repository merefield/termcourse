package ui

import "strings"

type laidOutControl struct {
	key   string
	label string
	x     int
	width int
}

func controlKey(segment string) string {
	prefix, _, _ := strings.Cut(strings.TrimSpace(segment), ":")
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	prefix, _, _ = strings.Cut(prefix, ",")
	prefix, _, _ = strings.Cut(prefix, "/")
	prefix = strings.TrimSpace(prefix)
	switch prefix {
	case "↵", "enter", "entrée":
		return "enter"
	case "esc", "escape":
		return "esc"
	case "ctrl+d":
		return "ctrl+d"
	}
	if len([]rune(prefix)) == 1 {
		return prefix
	}
	return ""
}

func controlCompactLabel(segment string) string {
	prefix, _, found := strings.Cut(strings.TrimSpace(segment), ":")
	if !found {
		return strings.ToUpper(strings.TrimSpace(segment))
	}
	return strings.ToUpper(strings.TrimSpace(prefix))
}

func layoutControls(value string, width int) []laidOutControl {
	if width <= 0 || strings.TrimSpace(value) == "" {
		return nil
	}
	segments := strings.Split(value, " | ")
	type tier struct {
		labels    []string
		separator string
	}
	build := func(compact bool, framed bool, separator string) tier {
		labels := make([]string, len(segments))
		for index, segment := range segments {
			label := strings.ToUpper(strings.TrimSpace(segment))
			if compact {
				label = controlCompactLabel(segment)
			} else if prefix, action, found := strings.Cut(strings.TrimSpace(segment), ":"); found && controlKey(segment) != "" {
				label = "(" + strings.ToUpper(strings.TrimSpace(prefix)) + ") " + strings.ToUpper(strings.TrimSpace(action))
			}
			if framed {
				label = "[ " + label + " ]"
			}
			labels[index] = label
		}
		return tier{labels: labels, separator: separator}
	}
	tiers := []tier{
		build(false, true, "  "),
		build(true, true, " "),
		build(true, false, " "),
		build(true, false, ""),
	}
	chosen := tiers[len(tiers)-1]
	for _, candidate := range tiers {
		total := max(len(candidate.labels)-1, 0) * displayWidth(candidate.separator)
		for _, label := range candidate.labels {
			total += displayWidth(label)
		}
		if total <= width {
			chosen = candidate
			break
		}
	}

	x := 0
	controls := make([]laidOutControl, 0, len(segments))
	for index, label := range chosen.labels {
		labelWidth := displayWidth(label)
		if x+labelWidth > width {
			break
		}
		controls = append(controls, laidOutControl{
			key: controlKey(segments[index]), label: label, x: x, width: labelWidth,
		})
		x += labelWidth
		if index != len(chosen.labels)-1 {
			x += displayWidth(chosen.separator)
		}
	}
	return controls
}

func (s *Style) renderControls(controls []laidOutControl, hovered string) string {
	parts := make([]string, 0, len(controls)*2)
	previousEnd := 0
	for _, control := range controls {
		if gap := control.x - previousEnd; gap > 0 {
			parts = append(parts, strings.Repeat(" ", gap))
		}
		controlStyle := s.control
		if control.key != "" && control.key == hovered {
			controlStyle = s.controlHot
		}
		parts = append(parts, controlStyle.Render(control.label))
		previousEnd = control.x + control.width
	}
	return strings.Join(parts, "")
}

func (u *UI) controlsFooter(value string, width, y int) string {
	themeControl := u.t("ui.controls.theme")
	if strings.TrimSpace(value) == "" {
		value = themeControl
	} else {
		value = themeControl + " | " + value
	}
	controls := layoutControls(value, width)
	for _, control := range controls {
		if control.key != "" {
			u.addMouseRegion(control.x, y, control.width, 1, control.key)
		}
	}
	return u.style.renderControls(controls, u.hoveredControl)
}

func (u *UI) placeControlsFooter(screen []string, value string, width int) {
	if len(screen) == 0 {
		return
	}
	y := len(screen) - 1
	screen[y] = u.controlsFooter(value, width, y)
}
