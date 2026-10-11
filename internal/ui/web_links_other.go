//go:build !darwin

package ui

import tea "charm.land/bubbletea/v2"

func localWebLinkOpening() bool  { return false }
func openWebLink(string) tea.Cmd { return nil }
