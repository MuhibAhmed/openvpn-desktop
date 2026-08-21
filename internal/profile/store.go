package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// metadataFile sits beside each stored config so the UI can list profiles
// without re-parsing every one of them.
const metadataFile = "profile.json"

// configExt is the extension openvpn and the community GUI expect.
const configExt = ".ovpn"

// Profile is a stored, normalised profile.
type Profile struct {
	// ID is the directory name: a slug derived from the original filename,
	// stable for the life of the profile.
	ID string `json:"id"`
	// Name is what the user sees and can change.
	Name string `json:"name"`
	// ImportedAt is when we first stored it.
	ImportedAt time.Time `json:"importedAt"`
	// Source is the path the profile was imported from, for the UI to show.
	Source string `json:"source"`
	// Summary describes the connection without re-reading the config.
	Summary Summary `json:"summary"`
	// Warnings are the import-time findings worth showing the user.
	Warnings []Warning `json:"warnings,omitempty"`

	// Dir is the absolute directory holding the config. Not persisted; it is
	// derived from the store root on load.
	Dir string `json:"-"`
}

// ConfigPath is the absolute path to the profile's .ovpn file.
func (p *Profile) ConfigPath() string {
	return filepath.Join(p.Dir, p.ID+configExt)
}

// Store keeps profiles on disk, one directory per profile.
//
// The location is not ours to choose freely: the OpenVPN Interactive Service
// only launches configs from directories it approves of, so the root must be one
// of those. See docs/windows-launch-path.md.
type Store struct {
	root string
}

// NewStore returns a store rooted at dir, creating it if needed.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("profile store needs a root directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create profile directory %s: %w", dir, err)
	}
	return &Store{root: dir}, nil
}

// Root is the directory profiles are stored under.
func (s *Store) Root() string { return s.root }

// Import normalises the config at srcPath and stores it.
//
// Files the config references are resolved relative to srcPath's directory,
// which is what makes dropping a config together with its certificates work.
func (s *Store) Import(srcPath string) (*Profile, error) {
	srcPath, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(srcPath), err)
	}
	defer f.Close()

	cfg, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s is not a readable OpenVPN profile: %w", filepath.Base(srcPath), err)
	}

	normalised, err := Normalise(cfg, filepath.Dir(srcPath))
	if err != nil {
		return nil, err
	}

	base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	name := displayName(base)
	id, err := s.freeID(slug(base))
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}

	profile := &Profile{
		ID:         id,
		Name:       name,
		ImportedAt: time.Now().UTC(),
		Source:     srcPath,
		Summary:    normalised.Summary,
		Warnings:   normalised.Warnings,
		Dir:        dir,
	}

	// Write the config first: if anything below fails, a half-written profile
	// directory is easier to reason about than metadata with no config.
	if err := os.WriteFile(profile.ConfigPath(), []byte(normalised.Config.String()), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("write profile config: %w", err)
	}
	for _, asset := range normalised.Assets {
		if err := os.WriteFile(filepath.Join(dir, asset.Name), asset.Data, 0o600); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("write %s: %w", asset.Name, err)
		}
	}
	if err := s.writeMetadata(profile); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return profile, nil
}

// ImportPaths imports everything importable in paths, which is what a drop from
// the file manager gives us: possibly several configs, possibly a directory,
// possibly certificate files that are only meaningful next to a config.
//
// It returns the profiles it managed to store and an error for each path it
// could not, so a partial drop still does something useful.
func (s *Store) ImportPaths(paths []string) ([]*Profile, []error) {
	var (
		imported []*Profile
		errs     []error
		configs  []string
	)

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		if info.IsDir() {
			found, err := configsIn(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if len(found) == 0 {
				errs = append(errs, fmt.Errorf("%s contains no .ovpn profile", filepath.Base(p)))
			}
			configs = append(configs, found...)
			continue
		}
		if isConfigFile(p) {
			configs = append(configs, p)
			continue
		}
		// Certificates and keys are not importable on their own; they are only
		// meaningful beside a config. Saying so beats silently ignoring them.
		errs = append(errs, fmt.Errorf("%s is not a profile. Drop the .ovpn file and this will be picked up with it", filepath.Base(p)))
	}

	for _, cfg := range dedupe(configs) {
		p, err := s.Import(cfg)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		imported = append(imported, p)
	}
	return imported, errs
}

// List returns the stored profiles, newest first.
func (s *Store) List() ([]*Profile, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profile directory: %w", err)
	}

	var out []*Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := s.load(e.Name())
		if err != nil {
			// A directory we did not create, or a damaged one. Skip it rather
			// than failing the whole listing.
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ImportedAt.Equal(out[j].ImportedAt) {
			return out[i].Name < out[j].Name
		}
		return out[i].ImportedAt.After(out[j].ImportedAt)
	})
	return out, nil
}

// Get returns one profile by id.
func (s *Store) Get(id string) (*Profile, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// Rename changes a profile's display name. The id and directory stay put, so
// nothing that refers to the profile breaks.
func (s *Store) Rename(id, name string) (*Profile, error) {
	p, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("a profile needs a name")
	}
	p.Name = name
	if err := s.writeMetadata(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Delete removes a profile and everything in its directory, including keys.
func (s *Store) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.root, id)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist", id)
		}
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete profile %q: %w", id, err)
	}
	return nil
}

func (s *Store) load(id string) (*Profile, error) {
	dir := filepath.Join(s.root, id)
	configPath := filepath.Join(dir, id+configExt)
	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("profile %q has no config file", id)
	}

	p := &Profile{ID: id, Dir: dir, Name: displayName(id)}

	hasMetadata := false
	data, err := os.ReadFile(filepath.Join(dir, metadataFile))
	if err == nil {
		// Metadata is a cache, not the source of truth. If it is unreadable we
		// still have a usable profile.
		if err := json.Unmarshal(data, p); err == nil {
			p.ID = id
			p.Dir = dir
			hasMetadata = true
		}
	}
	if p.Name == "" {
		p.Name = displayName(id)
	}

	// Profiles the community GUI created use exactly this layout, so they show
	// up in our list without being imported. They have no metadata of ours,
	// which would otherwise leave them looking blank in the UI, so describe them
	// by reading the config.
	if !hasMetadata {
		if summary, warnings, err := describe(configPath); err == nil {
			p.Summary = summary
			p.Warnings = warnings
		}
	}
	return p, nil
}

// describe derives a summary from a config we did not write, without modifying
// anything on disk.
func describe(configPath string) (Summary, []Warning, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return Summary{}, nil, err
	}
	defer f.Close()

	cfg, err := Parse(f)
	if err != nil {
		return Summary{}, nil, err
	}
	summary := summarise(cfg)
	return summary, reviewWarnings(cfg, summary), nil
}

func (s *Store) writeMetadata(p *Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(p.Dir, metadataFile)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write profile metadata: %w", err)
	}
	return nil
}

// freeID finds an unused directory name, appending a counter if the obvious one
// is taken, so importing two profiles with the same filename works.
func (s *Store) freeID(base string) (string, error) {
	if base == "" {
		base = "profile"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = base + "-" + strconv.Itoa(i+1)
		}
		if _, err := os.Stat(filepath.Join(s.root, candidate)); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("too many profiles named %q", base)
}

// --- helpers --------------------------------------------------------------

func isConfigFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == configExt || ext == ".conf"
}

// configsIn finds profiles in a dropped directory. It deliberately does not
// recurse: one level is enough for the usual "unzipped the provider's bundle"
// case, and deeper trees are more likely to surprise than help.
func configsIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(dir), err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isConfigFile(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

func dedupe(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		key := strings.ToLower(abs)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out
}

// slug turns a filename into a safe directory name.
func slug(s string) string {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// displayName makes a filename presentable: "us-east_1.ovpn" -> "us east 1".
func displayName(s string) string {
	s = strings.TrimSuffix(s, configExt)
	s = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "Profile"
	}
	return s
}

// validID rejects anything that could escape the store root.
func validID(id string) error {
	if id == "" {
		return errors.New("profile id is empty")
	}
	if id != filepath.Base(id) || strings.Contains(id, "..") ||
		strings.ContainsAny(id, `/\:`) {
		return fmt.Errorf("invalid profile id %q", id)
	}
	return nil
}
