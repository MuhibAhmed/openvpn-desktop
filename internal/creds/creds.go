// Package creds stores VPN credentials in the operating system's credential
// vault rather than in our own files.
//
// The app never writes a secret to disk itself. On Windows that means the
// Credential Manager, which encrypts entries against the user's login and is
// the same place other VPN clients keep theirs.
package creds

import "errors"

// ErrNotFound reports that nothing is stored for a profile.
var ErrNotFound = errors.New("no saved credentials")

// Slot distinguishes the separate secrets one profile can need.
//
// A profile commonly has both: openvpn asks for the account credentials and the
// private key passphrase as two independent prompts, and they are not
// interchangeable, so they cannot share one vault entry.
type Slot string

const (
	// SlotAuth is the account username and password (--auth-user-pass).
	//
	// Its empty value is deliberate: it keeps the vault entry name unchanged
	// from before slots existed, so credentials saved by earlier versions are
	// still found.
	SlotAuth Slot = ""

	// SlotPrivateKey is the passphrase protecting an encrypted private key.
	SlotPrivateKey Slot = "key"
)

// Credentials are what a profile needs to sign in.
//
// A second factor is deliberately absent: one-time codes are not worth storing,
// and storing them would defeat the point of having them. For SlotPrivateKey
// only Password is used.
type Credentials struct {
	Username string
	Password string
}

// Empty reports whether there is nothing worth saving.
func (c Credentials) Empty() bool { return c.Username == "" && c.Password == "" }

// Store keeps secrets for profiles.
type Store interface {
	// Get returns the saved credentials for one slot, or ErrNotFound.
	Get(profileID string, slot Slot) (Credentials, error)
	// Save stores credentials, replacing anything already in that slot.
	Save(profileID string, slot Slot, c Credentials) error
	// Delete removes one slot. Deleting what is not there is not an error, so
	// callers can tidy up unconditionally.
	Delete(profileID string, slot Slot) error
	// Forget removes every slot for a profile.
	Forget(profileID string) error
}

// AllSlots is every slot a profile can have, for callers that need to sweep.
func AllSlots() []Slot { return []Slot{SlotAuth, SlotPrivateKey} }
