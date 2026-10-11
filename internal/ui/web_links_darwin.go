package ui

import (
	tea "charm.land/bubbletea/v2"
	"os"
	"os/exec"
)

// Over SSH the terminal should open links on the user's own machine.
func localWebLinkOpening() bool { return localWebLinkEnvironment(os.Getenv) }
func localWebLinkEnvironment(getenv func(string) string) bool {
	return getenv("SSH_TTY") == "" && getenv("SSH_CONNECTION") == "" && getenv("SSH_CLIENT") == ""
}
func webLinkCommand(target string) *exec.Cmd { return exec.Command("/usr/bin/open", target) }
func openWebLink(target string) tea.Cmd {
	return func() tea.Msg { return webLinkOpenedMsg{err: webLinkCommand(target).Run()} }
}
