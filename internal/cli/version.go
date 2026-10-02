package cli

import (
	"runtime/debug"
)

// ResolveVersion is the version aja reports: the one goreleaser set through
// -ldflags (the release tag), else the module version a `go install
// …@vX.Y.Z` build records, else "dev".
func ResolveVersion(ldflag string, info *debug.BuildInfo) string {
	if ldflag != "" && ldflag != "dev" {
		return ldflag
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
