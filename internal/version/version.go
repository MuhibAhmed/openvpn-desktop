// Package version holds the application version in one place.
//
// It is its own package so that tooling (the release packager) can read the
// version without importing the service layer, which would drag in Wails and
// everything below it.
package version

// Current is the released version, in the form a human reads in the About
// panel and a release archive is named after.
//
// It is a var rather than a const so a build can override it for a nightly or
// a release candidate:
//
//	go build -ldflags "-X github.com/MuhibAhmed/openvpn-desktop/internal/version.Current=1.0.1-rc1" .
var Current = "1.0.0"
