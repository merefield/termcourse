package ui

// paneLayout describes the content cells inside a rounded main pane. Keeping
// these coordinates together makes wrapping, editor sizes and click targets
// agree with the visible borders.
type paneLayout struct {
	x, y, width, height             int
	outerY, outerWidth, outerHeight int
}

func (u *UI) mainPane(header []string, width, height, footerRows int) ([]string, paneLayout) {
	width, height = max(width, 1), max(height, 1)
	screen := make([]string, height)
	copy(screen, header)
	top := min(len(header), height)
	outerHeight := max(height-top-max(footerRows, 0), 0)
	innerWidth, padded := frameInnerWidth(width)
	x := 1
	if padded {
		x++
	}
	return screen, paneLayout{
		x: x, y: top + 1, width: innerWidth, height: max(outerHeight-2, 0),
		outerY: top, outerWidth: width, outerHeight: outerHeight,
	}
}

func (u *UI) drawMainPane(screen []string, pane paneLayout, lines []string) {
	if pane.outerHeight == 0 {
		return
	}
	content := make([]string, pane.height)
	copy(content, lines)
	box := u.style.BodyBox(u.mainPaneTitle, content, pane.outerWidth)
	copy(screen[pane.outerY:pane.outerY+pane.outerHeight], box)
}
