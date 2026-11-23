package branding

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

// packageVersion is set by the linker when building a release package.
var packageVersion string

func Version() string {
	if packageVersion != "" {
		return packageVersion
	}
	return strings.TrimSpace(versionFile)
}
