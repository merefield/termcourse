package ui

import (
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/merefield/termcourse/internal/discourse"
)

func TestMacWebLinkOpeningIsLocalAndLiteral(t *testing.T) {
	for _, remote := range []string{"SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT"} {
		if localWebLinkEnvironment(func(key string) string {
			if key == remote {
				return "remote"
			}
			return ""
		}) {
			t.Fatal("remote Mac launched host browser")
		}
	}
	if !localWebLinkEnvironment(func(string) string { return "" }) {
		t.Fatal("local opening unavailable")
	}
	target := "https://example.com/?q=$(touch%20should-not-run)&x=1"
	command := webLinkCommand(target)
	if command.Path != "/usr/bin/open" || !reflect.DeepEqual(command.Args, []string{"/usr/bin/open", target}) {
		t.Fatal("URL modified or passed to shell")
	}
}

func TestMacLinkClickConsumesPostSelectionOnlyOnLinkCells(t *testing.T) {
	for _, key := range []string{"SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT"} {
		t.Setenv(key, "")
	}
	m := startupPendingModel(t)
	m.startup = startupState{}
	m.state.mouse.Store(true)
	u := &UI{style: m.style, linksEnabled: true, mouseEnabled: true}
	target := "https://example.com/release?one=1&two=2"
	lines := u.postBlock(discourse.JSON{"username": "member", "raw": "[release](" + target + ") after"}, true, 35)
	m.content = normalizeScreen(lines, 35, len(lines))
	x, y := -1, -1
	for row := range lines {
		for col := 0; col < 35; col++ {
			if webLinkAt(m.content, col, row) == target {
				x, y = col, row
				break
			}
		}
		if x >= 0 {
			break
		}
	}
	if x < 0 {
		t.Fatal("no clickable rendered post")
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	// Never execute cmd: tests must not launch a browser.
	if cmd == nil || len(m.state.inputs) != 0 {
		t.Fatal("click selected post or failed to open link")
	}
	for _, msg := range []tea.MouseMsg{tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}, tea.MouseMotionMsg{X: x, Y: y}, tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}} {
		_, cmd = m.Update(msg)
		if cmd != nil || len(m.state.inputs) != 1 {
			t.Fatal("ordinary mouse input consumed")
		}
		<-m.state.inputs
	}
	for _, marker := range []string{"SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT"} {
		t.Setenv(marker, "remote")
		_, cmd = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if cmd != nil || len(m.state.inputs) != 1 {
			t.Fatal("remote session opened server browser")
		}
		<-m.state.inputs
		t.Setenv(marker, "")
	}
	m.state.mouse.Store(false)
	_, cmd = m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if cmd != nil {
		t.Fatal("mouse-disabled session opened link")
	}
	<-m.state.inputs
	m.Update(webLinkOpenedMsg{err: errors.New("browser failed")})
	if len(m.state.inputs) != 1 {
		t.Fatal("opener error not delivered")
	}
}
