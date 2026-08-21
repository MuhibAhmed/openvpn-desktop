//go:build windows

package settings

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKey is the per-user autostart list. Per-user rather than machine-wide is
// deliberate: it needs no elevation, and a VPN client is a personal choice
// rather than a machine policy.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// runValueName is our entry in that list.
const runValueName = "VPNDesktop"

// autostartFlag is passed to the app when Windows launches it at login, so it
// can come up in the tray instead of opening a window in the user's face.
const autostartFlag = "--autostart"

// SetLaunchOnLogin adds or removes the app from the current user's autostart
// list.
func SetLaunchOnLogin(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open the autostart registry key: %w", err)
	}
	defer key.Close()

	if !enabled {
		err := key.DeleteValue(runValueName)
		if err != nil && !strings.Contains(err.Error(), "cannot find") {
			return fmt.Errorf("remove the autostart entry: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate this executable: %w", err)
	}
	// Quote the path: Program Files has a space in it and an unquoted value is
	// a classic way to end up launching the wrong thing.
	if err := key.SetStringValue(runValueName, `"`+exe+`" `+autostartFlag); err != nil {
		return fmt.Errorf("write the autostart entry: %w", err)
	}
	return nil
}

// LaunchOnLoginEnabled reports whether the autostart entry is present, so the
// UI reflects what Windows actually thinks rather than what we last recorded.
func LaunchOnLoginEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValueName)
	return err == nil && value != ""
}

// StartedByAutostart reports whether this process was launched at login.
func StartedByAutostart(args []string) bool {
	for _, a := range args {
		if a == autostartFlag {
			return true
		}
	}
	return false
}
