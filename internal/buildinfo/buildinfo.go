// Package buildinfo carries the version stamped into released binaries.
//
// A racked combiner has no Internet and no Go toolchain, so `combiner -version`
// is the only way to tell what a field unit is actually running. Release builds
// set Version via -ldflags (see the Makefile); everything else falls back to the
// VCS revision the Go toolchain records, so a laptop build still says something
// more useful than "dev".
package buildinfo

import "runtime/debug"

// Version is overwritten at link time:
//
//	-X github.com/msnow/vunet-dante-combiner-2000/internal/buildinfo.Version=0.1.0
var Version = "dev"

// String returns the release version, or a VCS-derived description for
// unstamped builds (e.g. "dev (a1b2c3d, modified)").
func String() string {
	if Version != "dev" {
		return Version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}

	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return Version
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if modified {
		return Version + " (" + rev + ", modified)"
	}
	return Version + " (" + rev + ")"
}
