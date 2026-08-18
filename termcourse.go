// Package termcourse provides a terminal client for Discourse forums.
package termcourse

import (
	"runtime/debug"
	"strings"
)

// Version is the development fallback. Tagged module installs and builds made
// through the Makefile use their embedded semantic version instead.
const Version = "0.2.0"

// buildVersion is populated by the Makefile from the nearest Git tag.
var buildVersion string

// CurrentVersion returns a display-ready version without the conventional v
// prefix used by Go module tags.
func CurrentVersion() string {
	if value := normalizeVersion(buildVersion); value != "" {
		return value
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if value := normalizeVersion(info.Main.Version); value != "" {
			return value
		}
		if value := developmentVersion(info.Settings); value != "" {
			return value
		}
	}
	return Version
}

func developmentVersion(settings []debug.BuildSetting) string {
	revision := ""
	modified := false
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	value := Version + "-dev+" + revision
	if modified {
		value += ".dirty"
	}
	return value
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(value, "v")
}
