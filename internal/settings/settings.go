// Package settings holds the handful of user preferences the app keeps.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Theme is the appearance preference. "system" follows the OS.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Settings is the whole preference set. It is small on purpose: every option
// here is one the user asked for, not one we guessed at.
type Settings struct {
	// LaunchOnLogin starts the app minimised when the user signs in.
	LaunchOnLogin bool `json:"launchOnLogin"`
	// CloseToTray keeps the app running when the window is closed, which is
	// what a tray-resident VPN client is expected to do.
	CloseToTray bool `json:"closeToTray"`
	// AutoConnectProfileID is connected on startup, empty for none.
	AutoConnectProfileID string `json:"autoConnectProfileId"`
	// Theme is the appearance preference.
	Theme Theme `json:"theme"`
	// LastProfileID is remembered so the UI can preselect it.
	LastProfileID string `json:"lastProfileId"`
}

// Defaults are what a fresh install behaves like.
func Defaults() Settings {
	return Settings{
		CloseToTray: true,
		Theme:       ThemeSystem,
	}
}

// Store persists settings to a JSON file.
type Store struct {
	path string

	mu      sync.RWMutex
	current Settings
}

// New loads the settings at path, falling back to defaults when the file is
// absent or unreadable. A corrupt settings file is not worth failing startup
// over; losing preferences is recoverable, not launching is not.
func New(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("settings path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create settings directory: %w", err)
	}

	s := &Store{path: path, current: Defaults()}
	data, err := os.ReadFile(path)
	if err == nil {
		loaded := Defaults()
		if err := json.Unmarshal(data, &loaded); err == nil {
			s.current = normalise(loaded)
		}
	}
	return s, nil
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Save replaces the settings and writes them out.
func (s *Store) Save(next Settings) (Settings, error) {
	next = normalise(next)

	s.mu.Lock()
	s.current = next
	s.mu.Unlock()

	if err := s.write(next); err != nil {
		return next, err
	}
	return next, nil
}

// Update applies fn to the current settings and persists the result.
func (s *Store) Update(fn func(*Settings)) (Settings, error) {
	s.mu.Lock()
	next := s.current
	fn(&next)
	next = normalise(next)
	s.current = next
	s.mu.Unlock()

	if err := s.write(next); err != nil {
		return next, err
	}
	return next, nil
}

// write persists settings atomically, so a crash mid-write cannot leave a
// truncated file that loses every preference.
func (s *Store) write(next Settings) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace settings file: %w", err)
	}
	return nil
}

func normalise(s Settings) Settings {
	switch s.Theme {
	case ThemeLight, ThemeDark, ThemeSystem:
	default:
		s.Theme = ThemeSystem
	}
	return s
}
