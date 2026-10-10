package ui

import "testing"

func TestAppleTerminalTrueColorOptions(t *testing.T) {
	base := []string{"TERM_PROGRAM=Apple_Terminal", "TERM=xterm-256color"}
	if !appleTerminalTrueColor(base, true) {
		t.Fatal("modern Apple Terminal was not upgraded to true colour")
	}
	if appleTerminalTrueColor(base, false) {
		t.Fatal("older Apple Terminal was forced to true colour")
	}
	for _, extra := range []string{
		"COLORTERM=truecolor", "COLORTERM=256color", "NO_COLOR=", "NO_COLOR=0", "NO_COLOR=1", "CLICOLOR=0",
		"TERM=dumb", "TERM=screen-256color", "TERM=tmux-256color", "TERM=xterm",
		"TERM_PROGRAM=iTerm.app", "TMUX=/tmp/socket", "STY=session",
		"SSH_TTY=/dev/ttys001", "SSH_CONNECTION=remote",
	} {
		env := append(append([]string(nil), base...), extra)
		if appleTerminalTrueColor(env, true) {
			t.Fatalf("overrode explicit preference or uncertain terminal: %s", extra)
		}
	}
}
