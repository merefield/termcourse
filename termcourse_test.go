package termcourse

import (
	"runtime/debug"
	"testing"
)

func TestCurrentVersionUsesBuildOverride(t *testing.T) {
	previous := buildVersion
	buildVersion = "v0.2.1"
	t.Cleanup(func() { buildVersion = previous })

	if actual := CurrentVersion(); actual != "0.2.1" {
		t.Fatalf("CurrentVersion() = %q, want 0.2.1", actual)
	}
}

func TestNormalizeVersionRejectsDevelopmentMarker(t *testing.T) {
	if actual := normalizeVersion("(devel)"); actual != "" {
		t.Fatalf("normalizeVersion((devel)) = %q", actual)
	}
}

func TestDevelopmentVersionIncludesRevisionAndDirtyState(t *testing.T) {
	actual := developmentVersion([]debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.modified", Value: "true"},
	})
	if actual != "0.2.1-dev+0123456789ab.dirty" {
		t.Fatalf("developmentVersion() = %q", actual)
	}
}
