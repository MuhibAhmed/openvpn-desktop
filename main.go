// Command openvpn-desktop is a modern desktop client for OpenVPN.
//
// It does not implement the VPN itself. openvpn.exe remains the tunnel engine;
// this app is the profile manager, connection controller and status surface
// around it. See docs/windows-launch-path.md for how the two connect.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/MuhibAhmed/openvpn-desktop/internal/ovpn"
	"github.com/MuhibAhmed/openvpn-desktop/internal/settings"
	"github.com/MuhibAhmed/openvpn-desktop/internal/vpn"
	"github.com/MuhibAhmed/openvpn-desktop/services"
)

//go:embed all:frontend/dist
var assets embed.FS

// Event names the frontend subscribes to. Registering them gives the generated
// TypeScript bindings the right payload types.
const (
	eventStatus = "connection:status"
	eventLog    = "connection:log"
	// eventImported tells the UI to refresh its profile list after files were
	// dropped on the window, which Go handles rather than the frontend.
	eventImported = "profiles:imported"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	application.RegisterEvent[vpn.Status](eventStatus)
	application.RegisterEvent[vpn.LogLine](eventLog)
	application.RegisterEvent[services.ImportResult](eventImported)

	// Profiles have to live somewhere openvpn's privileged launcher will read
	// from, which is not a directory we get to pick freely.
	deps, err := services.NewDeps(ovpn.UserConfigDir())
	if err != nil {
		return err
	}

	profiles := services.NewProfileService(deps)

	app := application.New(application.Options{
		Name:        "VPN Desktop",
		Description: "A modern desktop client for OpenVPN",
		Services: []application.Service{
			application.NewService(profiles),
			application.NewService(services.NewConnectionService(deps)),
			application.NewService(services.NewAppService(deps)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "VPN Desktop",
		// Sized to fit a 1366x768 laptop at 125% scaling, which is still the
		// floor for a lot of work machines. Anything taller opens partly
		// offscreen there.
		Width:  1000,
		Height: 620,
		// Dropping a profile on the window is the primary way to add one.
		EnableFileDrop: true,
		// Small enough to still be usable when docked in a corner.
		MinWidth:  760,
		MinHeight: 480,
		// The UI paints its own background; matching it here avoids a white
		// flash before the first frame.
		BackgroundColour: application.NewRGB(15, 17, 21),
		URL:              "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	// Launched by Windows at login: come up in the tray rather than opening a
	// window in front of whatever the user is doing.
	if settings.StartedByAutostart(os.Args) {
		window.Hide()
	}

	// Closing the window leaves the app in the tray, so a connection is not torn
	// down by tidying the desktop. This has to be a hook rather than a listener:
	// hooks are dispatched synchronously and cancellation stops the built-in
	// handler that would otherwise destroy the window, whereas listeners run
	// concurrently and cancelling from one is a race.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !deps.Settings.Get().CloseToTray {
			return
		}
		e.Cancel()
		window.Hide()
	})

	// Files dropped on the window are imported here rather than in the
	// frontend, so filesystem paths never cross into JavaScript and the drop
	// path and the browse path share exactly one implementation.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		paths := e.Context().DroppedFiles()
		if len(paths) == 0 {
			return
		}
		result, _ := profiles.Import(paths)
		app.Event.Emit(eventImported, result)
	})

	tray := newTray(app, window, deps)
	tray.attach()

	// Forward connection state to both the UI and the tray. These callbacks run
	// on the session's goroutine, so they only publish and never block.
	deps.Manager.OnStatus(func(status vpn.Status) {
		app.Event.Emit(eventStatus, status)
		tray.update(status)
	})
	deps.Manager.OnLog(func(line vpn.LogLine) {
		app.Event.Emit(eventLog, line)
	})

	go autoConnect(deps)

	return app.Run()
}

// autoConnect brings up the chosen profile at startup. It runs in the
// background so a slow or failing connection never delays the window appearing;
// the result arrives through the normal status stream.
func autoConnect(deps *services.Deps) {
	prefs := deps.Settings.Get()
	if prefs.AutoConnectProfileID == "" {
		return
	}

	// Give the frontend a moment to subscribe, so the user sees the connection
	// attempt rather than arriving after it has already failed.
	time.Sleep(750 * time.Millisecond)

	p, err := deps.Profiles.Get(prefs.AutoConnectProfileID)
	if err != nil {
		return
	}
	// Errors are published as a failed status by the manager itself.
	_ = deps.Manager.Connect(context.Background(), p)
}
