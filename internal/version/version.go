// Package version exposes a build-time version string.
//
// The Version variable is populated at build time via -ldflags:
//
//	go build -ldflags "-X 'github.com/gummigudm/opbroker/internal/version.Version=v1.2.3'" ...
//
// Untouched builds report "dev" so local unversioned development is obvious.
package version

// Version is the build-time version string. Set via -ldflags at build time
// (see .repo/scripts/build.sh). Defaults to "dev" for unversioned local builds.
var Version = "dev"
