package ui

import (
	"strings"
)

// Apple Terminal gained true colour in macOS 26, but existing profiles can
// still advertise xterm-256color. Respect explicit colour preferences and
// avoid inferring the capabilities of a remote host or terminal multiplexer.
func appleTerminalTrueColor(environ []string, appleTrueColor bool) bool {
	env := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	_, noColor := env["NO_COLOR"]
	if !appleTrueColor || env["TERM_PROGRAM"] != "Apple_Terminal" ||
		env["TERM"] != "xterm-256color" || env["COLORTERM"] != "" ||
		noColor || env["CLICOLOR"] == "0" ||
		env["TMUX"] != "" || env["STY"] != "" ||
		env["SSH_TTY"] != "" || env["SSH_CONNECTION"] != "" {
		return false
	}
	return true
}
