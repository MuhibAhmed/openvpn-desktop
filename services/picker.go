package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// BrowseAndImport opens a file picker and imports whatever the user chooses.
//
// The dialog lives on the Go side so the frontend never handles filesystem
// paths, which keeps the drag-and-drop path and the browse path identical.
//
// Directories are deliberately not selectable. On Windows, Wails switches to a
// folder picker as soon as CanChooseDirectories is set -- whatever
// CanChooseFiles says -- so allowing both meant the user was asked for the
// folder containing their profile rather than for the profile itself, and could
// only pick one at a time. A dropped folder still works; that path goes through
// ImportPaths, which looks inside it.
func (s *ProfileService) BrowseAndImport() (ImportResult, error) {
	dialog := application.Get().Dialog.OpenFile().
		SetTitle("Choose a VPN profile").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AllowsOtherFileTypes(true)
	dialog.AddFilter("OpenVPN profile", "*.ovpn;*.conf")
	dialog.AddFilter("All files", "*.*")

	paths, err := dialog.PromptForMultipleSelection()
	if err != nil {
		return ImportResult{}, err
	}
	if len(paths) == 0 {
		// Cancelled. Not an error, and not something to report.
		return ImportResult{Imported: []ProfileView{}}, nil
	}
	return s.Import(paths)
}
