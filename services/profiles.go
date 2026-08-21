package services

import (
	"fmt"
	"strings"

	"github.com/MuhibAhmed/openvpn-desktop/internal/creds"
	"github.com/MuhibAhmed/openvpn-desktop/internal/profile"
	"github.com/MuhibAhmed/openvpn-desktop/internal/settings"
)

// ProfileService manages the list of VPN profiles.
type ProfileService struct {
	deps *Deps
}

// NewProfileService returns the service.
func NewProfileService(deps *Deps) *ProfileService {
	return &ProfileService{deps: deps}
}

// ProfileView is a profile as the UI shows it.
type ProfileView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Server is the primary server address, for the subtitle in the list.
	Server string `json:"server"`
	// Protocol is "udp" or "tcp", empty when the profile does not say.
	Protocol string `json:"protocol"`
	// ImportedAt is an RFC3339 timestamp.
	ImportedAt string `json:"importedAt"`
	// NeedsCredentials tells the UI a sign-in will be required.
	NeedsCredentials bool `json:"needsCredentials"`
	// HasSavedCredentials reports whether credentials are in the vault.
	HasSavedCredentials bool `json:"hasSavedCredentials"`
	// RoutesAllTraffic reports whether the profile sends everything through
	// the tunnel.
	RoutesAllTraffic bool `json:"routesAllTraffic"`
	// Warnings are import-time findings worth showing.
	Warnings []string `json:"warnings"`
}

// ImportResult reports what happened to a drop, which may have been partly
// successful.
type ImportResult struct {
	Imported []ProfileView `json:"imported"`
	// Errors are one per path we could not import, already written for a person.
	Errors []string `json:"errors"`
}

// List returns the stored profiles, newest first.
func (s *ProfileService) List() ([]ProfileView, error) {
	profiles, err := s.deps.Profiles.List()
	if err != nil {
		return nil, err
	}
	out := make([]ProfileView, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, s.view(p))
	}
	return out, nil
}

// Import takes the paths from a file drop or a file dialog and stores whatever
// is importable among them.
func (s *ProfileService) Import(paths []string) (ImportResult, error) {
	if len(paths) == 0 {
		return ImportResult{}, fmt.Errorf("nothing to import")
	}
	imported, errs := s.deps.Profiles.ImportPaths(paths)

	result := ImportResult{Imported: make([]ProfileView, 0, len(imported))}
	for _, p := range imported {
		result.Imported = append(result.Imported, s.view(p))
	}
	for _, err := range errs {
		result.Errors = append(result.Errors, err.Error())
	}
	// Only a drop where nothing at all worked is an error; a partial success
	// should still update the list.
	if len(result.Imported) == 0 && len(result.Errors) > 0 {
		return result, fmt.Errorf("%s", strings.Join(result.Errors, "\n"))
	}
	return result, nil
}

// Rename changes a profile's display name.
func (s *ProfileService) Rename(id, name string) (ProfileView, error) {
	p, err := s.deps.Profiles.Rename(id, name)
	if err != nil {
		return ProfileView{}, err
	}
	return s.view(p), nil
}

// Delete removes a profile, its keys, and any saved credentials.
func (s *ProfileService) Delete(id string) error {
	if err := s.deps.Profiles.Delete(id); err != nil {
		return err
	}
	// Leaving credentials behind for a profile the user deleted would be both
	// surprising and a small privacy leak.
	if err := s.deps.Creds.Forget(id); err != nil {
		return err
	}
	if _, err := s.deps.Settings.Update(func(cur *settings.Settings) {
		if cur.AutoConnectProfileID == id {
			cur.AutoConnectProfileID = ""
		}
		if cur.LastProfileID == id {
			cur.LastProfileID = ""
		}
	}); err != nil {
		return err
	}
	return nil
}

// ForgetCredentials removes saved credentials without touching the profile.
func (s *ProfileService) ForgetCredentials(id string) error {
	return s.deps.Creds.Forget(id)
}

// SavedUsername returns the stored username for a profile, so the sign-in
// dialog can prefill it. The password is never returned to the frontend.
func (s *ProfileService) SavedUsername(id string) (string, error) {
	c, err := s.deps.Creds.Get(id, creds.SlotAuth)
	if err != nil {
		if err == creds.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	return c.Username, nil
}

func (s *ProfileService) view(p *profile.Profile) ProfileView {
	v := ProfileView{
		ID:               p.ID,
		Name:             p.Name,
		Protocol:         p.Summary.Proto,
		ImportedAt:       p.ImportedAt.Format("2006-01-02T15:04:05Z07:00"),
		NeedsCredentials: p.Summary.NeedsCredentials,
		RoutesAllTraffic: p.Summary.UsesRedirectGateway,
		Warnings:         make([]string, 0, len(p.Warnings)),
	}
	if len(p.Summary.Remotes) > 0 {
		v.Server = p.Summary.Remotes[0]
	}
	for _, w := range p.Warnings {
		v.Warnings = append(v.Warnings, w.String())
	}
	if _, err := s.deps.Creds.Get(p.ID, creds.SlotAuth); err == nil {
		v.HasSavedCredentials = true
	}
	return v
}
