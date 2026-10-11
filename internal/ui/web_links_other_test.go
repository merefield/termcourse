//go:build !darwin

package ui

import "testing"

func TestWebLinksUseTerminalOpeningOutsideMacOS(t *testing.T) {
	if localWebLinkOpening() || openWebLink("https://example.com") != nil {
		t.Fatal("non-Mac session attempted local opening")
	}
}
