// Package vpn owns the lifecycle of a tunnel: launching openvpn, driving its
// management interface, and presenting the result as something a UI can render.
//
// Launching is platform-specific and privileged; everything above it is not.
// Runner is the seam between those two worlds, so the state machine, the
// credential handling and the status model are shared across platforms.
package vpn

import (
	"context"
	"errors"
)

// ErrUnsupportedPlatform is returned by NewRunner where no launcher exists yet.
var ErrUnsupportedPlatform = errors.New("starting openvpn is not implemented on this platform")

// StartRequest is everything a launcher needs to bring up one tunnel.
type StartRequest struct {
	// ConfigPath is the absolute path to the .ovpn file. On Windows it must sit
	// inside a directory the Interactive Service accepts.
	ConfigPath string

	// ManagementHost and ManagementPort are where openvpn should listen for us.
	ManagementHost string
	ManagementPort int
	// ManagementPassword is handed to openvpn over stdin so it never reaches
	// disk. The same value authenticates our management connection.
	ManagementPassword string

	// LogPath is where openvpn writes its own log. Worth setting even though we
	// read logs over the management interface: if openvpn dies before that
	// interface opens, this file is the only explanation.
	LogPath string

	// Verbosity is openvpn's --verb. Three is enough to explain a failure
	// without burying it.
	Verbosity int
}

// Process is a running openvpn instance.
type Process interface {
	// PID identifies the process, for diagnostics.
	PID() int
	// Done is closed when openvpn exits, however it exits.
	Done() <-chan struct{}
	// Stop asks openvpn to shut down. It is a fallback for when the management
	// connection is not usable; the normal path is a SIGTERM over management.
	Stop() error
	// Close releases the launcher's resources. It does not stop openvpn.
	Close() error
}

// Runner launches openvpn with the privileges it needs.
type Runner interface {
	// Start brings up one tunnel. The returned Process is valid until Close.
	Start(ctx context.Context, req StartRequest) (Process, error)
	// Preflight reports why tunnels cannot be started right now, so the UI can
	// explain the problem before the user tries to connect.
	Preflight() error
}
