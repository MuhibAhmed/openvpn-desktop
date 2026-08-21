// Package services is the API the frontend sees.
//
// Wails generates TypeScript bindings from the exported methods here, so this
// package is the contract between the Go side and the UI. Types crossing it are
// kept plain and JSON-tagged for that reason.
package services

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MuhibAhmed/openvpn-desktop/internal/creds"
	"github.com/MuhibAhmed/openvpn-desktop/internal/legacygui"
	"github.com/MuhibAhmed/openvpn-desktop/internal/profile"
	"github.com/MuhibAhmed/openvpn-desktop/internal/settings"
	"github.com/MuhibAhmed/openvpn-desktop/internal/vpn"
)

// appName is used for the per-user data directory.
const appName = "VPN Desktop"

// Deps are the shared pieces the services operate on. They are built once at
// startup and handed to each service, so there is one manager and one store
// rather than one per service.
type Deps struct {
	Profiles *profile.Store
	Manager  *vpn.Manager
	Creds    creds.Store
	Settings *settings.Store

	// DataDir is our per-user directory for logs and settings.
	DataDir string
}

// NewDeps wires everything up.
//
// profileRoot is where profiles are stored, which is not a free choice on
// Windows: the OpenVPN Interactive Service only launches configs from
// directories it trusts. See docs/windows-launch-path.md.
func NewDeps(profileRoot string) (*Deps, error) {
	dataDir, err := dataDirectory()
	if err != nil {
		return nil, err
	}

	profiles, err := profile.NewStore(profileRoot)
	if err != nil {
		return nil, err
	}

	runner, err := vpn.NewRunner()
	if err != nil {
		return nil, err
	}

	credStore, err := creds.NewStore()
	if err != nil {
		return nil, err
	}

	prefs, err := settings.New(filepath.Join(dataDir, "settings.json"))
	if err != nil {
		return nil, err
	}

	deps := &Deps{
		Profiles: profiles,
		Creds:    credStore,
		Settings: prefs,
		DataDir:  dataDir,
	}

	// The manager answers credential prompts from storage, so a secret that was
	// saved once is never asked for again.
	manager, err := vpn.NewManager(runner, filepath.Join(dataDir, "logs"), deps.lookupSecret)
	if err != nil {
		return nil, err
	}
	deps.Manager = manager

	return deps, nil
}

// StoredSecret is a saved answer for one prompt, and where it came from.
type StoredSecret struct {
	Username string
	Password string
	// FromLegacyGUI marks a value recovered from the OpenVPN community GUI
	// rather than from our own vault.
	FromLegacyGUI bool
}

// StoredSecretFor returns the saved answer for a prompt, preferring our own
// vault and falling back to whatever the community GUI has.
func (d *Deps) StoredSecretFor(profileID string, kind vpn.PromptKind) (StoredSecret, bool) {
	if profileID == "" {
		return StoredSecret{}, false
	}

	slot := creds.SlotAuth
	if kind == vpn.PromptPassphrase {
		slot = creds.SlotPrivateKey
	}

	if c, err := d.Creds.Get(profileID, slot); err == nil {
		return StoredSecret{Username: c.Username, Password: c.Password}, true
	}

	saved, err := legacygui.Lookup(profileID)
	if err != nil {
		return StoredSecret{}, false
	}
	if slot == creds.SlotPrivateKey {
		if saved.HasKeyPassphrase {
			return StoredSecret{Password: saved.KeyPassphrase, FromLegacyGUI: true}, true
		}
		return StoredSecret{}, false
	}
	if saved.HasUsername || saved.HasAuthPassword {
		return StoredSecret{
			Username:      saved.Username,
			Password:      saved.AuthPassword,
			FromLegacyGUI: true,
		}, true
	}
	return StoredSecret{}, false
}

// lookupSecret adapts StoredSecretFor to what the connection layer expects.
func (d *Deps) lookupSecret(profileID string, kind vpn.PromptKind) (vpn.Secret, bool) {
	stored, ok := d.StoredSecretFor(profileID, kind)
	if !ok {
		return vpn.Secret{}, false
	}
	return vpn.Secret{Username: stored.Username, Password: stored.Password}, true
}

// dataDirectory returns the per-user directory for our own files. Profiles do
// not live here; openvpn has to be able to read those.
func dataDirectory() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("could not find a place to keep application data: %w", err)
		}
		base = filepath.Join(home, ".vpn-desktop")
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}
