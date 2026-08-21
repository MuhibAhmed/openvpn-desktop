package services

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/MuhibAhmed/openvpn-desktop/internal/ovpn"
	"github.com/MuhibAhmed/openvpn-desktop/internal/settings"
	"github.com/MuhibAhmed/openvpn-desktop/internal/version"
)

// AppService covers everything that is about the app rather than a specific
// connection: readiness, preferences, and where things live on disk.
type AppService struct {
	deps *Deps
}

// NewAppService returns the service.
func NewAppService(deps *Deps) *AppService {
	return &AppService{deps: deps}
}

// Health is what the UI needs to decide whether to show the normal interface or
// an explanation of what is missing.
type Health struct {
	// Ready reports whether a connection could be started right now.
	Ready bool `json:"ready"`
	// Problem explains why not, written for a person.
	Problem string `json:"problem"`
	// OpenVPNVersion is the detected openvpn version banner, empty if absent.
	OpenVPNVersion string `json:"openvpnVersion"`
	// OpenVPNPath is where openvpn.exe was found.
	OpenVPNPath string `json:"openvpnPath"`
	// ProfileDir is where profiles are stored.
	ProfileDir string `json:"profileDir"`
	// LogDir is where openvpn's own logs are written.
	LogDir string `json:"logDir"`
	// AppVersion is our version.
	AppVersion string `json:"appVersion"`
	// Platform is the OS this is running on.
	Platform string `json:"platform"`
}

// Health reports whether the app can do its job, and if not, why.
func (s *AppService) Health() Health {
	h := Health{
		ProfileDir: s.deps.Profiles.Root(),
		LogDir:     filepath.Join(s.deps.DataDir, "logs"),
		AppVersion: version.Current,
		Platform:   runtime.GOOS,
	}

	if install, err := ovpn.Detect(); err == nil {
		h.OpenVPNPath = install.ExePath
		if v, err := install.Version(); err == nil {
			h.OpenVPNVersion = v
		}
	}

	if err := s.deps.Manager.Preflight(); err != nil {
		h.Problem = err.Error()
		return h
	}
	h.Ready = true
	return h
}

// Settings returns the current preferences.
func (s *AppService) Settings() settings.Settings {
	current := s.deps.Settings.Get()
	// Report what Windows actually has registered rather than what we last
	// wrote, so an entry removed elsewhere shows up correctly here.
	current.LaunchOnLogin = settings.LaunchOnLoginEnabled()
	return current
}

// SaveSettings replaces the preferences and applies the ones that have an
// effect outside our own files.
func (s *AppService) SaveSettings(next settings.Settings) (settings.Settings, error) {
	if err := settings.SetLaunchOnLogin(next.LaunchOnLogin); err != nil {
		return s.deps.Settings.Get(), err
	}
	saved, err := s.deps.Settings.Save(next)
	if err != nil {
		return saved, err
	}
	saved.LaunchOnLogin = settings.LaunchOnLoginEnabled()
	return saved, nil
}

// OpenLogFolder reveals the log directory in the file manager, for when a user
// needs to send a log to someone.
func (s *AppService) OpenLogFolder() error {
	dir := filepath.Join(s.deps.DataDir, "logs")
	if err := revealInFileManager(dir); err != nil {
		return fmt.Errorf("could not open %s: %w", dir, err)
	}
	return nil
}

// OpenProfileFolder reveals where profiles are stored.
func (s *AppService) OpenProfileFolder() error {
	dir := s.deps.Profiles.Root()
	if err := revealInFileManager(dir); err != nil {
		return fmt.Errorf("could not open %s: %w", dir, err)
	}
	return nil
}
