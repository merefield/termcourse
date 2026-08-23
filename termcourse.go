// Package termcourse provides a terminal client for Discourse forums.
package termcourse

import (
	_ "embed"
	"runtime/debug"
	"strings"
)

//go:embed VERSION
var sourceVersion string

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
	return maintainedVersion()
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
	value := maintainedVersion() + "-dev+" + revision
	if modified {
		value += ".dirty"
	}
	return value
}

func maintainedVersion() string {
	return strings.TrimSpace(sourceVersion)
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(value, "v")
}
