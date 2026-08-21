package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/MuhibAhmed/openvpn-desktop/internal/brand"
	"github.com/MuhibAhmed/openvpn-desktop/internal/vpn"
	"github.com/MuhibAhmed/openvpn-desktop/services"
)

// tray is the system tray presence: a status line, a connect/disconnect action,
// a profile list, and a way back to the window.
//
// A VPN client spends nearly all of its life not being looked at, so the tray
// is the primary interface rather than a nicety. It is rebuilt on every status
// change, which is cheap and avoids the class of bug where the menu and the
// real state disagree.
type tray struct {
	app    *application.App
	window application.Window
	deps   *services.Deps

	systray *application.SystemTray

	mu     sync.Mutex
	status vpn.Status
}

func newTray(app *application.App, window application.Window, deps *services.Deps) *tray {
	return &tray{
		app:    app,
		window: window,
		deps:   deps,
		status: vpn.Status{Phase: vpn.PhaseIdle, Detail: "Not connected"},
	}
}

// attach creates the tray icon and its menu.
func (t *tray) attach() {
	t.systray = t.app.SystemTray.New()
	t.systray.SetLabel("VPN Desktop")
	t.systray.SetTooltip("VPN Desktop -- not connected")
	t.systray.OnClick(func() { t.showWindow() })
	t.applyIcon(false)
	t.rebuild()
	t.systray.Run()
}

// update records a new status and refreshes what the tray shows.
func (t *tray) update(status vpn.Status) {
	t.mu.Lock()
	previous := t.status.Phase
	t.status = status
	t.mu.Unlock()

	if t.systray == nil {
		return
	}
	t.systray.SetTooltip("VPN Desktop -- " + trayStatusLine(status))
	if wasConnected, isConnected := previous == vpn.PhaseConnected, status.Phase == vpn.PhaseConnected; wasConnected != isConnected {
		t.applyIcon(isConnected)
	}
	t.rebuild()
}

// applyIcon swaps the tray image so the connection state is readable without
// opening anything, which is the main reason a VPN client lives in the tray.
func (t *tray) applyIcon(connected bool) {
	icon, err := brand.TrayIcon(connected)
	if err != nil {
		// A missing badge is not worth interrupting anyone over; the menu and
		// the tooltip still say what is going on.
		return
	}
	t.systray.SetIcon(icon)
	// Windows uses the dark-mode image against a light taskbar. The mark is a
	// coloured tile either way, so the same one is correct for both.
	t.systray.SetDarkModeIcon(icon)
}

// rebuild replaces the tray menu with one that reflects the current state.
func (t *tray) rebuild() {
	t.mu.Lock()
	status := t.status
	t.mu.Unlock()

	menu := application.NewMenu()

	// The first entry is the status, disabled: it is a label, not an action.
	menu.Add(trayStatusLine(status)).SetEnabled(false)
	if status.Phase == vpn.PhaseConnected && status.RemoteIP != "" {
		menu.Add("    " + status.RemoteIP).SetEnabled(false)
	}
	menu.AddSeparator()

	switch status.Phase {
	case vpn.PhaseIdle, vpn.PhaseFailed:
		t.addProfileMenu(menu, status)
	default:
		menu.Add("Disconnect").OnClick(func(*application.Context) {
			go t.deps.Manager.Disconnect()
		})
		if status.Phase == vpn.PhaseConnected {
			t.addProfileMenu(menu, status)
		}
	}

	menu.AddSeparator()
	menu.Add("Open VPN Desktop").OnClick(func(*application.Context) { t.showWindow() })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		// Leaving a tunnel up after the app is gone would strand the user's
		// traffic in a connection nothing is managing.
		t.deps.Manager.Disconnect()
		t.app.Quit()
	})

	t.systray.SetMenu(menu)
}

// addProfileMenu adds the profile list, either directly or as a submenu when
// there are enough of them to be unwieldy.
func (t *tray) addProfileMenu(menu *application.Menu, status vpn.Status) {
	profiles, err := t.deps.Profiles.List()
	if err != nil || len(profiles) == 0 {
		menu.Add("No profiles yet").SetEnabled(false)
		return
	}

	target := menu
	label := "Connect to"
	if len(profiles) > 8 {
		target = menu.AddSubmenu(label)
	} else {
		menu.Add(label).SetEnabled(false)
	}

	for _, p := range profiles {
		id, name := p.ID, p.Name
		item := target.Add("    " + name)
		if id == status.ProfileID && status.Phase == vpn.PhaseConnected {
			item.SetEnabled(false)
			continue
		}
		item.OnClick(func(*application.Context) {
			go func() {
				if err := t.deps.Manager.Connect(context.Background(), p); err != nil {
					// The failure is already published as a status, which the
					// tray and the window both show; nothing to do here.
					_ = err
				}
			}()
		})
	}
}

func (t *tray) showWindow() {
	t.window.Show()
	t.window.Focus()
}

// trayStatusLine is the one-line summary shown in the menu and the tooltip.
func trayStatusLine(status vpn.Status) string {
	switch status.Phase {
	case vpn.PhaseConnected:
		if status.ProfileName != "" {
			return fmt.Sprintf("Connected to %s", status.ProfileName)
		}
		return "Connected"
	case vpn.PhaseIdle:
		return "Not connected"
	case vpn.PhaseFailed:
		return "Connection failed"
	default:
		if status.Detail != "" {
			return status.Detail
		}
		return string(status.Phase)
	}
}
