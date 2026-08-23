package termcourse

import (
	"runtime/debug"
	"testing"
)

func TestCurrentVersionUsesBuildOverride(t *testing.T) {
	previous := buildVersion
	buildVersion = "v9.8.7"
	t.Cleanup(func() { buildVersion = previous })

	if actual := CurrentVersion(); actual != "9.8.7" {
		t.Fatalf("CurrentVersion() = %q, want 9.8.7", actual)
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
	if actual != maintainedVersion()+"-dev+0123456789ab.dirty" {
		t.Fatalf("developmentVersion() = %q", actual)
	}
}
